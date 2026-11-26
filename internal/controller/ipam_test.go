package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ipamv1 "sigs.k8s.io/cluster-api/api/ipam/v1beta2"
)

const testPool = "vlan60"

func ipamHyperVMachine() *infrav1.HyperVMachine {
	m := testHyperVMachine()
	m.Spec.Network = infrav1.NetworkSpec{
		AddressesFromPools: []infrav1.IPPoolReference{{
			APIGroup: "ipam.cluster.x-k8s.io",
			Kind:     "InClusterIPPool",
			Name:     testPool,
		}},
		Nameservers:   []string{"10.0.0.1"},
		SearchDomains: []string{"lab.example"},
	}
	return m
}

func getClaim(t *testing.T, c client.Client, name string) *ipamv1.IPAddressClaim {
	t.Helper()
	claim := &ipamv1.IPAddressClaim{}
	if err := c.Get(testContext(), types.NamespacedName{Namespace: testNamespace, Name: name}, claim); err != nil {
		t.Fatal(err)
	}
	return claim
}

// bindClaim does what the IPAM provider does: create an IPAddress and point
// the claim at it.
func bindClaim(t *testing.T, c client.Client, claimName string, spec ipamv1.IPAddressSpec) {
	t.Helper()
	ctx := testContext()
	addr := &ipamv1.IPAddress{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: claimName},
		Spec:       spec,
	}
	addr.Spec.ClaimRef = ipamv1.IPAddressClaimReference{Name: claimName}
	addr.Spec.PoolRef = ipamv1.IPPoolReference{APIGroup: "ipam.cluster.x-k8s.io", Kind: "InClusterIPPool", Name: testPool}
	if err := c.Create(ctx, addr); err != nil {
		t.Fatal(err)
	}
	claim := getClaim(t, c, claimName)
	claim.Status.AddressRef = ipamv1.IPAddressReference{Name: addr.Name}
	if err := c.Status().Update(ctx, claim); err != nil {
		t.Fatal(err)
	}
}

func ipamClient(t *testing.T, controlPlane bool) client.Client {
	t.Helper()
	hvCluster := testHyperVCluster(infrav1.TransportSSH, "")
	hvCluster.Spec.KubeVIP.Enabled = true
	machine := testOwnerMachine()
	if controlPlane {
		machine.Labels[clusterv1.MachineControlPlaneLabel] = ""
	}
	c := testClient(testOwnerCluster(true), hvCluster, machine, ipamHyperVMachine(), sshSecret(), bootstrapSecret(validBootstrap()))
	if err := markClusterProvisioned(testContext(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMachineIPAMWaitsForClaimBeforeTouchingHost(t *testing.T) {
	c := ipamClient(t, false)
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	if calls := session.Calls(); len(calls) != 0 {
		t.Fatalf("host was called before the address was known: %v", calls)
	}
	assertReady(t, getMachine(t, c), infrav1.WaitingForIPAddressClaimReason)

	claim := getClaim(t, c, testMachine+"-0")
	if claim.Spec.ClusterName != testCluster || claim.Labels[clusterv1.ClusterNameLabel] != testCluster {
		t.Fatalf("claim cluster = %q label = %q", claim.Spec.ClusterName, claim.Labels[clusterv1.ClusterNameLabel])
	}
	if claim.Spec.PoolRef.Kind != "InClusterIPPool" || claim.Spec.PoolRef.Name != testPool || claim.Spec.PoolRef.APIGroup != "ipam.cluster.x-k8s.io" {
		t.Fatalf("pool ref = %+v", claim.Spec.PoolRef)
	}
	if len(claim.OwnerReferences) != 1 || claim.OwnerReferences[0].UID != "hvmachine-uid" || !ptr.Deref(claim.OwnerReferences[0].Controller, false) {
		t.Fatalf("owner refs = %+v", claim.OwnerReferences)
	}

	// A second pass must reuse the claim, not create another.
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	claims := &ipamv1.IPAddressClaimList{}
	if err := c.List(testContext(), claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 1 {
		t.Fatalf("claims = %d", len(claims.Items))
	}
}

func TestMachineIPAMWritesStaticNetworkConfig(t *testing.T) {
	c := ipamClient(t, true)
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	bindClaim(t, c, testMachine+"-0", ipamv1.IPAddressSpec{Address: "10.0.0.50", Prefix: ptr.To[int32](24), Gateway: "10.0.0.1"})
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	body := string(isoUpload(t, session))
	for _, want := range []string{"10.0.0.50/24", "via: 10.0.0.1", "dhcp4: false", "set-name: eth0", "lab.example", "kube-vip"} {
		if !strings.Contains(body, want) {
			t.Fatalf("iso missing %q", want)
		}
	}
	if strings.Contains(body, "dhcp4: true") {
		t.Fatal("iso still enables DHCP")
	}

	got := getMachine(t, c)
	assertReady(t, got, infrav1.ProvisionedReason)
	// Integration services reported 10.0.0.8. The IPAM address wins.
	if !hasAddress(got.Status.Addresses, clusterv1.MachineInternalIP, "10.0.0.50") || hasAddress(got.Status.Addresses, clusterv1.MachineInternalIP, "10.0.0.8") {
		t.Fatalf("addresses = %+v", got.Status.Addresses)
	}
}

func TestMachineIPAMRejectsAddressWithoutPrefix(t *testing.T) {
	c := ipamClient(t, false)
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	bindClaim(t, c, testMachine+"-0", ipamv1.IPAddressSpec{Address: "10.0.0.50"})
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	if _, ok := callNamed(session.Calls(), "ensure-vm"); ok {
		t.Fatal("vm was created with an unusable address")
	}
	assertReady(t, getMachine(t, c), infrav1.IPAddressInvalidReason)
}

func TestMachineIPAMRefusesForeignClaim(t *testing.T) {
	foreign := &ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testMachine + "-0",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrav1.GroupVersion.String(), Kind: "HyperVMachine", Name: "other", UID: "other-uid",
			}},
		},
	}
	c := ipamClient(t, false)
	if err := c.Create(testContext(), foreign); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	_, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest())
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("err = %v", err)
	}
	if len(session.Calls()) != 0 {
		t.Fatal("host was called")
	}
}

func TestMachineWithoutIPAMKeepsDHCP(t *testing.T) {
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportSSH, ""), testOwnerMachine(), testHyperVMachine(), sshSecret(), bootstrapSecret(validBootstrap()))
	if err := markClusterProvisioned(testContext(), c); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	if _, err := machineReconciler(c, session).Reconcile(testContext(), machineRequest()); err != nil {
		t.Fatal(err)
	}
	if body := string(isoUpload(t, session)); !strings.Contains(body, "dhcp4: true") {
		t.Fatal("dhcp network config missing")
	}
	claims := &ipamv1.IPAddressClaimList{}
	if err := c.List(testContext(), claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("claims = %d", len(claims.Items))
	}
}
