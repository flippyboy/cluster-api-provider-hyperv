// Package kubevip renders the kube-vip static pod placed on control plane guests.
package kubevip

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// BusyboxImage copies the kubeadm kubeconfig into the static pod.
	// The kube-vip image is scratch and has no shell.
	BusyboxImage = "busybox:1.37.0"
)

var (
	ifaceName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,14}$`)
	dnsName   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// Settings is the control plane VIP advertised with ARP.
type Settings struct {
	Address   string
	Port      int
	Interface string
	Image     string
}

// Manifest returns a static pod that advertises the control plane endpoint.
// Service load balancer support is off. The pod waits for super-admin.conf on
// the first control plane node and admin.conf on nodes that join later.
func Manifest(s Settings) ([]byte, error) {
	if err := validate(s); err != nil {
		return nil, err
	}
	pod := staticPod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata:   podMeta{Name: "kube-vip", Namespace: "kube-system"},
		Spec: podSpec{
			HostNetwork: true,
			HostAliases: []hostAlias{{
				IP:        "127.0.0.1",
				Hostnames: []string{"kubernetes"},
			}},
			InitContainers: []container{{
				Name:            "kubeconfig",
				Image:           BusyboxImage,
				ImagePullPolicy: "IfNotPresent",
				Command:         []string{"/bin/sh", "-ec"},
				Args:            []string{kubeconfigScript},
				VolumeMounts: []volumeMount{
					{Name: "host-kubernetes", MountPath: "/host", ReadOnly: true},
					{Name: "kubeconfig", MountPath: "/out"},
				},
			}},
			Containers: []container{{
				Name:            "kube-vip",
				Image:           s.Image,
				ImagePullPolicy: "IfNotPresent",
				Args:            []string{"manager"},
				Env:             env(s),
				SecurityContext: &securityContext{Capabilities: capabilities{Add: []string{"NET_ADMIN", "NET_RAW", "SYS_TIME"}}},
				VolumeMounts: []volumeMount{{
					Name:      "kubeconfig",
					MountPath: "/etc/kubernetes/admin.conf",
					SubPath:   "admin.conf",
				}},
			}},
			Volumes: []volume{
				{Name: "kubeconfig", EmptyDir: &emptyDir{}},
				{Name: "host-kubernetes", HostPath: &hostPath{Path: "/etc/kubernetes", Type: "DirectoryOrCreate"}},
			},
		},
	}
	out, err := yaml.Marshal(pod)
	if err != nil {
		return nil, fmt.Errorf("encode kube-vip manifest: %w", err)
	}
	return out, nil
}

func validate(s Settings) error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("kube-vip port %d is invalid", s.Port)
	}
	if !ifaceName.MatchString(s.Interface) {
		return fmt.Errorf("kube-vip interface %q is invalid", s.Interface)
	}
	if strings.TrimSpace(s.Image) == "" || strings.ContainsAny(s.Image, " \t\r\n") {
		return fmt.Errorf("kube-vip image %q is invalid", s.Image)
	}
	if !validAddress(s.Address) {
		return fmt.Errorf("kube-vip address %q is invalid", s.Address)
	}
	return nil
}

func validAddress(address string) bool {
	if address == "" || strings.ContainsAny(address, " \t\r\n'\"\\/") {
		return false
	}
	if ip := net.ParseIP(address); ip != nil {
		return true
	}
	return dnsName.MatchString(address)
}

func env(s Settings) []envVar {
	subnet := "32"
	if ip := net.ParseIP(s.Address); ip != nil && ip.To4() == nil {
		subnet = "128"
	}
	return []envVar{
		{Name: "vip_arp", Value: "true"},
		{Name: "bgp_enable", Value: "false"},
		{Name: "port", Value: strconv.Itoa(s.Port)},
		{Name: "vip_interface", Value: s.Interface},
		{Name: "vip_subnet", Value: subnet},
		{Name: "address", Value: s.Address},
		{Name: "dns_mode", Value: "ipv4"},
		{Name: "cp_enable", Value: "true"},
		{Name: "cp_detect", Value: "true"},
		{Name: "cp_namespace", Value: "kube-system"},
		{Name: "svc_enable", Value: "false"},
		{Name: "vip_leaderelection", Value: "true"},
		{Name: "vip_leaseduration", Value: "5"},
		{Name: "vip_renewdeadline", Value: "3"},
		{Name: "vip_retryperiod", Value: "1"},
		{Name: "k8s_config_file", Value: "/etc/kubernetes/admin.conf"},
	}
}

const kubeconfigScript = `while true; do
  if [ -s /host/super-admin.conf ]; then
    cp /host/super-admin.conf /out/admin.conf
    exit 0
  fi
  if [ -s /host/admin.conf ]; then
    cp /host/admin.conf /out/admin.conf
    exit 0
  fi
  sleep 1
done
`

type staticPod struct {
	APIVersion string  `yaml:"apiVersion"`
	Kind       string  `yaml:"kind"`
	Metadata   podMeta `yaml:"metadata"`
	Spec       podSpec `yaml:"spec"`
}

type podMeta struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type podSpec struct {
	HostNetwork    bool        `yaml:"hostNetwork"`
	HostAliases    []hostAlias `yaml:"hostAliases"`
	InitContainers []container `yaml:"initContainers"`
	Containers     []container `yaml:"containers"`
	Volumes        []volume    `yaml:"volumes"`
}

type hostAlias struct {
	IP        string   `yaml:"ip"`
	Hostnames []string `yaml:"hostnames"`
}

type container struct {
	Name            string           `yaml:"name"`
	Image           string           `yaml:"image"`
	ImagePullPolicy string           `yaml:"imagePullPolicy"`
	Command         []string         `yaml:"command,omitempty"`
	Args            []string         `yaml:"args,omitempty"`
	Env             []envVar         `yaml:"env,omitempty"`
	SecurityContext *securityContext `yaml:"securityContext,omitempty"`
	VolumeMounts    []volumeMount    `yaml:"volumeMounts"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type securityContext struct {
	Capabilities capabilities `yaml:"capabilities"`
}

type capabilities struct {
	Add []string `yaml:"add"`
}

type volumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
	SubPath   string `yaml:"subPath,omitempty"`
}

type volume struct {
	Name     string    `yaml:"name"`
	EmptyDir *emptyDir `yaml:"emptyDir,omitempty"`
	HostPath *hostPath `yaml:"hostPath,omitempty"`
}

type emptyDir struct{}

type hostPath struct {
	Path string `yaml:"path"`
	Type string `yaml:"type"`
}
