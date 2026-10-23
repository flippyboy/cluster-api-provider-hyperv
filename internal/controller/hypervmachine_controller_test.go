package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

func TestMachineWaitsForIPv4ThenProvisions(t *testing.T) {
	ctx := testContext()
	hvCluster := testHyperVCluster(infrav1.TransportSSH, "")
	hvMachine := testHyperVMachine()
	c := testClient(testOwnerCluster(true), hvCluster, testOwnerMachine(), hvMachine, sshSecret(), bootstrapSecret(validBootstrap()))
	if err := markClusterProvisioned(ctx, c); err != nil {
		t.Fatal(err)
	}
	withIP := false
	session := &hyperv.FakeSession{Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
		return hostResponder(withIP)(name, params)
	}}
	r := machineReconciler(c, session)

	if _, err := r.Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	got := getMachine(t, c)
	if got.Status.Initialization.Provisioned != nil || got.Spec.ProviderID != "" {
		t.Fatalf("provisioned early: %#v", got.Status.Initialization)
	}
	assertReady(t, got, infrav1.WaitingForIPReason)
	if !containsString(got.Finalizers, infrav1.HyperVMachineFinalizer) {
		t.Fatal("finalizer was not added")
	}
	if len(session.Uploads()) == 0 {
		t.Fatal("ssh upload was not recorded")
	}

	withIP = true
	if _, err := r.Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	got = getMachine(t, c)
	if !ptr.Deref(got.Status.Initialization.Provisioned, false) {
		t.Fatal("machine was not provisioned")
	}
	if got.Spec.ProviderID != "hyperv://"+testVMID {
		t.Fatalf("providerID = %s", got.Spec.ProviderID)
	}
	assertReady(t, got, infrav1.ProvisionedReason)
	if !hasAddress(got.Status.Addresses, clusterv1.MachineInternalIP, "10.0.0.8") {
		t.Fatalf("addresses = %#v", got.Status.Addresses)
	}
	if !hasAddress(got.Status.Addresses, clusterv1.MachineHostName, testMachine) {
		t.Fatalf("hostname missing: %#v", got.Status.Addresses)
	}
	if got.Status.MACAddress != "00:15:5d:aa:bb:cc" {
		t.Fatalf("mac = %s", got.Status.MACAddress)
	}
}

func TestMachineRecordsCloneMode(t *testing.T) {
	for _, mode := range []infrav1.DiskCloneMode{"", infrav1.DiskCloneModeCopy} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := testContext()
			hvMachine := testHyperVMachine()
			hvMachine.Spec.Disk.CloneMode = mode
			c := testClient(
				testOwnerCluster(true),
				testHyperVCluster(infrav1.TransportSSH, ""),
				testOwnerMachine(),
				hvMachine,
				sshSecret(),
				bootstrapSecret(validBootstrap()),
			)
			if err := markClusterProvisioned(ctx, c); err != nil {
				t.Fatal(err)
			}
			session := &hyperv.FakeSession{Respond: hostResponder(true)}
			if _, err := machineReconciler(c, session).Reconcile(ctx, machineRequest()); err != nil {
				t.Fatal(err)
			}
			call, ok := callNamed(session.Calls(), "ensure-disk")
			if !ok {
				t.Fatal("ensure-disk was not called")
			}
			var params struct {
				CloneMode string `json:"cloneMode"`
				ImagePath string `json:"imagePath"`
			}
			if err := json.Unmarshal(call.Params, &params); err != nil {
				t.Fatal(err)
			}
			want := string(infrav1.DiskCloneModeDifferencing)
			if mode == infrav1.DiskCloneModeCopy {
				want = string(infrav1.DiskCloneModeCopy)
			}
			if params.CloneMode != want || params.ImagePath != `C:\images\golden.vhdx` {
				t.Fatalf("disk params = %#v", params)
			}
		})
	}
}

