package kubevip

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManifestARPControlPlane(t *testing.T) {
	raw, err := Manifest(Settings{
		Address:   "10.0.0.10",
		Port:      6443,
		Interface: "eth0",
		Image:     "ghcr.io/kube-vip/kube-vip:v1.3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	var pod struct {
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			HostNetwork    bool `yaml:"hostNetwork"`
			InitContainers []struct {
				Image string   `yaml:"image"`
				Args  []string `yaml:"args"`
			} `yaml:"initContainers"`
			Containers []struct {
				Image string `yaml:"image"`
				Env   []struct {
					Name  string `yaml:"name"`
					Value string `yaml:"value"`
				} `yaml:"env"`
			} `yaml:"containers"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &pod); err != nil {
		t.Fatal(err)
	}
	if pod.Metadata.Name != "kube-vip" || pod.Metadata.Namespace != "kube-system" || !pod.Spec.HostNetwork {
		t.Fatalf("pod identity = %#v", pod.Metadata)
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Image != BusyboxImage {
		t.Fatalf("init = %#v", pod.Spec.InitContainers)
	}
	script := strings.Join(pod.Spec.InitContainers[0].Args, "\n")
	if !strings.Contains(script, "super-admin.conf") || !strings.Contains(script, "admin.conf") {
		t.Fatalf("kubeconfig script = %s", script)
	}
	env := map[string]string{}
	for _, item := range pod.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	if pod.Spec.Containers[0].Image != "ghcr.io/kube-vip/kube-vip:v1.3.0" {
		t.Fatal(pod.Spec.Containers[0].Image)
	}
	if env["vip_arp"] != "true" || env["cp_enable"] != "true" || env["svc_enable"] != "false" || env["bgp_enable"] != "false" {
		t.Fatalf("env = %#v", env)
	}
	if env["address"] != "10.0.0.10" || env["port"] != "6443" || env["vip_interface"] != "eth0" || env["vip_subnet"] != "32" {
		t.Fatalf("vip env = %#v", env)
	}
}

func TestManifestRejectsBadInput(t *testing.T) {
	_, err := Manifest(Settings{Address: "10.0.0.10", Port: 6443, Interface: "eth0 bad", Image: "ghcr.io/kube-vip/kube-vip:v1.3.0"})
	if err == nil {
		t.Fatal("expected invalid interface")
	}
	_, err = Manifest(Settings{Address: "", Port: 6443, Interface: "eth0", Image: "ghcr.io/kube-vip/kube-vip:v1.3.0"})
	if err == nil {
		t.Fatal("expected invalid address")
	}
}

func TestManifestIPv6Subnet(t *testing.T) {
	raw, err := Manifest(Settings{Address: "2001:db8::10", Port: 6443, Interface: "eth0", Image: "example/kube-vip:v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `value: "128"`) && !strings.Contains(string(raw), "value: \"128\"") && !strings.Contains(string(raw), "vip_subnet") {
		t.Fatalf("manifest = %s", raw)
	}
	if !strings.Contains(string(raw), "128") {
		t.Fatalf("missing ipv6 prefix: %s", raw)
	}
}
