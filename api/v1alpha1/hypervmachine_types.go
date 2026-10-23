package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	// HyperVMachineFinalizer is placed on HyperVMachine while a VM may exist.
	HyperVMachineFinalizer = "hypervmachine.infrastructure.cluster.x-k8s.io"
)

// DiskCloneMode selects how the system disk is created from the golden VHDX.
// +kubebuilder:validation:Enum=Differencing;Copy
type DiskCloneMode string

const (
	// DiskCloneModeDifferencing creates a child VHDX whose parent is the golden image.
	DiskCloneModeDifferencing DiskCloneMode = "Differencing"
	// DiskCloneModeCopy copies the golden VHDX.
	DiskCloneModeCopy DiskCloneMode = "Copy"
)

// DiskSpec is the system disk for one VM.
type DiskSpec struct {
	// imagePath overrides HyperVCluster.spec.imagePath for this machine.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	ImagePath string `json:"imagePath,omitempty"`

	// cloneMode defaults to Differencing.
	// +optional
	// +kubebuilder:default=Differencing
	// +kubebuilder:validation:Enum=Differencing;Copy
	CloneMode DiskCloneMode `json:"cloneMode,omitempty"`

	// sizeGiB grows the disk with Resize-VHD when it is larger than the image.
	// Zero leaves the cloned size unchanged.
	// +optional
	// +kubebuilder:validation:Minimum=0
	SizeGiB int32 `json:"sizeGiB,omitempty"`
}

// NormalizedCloneMode returns Differencing when clone mode is empty.
func (d DiskSpec) NormalizedCloneMode() DiskCloneMode {
	if d.CloneMode == "" {
		return DiskCloneModeDifferencing
	}
	return d.CloneMode
}

// HyperVMachineSpec defines the desired state of HyperVMachine.
type HyperVMachineSpec struct {
	// providerID is the Node provider ID, hyperv://<vm-guid>. The controller sets it.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	ProviderID string `json:"providerID,omitempty"`

	// vCPUs is the number of virtual processors. Defaults to 2.
	// +optional
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=240
	VCPUs int32 `json:"vCPUs,omitempty"`

	// memoryMiB is static memory in MiB. Dynamic Memory is not used. Defaults to 2048.
	// +optional
	// +kubebuilder:default=2048
	// +kubebuilder:validation:Minimum=512
	MemoryMiB int32 `json:"memoryMiB,omitempty"`

	// disk is the system disk. An empty clone mode means Differencing.
	// +optional
	Disk DiskSpec `json:"disk,omitempty"`

	// virtualSwitch overrides HyperVCluster.spec.virtualSwitch for this machine.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	VirtualSwitch string `json:"virtualSwitch,omitempty"`
}

// EffectiveVCPUs returns the CPU count, defaulting when unset.
func (s HyperVMachineSpec) EffectiveVCPUs() int32 {
	if s.VCPUs == 0 {
		return 2
	}
	return s.VCPUs
}

// EffectiveMemoryMiB returns the memory size, defaulting when unset.
func (s HyperVMachineSpec) EffectiveMemoryMiB() int32 {
	if s.MemoryMiB == 0 {
		return 2048
	}
	return s.MemoryMiB
}

// HyperVMachineInitializationStatus reports contract initialization.
// +kubebuilder:validation:MinProperties=1
type HyperVMachineInitializationStatus struct {
	// provisioned is true when the VM is running, the provider ID is set,
	// and an IPv4 address is known. It is set once.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// HyperVMachineStatus defines the observed state of HyperVMachine.
type HyperVMachineStatus struct {
	// conditions represent the observations of the machine.
	// The Ready condition is mirrored onto Machine InfrastructureReady.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// initialization reports contract provisioning state.
	// +optional
	Initialization HyperVMachineInitializationStatus `json:"initialization,omitempty,omitzero"`

	// vmID is the Hyper-V VM GUID.
	// +optional
	VMID string `json:"vmID,omitempty"`

	// vmName is the name of the VM on the host.
	// +optional
	VMName string `json:"vmName,omitempty"`

	// macAddress is the NIC MAC address, lowercase, colon separated.
	// +optional
	MACAddress string `json:"macAddress,omitempty"`

	// addresses are the addresses reported by Hyper-V integration services.
	// +optional
	Addresses clusterv1.MachineAddresses `json:"addresses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hypervmachines,shortName=hvm,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="ProviderID",type=string,JSONPath=`.spec.providerID`
// +kubebuilder:printcolumn:name="VM",type=string,JSONPath=`.status.vmName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyperVMachine is the Schema for hypervmachines API.
type HyperVMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HyperVMachineSpec   `json:"spec,omitempty"`
	Status HyperVMachineStatus `json:"status,omitempty"`
}

// GetConditions returns the status conditions.
func (m *HyperVMachine) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets the status conditions.
func (m *HyperVMachine) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// HyperVMachineList contains a list of HyperVMachine.
type HyperVMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperVMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperVMachine{}, &HyperVMachineList{})
}
