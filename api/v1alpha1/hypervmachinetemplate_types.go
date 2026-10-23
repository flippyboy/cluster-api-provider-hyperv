package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// HyperVMachineTemplateSpec defines the desired state of HyperVMachineTemplate.
type HyperVMachineTemplateSpec struct {
	// template is the HyperVMachine spec applied to each machine.
	// +required
	Template HyperVMachineTemplateResource `json:"template"`
}

// HyperVMachineTemplateResource embeds metadata and a HyperVMachineSpec.
// +kubebuilder:validation:MinProperties=1
type HyperVMachineTemplateResource struct {
	// metadata is the standard object metadata.
	// +optional
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the desired state of the HyperVMachine.
	// +required
	Spec HyperVMachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hypervmachinetemplates,shortName=hvmt,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HyperVMachineTemplate is the Schema for hypervmachinetemplates API.
type HyperVMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HyperVMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// HyperVMachineTemplateList contains a list of HyperVMachineTemplate.
type HyperVMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HyperVMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HyperVMachineTemplate{}, &HyperVMachineTemplateList{})
}
