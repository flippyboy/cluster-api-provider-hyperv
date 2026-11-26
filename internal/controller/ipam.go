package controller

import (
	"context"
	"fmt"
	"net/netip"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/cloudinit"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
)

// ipamResult is the outcome of one pass over a machine's IPAddressClaims.
type ipamResult struct {
	// Addresses holds one entry per pool, in pool order, once every claim is bound.
	Addresses []cloudinit.StaticAddress
	// Pending names the first claim that has no IPAddress yet.
	Pending string
}

// ipAddressClaimName is stable per machine and pool index, so a requeue finds
// the same claim.
func ipAddressClaimName(hvMachine *infrav1.HyperVMachine, index int) string {
	return fmt.Sprintf("%s-%d", hvMachine.Name, index)
}

// reconcileIPAddresses creates a claim for each pool and returns the bound
// addresses. A claim is owned by the HyperVMachine. Garbage collection releases
// it only after the finalizer has removed the VM, so an address is never
// handed to a new machine while the old VM still holds it.
func reconcileIPAddresses(ctx context.Context, c client.Client, hvMachine *infrav1.HyperVMachine, cluster *clusterv1.Cluster) (ipamResult, error) {
	var res ipamResult
	for i, pool := range hvMachine.Spec.Network.AddressesFromPools {
		claim, err := ensureIPAddressClaim(ctx, c, hvMachine, cluster, i, pool)
		if err != nil {
			return ipamResult{}, err
		}
		if claim.Status.AddressRef.Name == "" {
			if res.Pending == "" {
				res.Pending = claim.Name
			}
			continue
		}
		addr := &ipamv1.IPAddress{}
		key := client.ObjectKey{Namespace: hvMachine.Namespace, Name: claim.Status.AddressRef.Name}
		if err := c.Get(ctx, key, addr); err != nil {
			if apierrors.IsNotFound(err) {
				if res.Pending == "" {
					res.Pending = claim.Name
				}
				continue
			}
			return ipamResult{}, err
		}
		static, err := staticAddressFrom(addr)
		if err != nil {
			return ipamResult{}, &invalidIPAddressError{name: addr.Name, err: err}
		}
		res.Addresses = append(res.Addresses, static)
	}
	if res.Pending != "" {
		res.Addresses = nil
	}
	return res, nil
}

func ensureIPAddressClaim(ctx context.Context, c client.Client, hvMachine *infrav1.HyperVMachine, cluster *clusterv1.Cluster, index int, pool infrav1.IPPoolReference) (*ipamv1.IPAddressClaim, error) {
	claim := &ipamv1.IPAddressClaim{}
	key := client.ObjectKey{Namespace: hvMachine.Namespace, Name: ipAddressClaimName(hvMachine, index)}
	err := c.Get(ctx, key, claim)
	if err == nil {
		if !isOwnedBy(claim, hvMachine) {
			return nil, fmt.Errorf("IPAddressClaim %s exists and is not owned by HyperVMachine %s", key.Name, hvMachine.Name)
		}
		return claim, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	claim = &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace,
			Name:      key.Name,
			Labels:    map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         infrav1.GroupVersion.String(),
				Kind:               "HyperVMachine",
				Name:               hvMachine.Name,
				UID:                hvMachine.UID,
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}},
		},
		Spec: ipamv1.IPAddressClaimSpec{
			ClusterName: cluster.Name,
			PoolRef: ipamv1.IPPoolReference{
				APIGroup: pool.APIGroup,
				Kind:     pool.Kind,
				Name:     pool.Name,
			},
		},
	}
	if err := c.Create(ctx, claim); err != nil {
		return nil, fmt.Errorf("create IPAddressClaim %s: %w", key.Name, err)
	}
	return claim, nil
}

func isOwnedBy(obj metav1.Object, owner *infrav1.HyperVMachine) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == owner.UID {
			return true
		}
	}
	return false
}

func staticAddressFrom(addr *ipamv1.IPAddress) (cloudinit.StaticAddress, error) {
	ip, err := netip.ParseAddr(addr.Spec.Address)
	if err != nil {
		return cloudinit.StaticAddress{}, fmt.Errorf("address %q is not an IP address", addr.Spec.Address)
	}
	if addr.Spec.Prefix == nil {
		return cloudinit.StaticAddress{}, fmt.Errorf("address %s has no prefix", ip)
	}
	prefix := int(*addr.Spec.Prefix)
	if prefix < 1 || prefix > ip.BitLen() {
		return cloudinit.StaticAddress{}, fmt.Errorf("address %s has prefix %d", ip, prefix)
	}
	if addr.Spec.Gateway != "" {
		gw, err := netip.ParseAddr(addr.Spec.Gateway)
		if err != nil || gw.Is4() != ip.Is4() {
			return cloudinit.StaticAddress{}, fmt.Errorf("gateway %q does not match address %s", addr.Spec.Gateway, ip)
		}
	}
	return cloudinit.StaticAddress{Address: ip.String(), Prefix: prefix, Gateway: addr.Spec.Gateway}, nil
}

type invalidIPAddressError struct {
	name string
	err  error
}

func (e *invalidIPAddressError) Error() string {
	return fmt.Sprintf("IPAddress %s: %v", e.name, e.err)
}

func (e *invalidIPAddressError) Unwrap() error { return e.err }
