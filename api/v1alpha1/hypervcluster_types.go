package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HyperVClusterFinalizer is unused. HyperVCluster creates no host-side
	// object of its own. The constant is kept so callers can recognize the
	// provider finalizer namespace.
	HyperVClusterFinalizer = "hypervcluster.infrastructure.cluster.x-k8s.io"
)

// Transport is how the controller reaches the Hyper-V host.
// +kubebuilder:validation:Enum=SSH;WinRM
type Transport string

const (
	// TransportSSH runs PowerShell over OpenSSH.
	TransportSSH Transport = "SSH"
	// TransportWinRM runs PowerShell over WinRM with NTLM.
	TransportWinRM Transport = "WinRM"
)

// WinRMScheme selects the WinRM listener.
// +kubebuilder:validation:Enum=HTTP;HTTPS
type WinRMScheme string

const (
	// WinRMSchemeHTTP is the unencrypted listener, normally port 5985.
	// NTLM message encryption still seals the SOAP body.
	WinRMSchemeHTTP WinRMScheme = "HTTP"
	// WinRMSchemeHTTPS is the TLS listener, normally port 5986.
	WinRMSchemeHTTPS WinRMScheme = "HTTPS"
)

// HostSpec is the connection to one Hyper-V host.
type HostSpec struct {
	// address is the host name or IP of the Hyper-V host.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Address string `json:"address"`

	// port is the TCP port. When unset, SSH uses 22, WinRM HTTP uses 5985,
	// and WinRM HTTPS uses 5986.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// transport selects SSH or WinRM. Defaults to SSH.
	// +optional
	// +kubebuilder:default=SSH
	// +kubebuilder:validation:Enum=SSH;WinRM
	Transport Transport `json:"transport,omitempty"`

	// winRMScheme selects the WinRM listener when transport is WinRM.
	// Defaults to HTTPS.
	// +optional
	// +kubebuilder:default=HTTPS
	// +kubebuilder:validation:Enum=HTTP;HTTPS
	WinRMScheme WinRMScheme `json:"winRMScheme,omitempty"`

	// insecureSkipHostKeyVerification skips SSH host key checks.
	// +optional
	InsecureSkipHostKeyVerification bool `json:"insecureSkipHostKeyVerification,omitempty"`

	// insecureSkipTLSVerify skips TLS verification for WinRM HTTPS.
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// tlsServerName overrides the name checked against the WinRM server
	// certificate. Set this when address is an IP and the certificate is
	// issued to a DNS name.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	TLSServerName string `json:"tlsServerName,omitempty"`
}

// NormalizedTransport returns SSH when transport is empty.
func (h HostSpec) NormalizedTransport() Transport {
	if h.Transport == "" {
		return TransportSSH
	}
	return h.Transport
}

// NormalizedWinRMScheme returns HTTPS when the scheme is empty.
func (h HostSpec) NormalizedWinRMScheme() WinRMScheme {
	if h.WinRMScheme == "" {
		return WinRMSchemeHTTPS
	}
	return h.WinRMScheme
}

// EffectivePort returns the port the controller dials.
func (h HostSpec) EffectivePort() int32 {
	if h.Port != 0 {
		return h.Port
	}
	if h.NormalizedTransport() == TransportWinRM {
		if h.NormalizedWinRMScheme() == WinRMSchemeHTTP {
			return 5985
		}
		return 5986
	}
	return 22
}

// HyperVClusterSpec defines the desired state of HyperVCluster.
type HyperVClusterSpec struct {
	// host is the Hyper-V host that will run every machine in this cluster.
	// +required
	Host HostSpec `json:"host"`

	// credentialsSecretRef names a Secret in the same namespace.
	// SSH keys: username, ssh-privatekey, and ssh-known-hosts unless host
	// key verification is skipped.
	// WinRM keys: username and password. NTLM is the authentication method.
	// Optional WinRM key: ca-cert, a PEM bundle for the HTTPS listener.
	// +required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`

	// virtualSwitch is the name of an external virtual switch that already
	// exists on the host.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	VirtualSwitch string `json:"virtualSwitch"`

	// imagePath is the golden VHDX on the host. Differencing disks use it as
	// their parent, so it must stay immutable.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	ImagePath string `json:"imagePath"`

	// vmPath is the directory under which each VM folder is created. When
	// empty, the host default virtual machine path is used.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	VMPath string `json:"vmPath,omitempty"`

	// kubeVIP installs kube-vip on control plane machines. The virtual IP is
	// Cluster.spec.controlPlaneEndpoint. Leave this unset when that endpoint
	// is a DHCP reservation or another load balancer.
	// +optional
	KubeVIP KubeVIPSpec `json:"kubeVIP,omitempty,omitzero"`
}

// DefaultKubeVIPImage is the kube-vip image used when spec.kubeVIP.image is empty.
const DefaultKubeVIPImage = "ghcr.io/kube-vip/kube-vip:v1.3.0"

// KubeVIPSpec is the kube-vip static pod installed on control plane guests.
type KubeVIPSpec struct {
	// enabled adds the kube-vip static pod to control plane cloud-init.
	// Workers are left unchanged. The mode is ARP on the guest NIC.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// image overrides the kube-vip container image.
	// +optional
	// +kubebuilder:default="ghcr.io/kube-vip/kube-vip:v1.3.0"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Image string `json:"image,omitempty"`

	// interface is the guest interface that holds the virtual IP.
	// The NoCloud network-config renames the matched NIC to this name.
	// +optional
	// +kubebuilder:default=eth0
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9_-]{0,14}$`
	Interface string `json:"interface,omitempty"`
}

// EffectiveImage returns the kube-vip image, defaulting when unset.
func (k KubeVIPSpec) EffectiveImage() string {
	if k.Image == "" {
		return DefaultKubeVIPImage
	}
	return k.Image
}

// EffectiveInterface returns the guest interface, defaulting when unset.
func (k KubeVIPSpec) EffectiveInterface() string {
	if k.Interface == "" {
		return "eth0"
	}
	return k.Interface
}

// HyperVClusterInitializationStatus reports contract initialization.
// +kubebuilder:validation:MinProperties=1
type HyperVClusterInitializationStatus struct {
	// provisioned is true when machines may be created. It is set once.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// HyperVClusterStatus defines the observed state of HyperVCluster.
type HyperVClusterStatus struct {
	// conditions represent the observations of the cluster.
	// The Ready condition is mirrored onto Cluster InfrastructureReady.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// initialization reports contract provisioning state.
	// +optional
	Initialization HyperVClusterInitializationStatus `json:"initialization,omitempty,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hypervclusters,shortName=hvcl,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.spec.host.address`
// +kubebuilder:printcolumn:name="Transport",type=string,JSONPath=`.spec.host.transport`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyperVCluster is the Schema for hypervclusters API.
type HyperVCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HyperVClusterSpec   `json:"spec,omitempty"`
	Status HyperVClusterStatus `json:"status,omitempty"`
}

// GetConditions returns the status conditions.
func (c *HyperVCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the status conditions.
func (c *HyperVCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// HyperVClusterList contains a list of HyperVCluster.
type HyperVClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperVCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperVCluster{}, &HyperVClusterList{})
}