func TestMachineWinRMUploadIsChunked(t *testing.T) {
	ctx := testContext()
	c := testClient(
		testOwnerCluster(true),
		testHyperVCluster(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTPS),
		testOwnerMachine(),
		testHyperVMachine(),
		winrmSecret("s3cret"),
		bootstrapSecret(validBootstrap()),
	)
	if err := markClusterProvisioned(ctx, c); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(false)}
	var dialed hyperv.HostConfig
	r := &HyperVMachineReconciler{
		Client: c,
		Dial: func(_ context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = cfg
			return hyperv.NewClient(session, cfg.Transport), nil
		},
	}
	if _, err := r.Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	if dialed.Transport != hyperv.TransportWinRM || !dialed.UseHTTPS || dialed.Password != "s3cret" {
		t.Fatalf("dial cfg = %#v", dialed)
	}
	if len(session.Uploads()) != 0 {
		t.Fatal("winrm used the sftp upload path")
	}
	call, ok := callNamed(session.Calls(), "write-chunk")
	if !ok {
		t.Fatal("write-chunk was not called")
	}
	var params struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Data   string `json:"data"`
	}
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Offset != 0 || params.Data == "" || !strings.HasSuffix(params.Path, `\cidata.iso`) {
		t.Fatalf("chunk = %#v", params)
	}
	assertReady(t, getMachine(t, c), infrav1.WaitingForIPReason)
}

func TestMachineKubeVIPOnlyOnControlPlane(t *testing.T) {
	t.Run("control plane", func(t *testing.T) {
		session, _ := reconcileKubeVIP(t, true, "eth0")
		if _, ok := callNamed(session.Calls(), "start"); !ok {
			t.Fatal("control plane vm was not started")
		}
		if len(session.Uploads()) != 1 {
			t.Fatalf("uploads = %d", len(session.Uploads()))
		}
		body := string(session.Uploads()[0].Data)
		for _, want := range []string{"kube-vip", "10.0.0.10", "set-name: eth0", "svc_enable", "super-admin.conf"} {
			if !strings.Contains(body, want) {
				t.Fatalf("iso missing %q", want)
			}
		}
	})
	t.Run("worker", func(t *testing.T) {
		session, _ := reconcileKubeVIP(t, false, "eth0")
		body := string(session.Uploads()[0].Data)
		if strings.Contains(body, "kube-vip") || strings.Contains(body, "set-name:") {
			t.Fatal("worker cloud-init included kube-vip")
		}
	})
	t.Run("bad interface", func(t *testing.T) {
		session, c := reconcileKubeVIP(t, true, "eth0 bad")
		if _, ok := callNamed(session.Calls(), "start"); ok {
			t.Fatal("vm started with an invalid kube-vip interface")
		}
		assertReady(t, getMachine(t, c), infrav1.KubeVIPConfigInvalidReason)
	})
}

func reconcileKubeVIP(t *testing.T, controlPlane bool, iface string) (*hyperv.FakeSession, client.Client) {
	t.Helper()
	ctx := testContext()
	hvCluster := testHyperVCluster(infrav1.TransportSSH, "")
	hvCluster.Spec.KubeVIP.Enabled = true
	hvCluster.Spec.KubeVIP.Interface = iface
	machine := testOwnerMachine()
	if controlPlane {
		machine.Labels[clusterv1.MachineControlPlaneLabel] = ""
	}
	c := testClient(testOwnerCluster(true), hvCluster, machine, testHyperVMachine(), sshSecret(), bootstrapSecret(validBootstrap()))
	if err := markClusterProvisioned(ctx, c); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(false)}
	if _, err := machineReconciler(c, session).Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	return session, c
}

func TestMachineInvalidBootstrapDoesNotStart(t *testing.T) {
	ctx := testContext()
	c := testClient(
		testOwnerCluster(true),
		testHyperVCluster(infrav1.TransportSSH, ""),
		testOwnerMachine(),
		testHyperVMachine(),
		sshSecret(),
		bootstrapSecret("#cloud-config\npackage_update: true\n"),
	)
	if err := markClusterProvisioned(ctx, c); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(true)}
	if _, err := machineReconciler(c, session).Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	if _, ok := callNamed(session.Calls(), "start"); ok {
		t.Fatal("vm was started with invalid bootstrap data")
	}
	got := getMachine(t, c)
	assertReady(t, got, infrav1.BootstrapDataInvalidReason)
	if got.Spec.ProviderID != "" || got.Status.Initialization.Provisioned != nil {
		t.Fatal("invalid bootstrap set provider id or provisioned")
	}
}

