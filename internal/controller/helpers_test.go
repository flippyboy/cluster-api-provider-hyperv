package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
)

const (
	testNamespace = "default"
	testCluster   = "demo"
	testMachine   = "demo-cp-0"
	testVMID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func testContext() context.Context {
	return logr.NewContext(context.Background(), logr.Discard())
}

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func testClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithStatusSubresource(&infrav1.HyperVCluster{}, &infrav1.HyperVMachine{}).
		WithObjects(objs...).
		Build()
}

func sshSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "creds"},
		Data: map[string][]byte{
			"username":        []byte("Administrator"),
			"ssh-privatekey":  []byte("test-key"),
			"ssh-known-hosts": []byte("hyperv.example ssh-ed25519 AAAA"),
		},
	}
}

func winrmSecret(password string) *corev1.Secret {
	data := map[string][]byte{"username": []byte(`HOST\Administrator`)}
	if password != "" {
		data["password"] = []byte(password)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "creds"},
		Data:       data,
	}
}

func testOwnerCluster(endpoint bool) *clusterv1.Cluster {
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testCluster,
			UID:       types.UID("cluster-uid"),
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infrav1.GroupVersion.Group,
				Kind:     "HyperVCluster",
				Name:     testCluster,
			},
		},
	}
	if endpoint {
		cluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "10.0.0.10", Port: 6443}
	}
	return cluster
}

func testHyperVCluster(transport infrav1.Transport, scheme infrav1.WinRMScheme) *infrav1.HyperVCluster {
	cluster := testOwnerCluster(true)
	return &infrav1.HyperVCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testCluster,
			UID:       types.UID("hvcluster-uid"),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
				Controller: ptr.To(true),
			}},
		},
		Spec: testClusterSpec(transport, scheme),
	}
}

func testOwnerMachine() *clusterv1.Machine {
	return &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testMachine,
			UID:       types.UID("machine-uid"),
			Labels:    map[string]string{clusterv1.ClusterNameLabel: testCluster},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster,
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: ptr.To(testMachine + "-bootstrap"),
			},
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: infrav1.GroupVersion.Group,
				Kind:     "HyperVMachine",
				Name:     testMachine,
			},
		},
	}
}

func testHyperVMachine() *infrav1.HyperVMachine {
	machine := testOwnerMachine()
	return &infrav1.HyperVMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testMachine,
			UID:       types.UID("hvmachine-uid"),
			Labels:    map[string]string{clusterv1.ClusterNameLabel: testCluster},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       "Machine",
				Name:       machine.Name,
				UID:        machine.UID,
				Controller: ptr.To(true),
			}},
		},
	}
}

func bootstrapSecret(value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testMachine + "-bootstrap"},
		Data:       map[string][]byte{"value": []byte(value)},
	}
}

func validBootstrap() string {
	return `#cloud-config
write_files:
  - path: /run/kubeadm/kubeadm.yaml
    content: |
      apiVersion: kubeadm.k8s.io/v1beta3
      kind: InitConfiguration
      nodeRegistration:
        kubeletExtraArgs:
          cgroup-driver: systemd
`
}

func hostResponder(withIP bool) hyperv.Responder {
	return func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
		switch name {
		case "probe":
			return []byte(`{"hypervModule":true,"switchFound":true,"imageFound":true,"imageIsVhdx":true,"virtualMachinePath":"C:\\VMs"}`), nil, 0, nil
		case "ensure-vm":
			var in struct {
				VMName string `json:"vmName"`
			}
			_ = json.Unmarshal(params, &in)
			if in.VMName == "" {
				in.VMName = "vm"
			}
			body := fmt.Sprintf(`{"vmID":%q,"vmName":%q,"macAddress":"00155DAABBCC","vmPath":"C:\\VMs\\%s","state":"Off"}`, testVMID, in.VMName, in.VMName)
			return []byte(body), nil, 0, nil
		case "ensure-disk":
			return []byte(`{"diskPath":"C:\\VMs\\vm\\disk.vhdx","cloneMode":"Differencing"}`), nil, 0, nil
		case "ensure-iso":
			return []byte(`{"isoPath":"C:\\VMs\\vm\\cidata.iso","needsRestart":false}`), nil, 0, nil
		case "start":
			return []byte(`{"state":"Running"}`), nil, 0, nil
		case "stop":
			return []byte(`{"state":"Off"}`), nil, 0, nil
		case "inspect":
			ips := "[]"
			if withIP {
				ips = `["10.0.0.8","fe80::1","169.254.1.1","127.0.0.1"]`
			}
			return []byte(fmt.Sprintf(`{"vmID":%q,"state":"Running","macAddress":"00155DAABBCC","ipAddresses":%s}`, testVMID, ips)), nil, 0, nil
		case "delete", "write-chunk":
			return []byte(`{}`), nil, 0, nil
		default:
			return []byte(`{}`), nil, 0, nil
		}
	}
}

func readyCondition(obj conditions.Getter) *metav1.Condition {
	return conditions.Get(obj, clusterv1.ReadyCondition)
}

func callNamed(calls []hyperv.Call, name string) (hyperv.Call, bool) {
	for _, call := range calls {
		if call.Name == name {
			return call, true
		}
	}
	return hyperv.Call{}, false
}
