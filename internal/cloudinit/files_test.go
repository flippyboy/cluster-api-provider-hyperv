package cloudinit

import (
	"strings"
	"testing"
)

func TestAddWriteFileAppendsManifest(t *testing.T) {
	in := []byte(`#cloud-config
write_files:
  - path: /run/kubeadm/kubeadm.yaml
    content: |
      apiVersion: kubeadm.k8s.io/v1beta3
      kind: InitConfiguration
`)
	out, err := AddWriteFile(in, "/etc/kubernetes/manifests/kube-vip.yaml", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: kube-vip\n")
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "/run/kubeadm/kubeadm.yaml") || !strings.Contains(text, "kind: InitConfiguration") {
		t.Fatalf("kubeadm file was dropped: %s", text)
	}
	if !strings.Contains(text, "/etc/kubernetes/manifests/kube-vip.yaml") || !strings.Contains(text, "name: kube-vip") {
		t.Fatalf("manifest missing: %s", text)
	}
	again, err := AddWriteFile(out, "/etc/kubernetes/manifests/kube-vip.yaml", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: replaced\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(again), "/etc/kubernetes/manifests/kube-vip.yaml") != 1 || !strings.Contains(string(again), "name: replaced") {
		t.Fatalf("replace failed: %s", again)
	}
}

func TestNetworkConfigNamed(t *testing.T) {
	got, err := NetworkConfigNamed("00:15:5d:aa:bb:cc", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "00:15:5d:aa:bb:cc") || !strings.Contains(text, "set-name: eth0") || !strings.Contains(text, "dhcp4: true") {
		t.Fatalf("net = %s", text)
	}
	if _, err := NetworkConfigNamed("00:15:5d:aa:bb:cc", "eth0 bad"); err == nil {
		t.Fatal("expected invalid interface")
	}
}