func TestMachineDeleteRemovesVMAndFinalizer(t *testing.T) {
	ctx := testContext()
	hvMachine := testHyperVMachine()
	hvMachine.Finalizers = []string{infrav1.HyperVMachineFinalizer}
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportSSH, ""), testOwnerMachine(), hvMachine, sshSecret())
	if err := setMachineStatus(ctx, c, func(machine *infrav1.HyperVMachine) {
		machine.Status.VMName = "capi-demo-demo-cp-0"
		machine.Status.VMID = testVMID
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, hvMachine); err != nil {
		t.Fatal(err)
	}
	session := &hyperv.FakeSession{Respond: hostResponder(false)}
	if _, err := machineReconciler(c, session).Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	call, ok := callNamed(session.Calls(), "delete")
	if !ok {
		t.Fatal("delete was not called")
	}
	var params struct {
		VMName    string `json:"vmName"`
		ImagePath string `json:"imagePath"`
	}
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.VMName != "capi-demo-demo-cp-0" || params.ImagePath != `C:\images\golden.vhdx` {
		t.Fatalf("delete params = %#v", params)
	}
	assertMachineGone(t, c)
}

func TestMachineDeleteWithoutVMSkipsDial(t *testing.T) {
	ctx := testContext()
	hvMachine := testHyperVMachine()
	hvMachine.Finalizers = []string{infrav1.HyperVMachineFinalizer}
	c := testClient(hvMachine)
	if err := c.Delete(ctx, hvMachine); err != nil {
		t.Fatal(err)
	}
	dialed := false
	r := &HyperVMachineReconciler{
		Client: c,
		Dial: func(context.Context, hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = true
			return nil, errDial
		},
	}
	if _, err := r.Reconcile(ctx, machineRequest()); err != nil {
		t.Fatal(err)
	}
	if dialed {
		t.Fatal("dialed for a machine that never created a vm")
	}
	assertMachineGone(t, c)
}

func TestMachineDeleteKeepsFinalizerWhenPasswordMissing(t *testing.T) {
	ctx := testContext()
	hvMachine := testHyperVMachine()
	hvMachine.Finalizers = []string{infrav1.HyperVMachineFinalizer}
	c := testClient(
		testOwnerCluster(true),
		testHyperVCluster(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTP),
		testOwnerMachine(),
		hvMachine,
		winrmSecret(""),
	)
	if err := setMachineStatus(ctx, c, func(machine *infrav1.HyperVMachine) {
		machine.Status.VMName = "capi-demo-demo-cp-0"
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, hvMachine); err != nil {
		t.Fatal(err)
	}
	dialed := false
	r := &HyperVMachineReconciler{
		Client: c,
		Dial: func(context.Context, hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = true
			return nil, errDial
		},
	}
	result, err := r.Reconcile(ctx, machineRequest())
	if err != nil {
		t.Fatal(err)
	}
	if dialed || result.RequeueAfter != requeueAfter {
		t.Fatalf("dialed=%v requeue=%s", dialed, result.RequeueAfter)
	}
	got := getMachine(t, c)
	if !containsString(got.Finalizers, infrav1.HyperVMachineFinalizer) {
		t.Fatal("finalizer was removed while credentials were missing")
	}
	assertReady(t, got, infrav1.CredentialsInvalidReason)
}

func machineRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testMachine}}
}

func machineReconciler(c client.Client, session *hyperv.FakeSession) *HyperVMachineReconciler {
	return &HyperVMachineReconciler{
		Client: c,
		Dial: func(_ context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
			return hyperv.NewClient(session, cfg.Transport), nil
		},
	}
}

func markClusterProvisioned(ctx context.Context, c client.Client) error {
	hv := &infrav1.HyperVCluster{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testCluster}, hv); err != nil {
		return err
	}
	hv.Status.Initialization.Provisioned = ptr.To(true)
	return c.Status().Update(ctx, hv)
}

func setMachineStatus(ctx context.Context, c client.Client, update func(*infrav1.HyperVMachine)) error {
	got := &infrav1.HyperVMachine{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testMachine}, got); err != nil {
		return err
	}
	update(got)
	return c.Status().Update(ctx, got)
}

func getMachine(t *testing.T, c client.Client) *infrav1.HyperVMachine {
	t.Helper()
	got := &infrav1.HyperVMachine{}
	if err := c.Get(testContext(), types.NamespacedName{Namespace: testNamespace, Name: testMachine}, got); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertMachineGone(t *testing.T, c client.Client) {
	t.Helper()
	got := &infrav1.HyperVMachine{}
	err := c.Get(testContext(), types.NamespacedName{Namespace: testNamespace, Name: testMachine}, got)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if containsString(got.Finalizers, infrav1.HyperVMachineFinalizer) {
		t.Fatal("finalizer was kept")
	}
}

func hasAddress(addrs clusterv1.MachineAddresses, typ clusterv1.MachineAddressType, value string) bool {
	for _, addr := range addrs {
		if addr.Type == typ && addr.Address == value {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
