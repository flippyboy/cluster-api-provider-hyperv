package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
)

var errDial = errors.New("dial")

func TestClusterWaitsForEndpointWithoutDialing(t *testing.T) {
	ctx := testContext()
	c := testClient(testOwnerCluster(false), testHyperVCluster(infrav1.TransportSSH, ""), sshSecret())
	dialed := false
	r := &HyperVClusterReconciler{
		Client: c,
		Dial: func(context.Context, hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = true
			return nil, errDial
		},
	}
	result, err := r.Reconcile(ctx, clusterRequest())
	if err != nil {
		t.Fatal(err)
	}
	if dialed {
		t.Fatal("dialed before the control plane endpoint was set")
	}
	if result.RequeueAfter != requeueAfter {
		t.Fatalf("requeue = %s", result.RequeueAfter)
	}
	got := getCluster(t, c)
	if got.Status.Initialization.Provisioned != nil {
		t.Fatal("provisioned was set without an endpoint")
	}
	assertReady(t, got, infrav1.WaitingForControlPlaneEndpointReason)
}

func TestClusterProvisionsAfterProbe(t *testing.T) {
	ctx := testContext()
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTP), winrmSecret("s3cret"))
	session := &hyperv.FakeSession{Respond: hostResponder(false)}
	var dialed hyperv.HostConfig
	r := &HyperVClusterReconciler{
		Client: c,
		Dial: func(_ context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = cfg
			return hyperv.NewClient(session, cfg.Transport), nil
		},
	}
	if _, err := r.Reconcile(ctx, clusterRequest()); err != nil {
		t.Fatal(err)
	}
	if dialed.Transport != hyperv.TransportWinRM || dialed.UseHTTPS || dialed.Username != `HOST\Administrator` || dialed.Password != "s3cret" {
		t.Fatalf("dial cfg = %#v", dialed)
	}
	if _, ok := callNamed(session.Calls(), "probe"); !ok {
		t.Fatal("probe was not called")
	}
	got := getCluster(t, c)
	if !ptr.Deref(got.Status.Initialization.Provisioned, false) {
		t.Fatal("cluster was not provisioned")
	}
	assertReady(t, got, infrav1.ProvisionedReason)
}

func TestClusterSwitchMissingStaysUnprovisioned(t *testing.T) {
	ctx := testContext()
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportSSH, ""), sshSecret())
	session := &hyperv.FakeSession{Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
		if name == "probe" {
			return []byte(`{"hypervModule":true,"switchFound":false,"imageFound":true,"imageIsVhdx":true}`), nil, 0, nil
		}
		return hostResponder(false)(name, params)
	}}
	if _, err := clusterReconciler(c, session).Reconcile(ctx, clusterRequest()); err != nil {
		t.Fatal(err)
	}
	got := getCluster(t, c)
	if got.Status.Initialization.Provisioned != nil {
		t.Fatal("provisioned despite missing switch")
	}
	assertReady(t, got, infrav1.VirtualSwitchNotFoundReason)
}

func TestClusterFailureAfterProvisionKeepsProvisioned(t *testing.T) {
	ctx := testContext()
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportSSH, ""), sshSecret())
	probes := 0
	session := &hyperv.FakeSession{Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
		if name == "probe" {
			probes++
			if probes > 1 {
				return []byte(`{"error":"host down"}`), []byte("host down"), 1, nil
			}
		}
		return hostResponder(false)(name, params)
	}}
	r := clusterReconciler(c, session)
	if _, err := r.Reconcile(ctx, clusterRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, clusterRequest()); err != nil {
		t.Fatal(err)
	}
	got := getCluster(t, c)
	if !ptr.Deref(got.Status.Initialization.Provisioned, false) {
		t.Fatal("provisioned was cleared")
	}
	assertReady(t, got, infrav1.HostCommandFailedReason)
}

func TestClusterMissingSecret(t *testing.T) {
	ctx := testContext()
	c := testClient(testOwnerCluster(true), testHyperVCluster(infrav1.TransportSSH, ""))
	dialed := false
	r := &HyperVClusterReconciler{
		Client: c,
		Dial: func(context.Context, hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = true
			return nil, errDial
		},
	}
	result, err := r.Reconcile(ctx, clusterRequest())
	if err != nil {
		t.Fatal(err)
	}
	if dialed || result.RequeueAfter != requeueAfter {
		t.Fatalf("dialed=%v requeue=%s", dialed, result.RequeueAfter)
	}
	assertReady(t, getCluster(t, c), infrav1.CredentialsInvalidReason)
}

func TestClusterPausedDoesNotDial(t *testing.T) {
	ctx := testContext()
	owner := testOwnerCluster(true)
	owner.Spec.Paused = ptr.To(true)
	c := testClient(owner, testHyperVCluster(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTPS), winrmSecret("s3cret"))
	dialed := false
	r := &HyperVClusterReconciler{
		Client: c,
		Dial: func(context.Context, hyperv.HostConfig) (*hyperv.Client, error) {
			dialed = true
			return nil, errDial
		},
	}
	result, err := r.Reconcile(ctx, clusterRequest())
	if err != nil {
		t.Fatal(err)
	}
	if dialed || result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("dialed=%v result=%+v", dialed, result)
	}
	got := getCluster(t, c)
	paused := conditions.Get(got, clusterv1.PausedCondition)
	if paused == nil || paused.Status != metav1.ConditionTrue || paused.Reason != infrav1.PausedReason {
		t.Fatalf("paused = %#v", paused)
	}
}

func clusterRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testCluster}}
}

func clusterReconciler(c client.Client, session *hyperv.FakeSession) *HyperVClusterReconciler {
	return &HyperVClusterReconciler{
		Client: c,
		Dial: func(_ context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
			return hyperv.NewClient(session, cfg.Transport), nil
		},
	}
}

func getCluster(t *testing.T, c client.Client) *infrav1.HyperVCluster {
	t.Helper()
	got := &infrav1.HyperVCluster{}
	if err := c.Get(testContext(), types.NamespacedName{Namespace: testNamespace, Name: testCluster}, got); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertReady(t *testing.T, obj conditions.Getter, reason string) {
	t.Helper()
	cond := readyCondition(obj)
	if cond == nil || cond.Reason != reason {
		t.Fatalf("ready = %#v, want reason %s", cond, reason)
	}
}
