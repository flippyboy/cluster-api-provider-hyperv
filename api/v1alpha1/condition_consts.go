package v1alpha1

import clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

const (
	// HyperVClusterReadyCondition mirrors Cluster InfrastructureReady.
	HyperVClusterReadyCondition = clusterv1.ReadyCondition
	// HyperVMachineReadyCondition mirrors Machine InfrastructureReady.
	HyperVMachineReadyCondition = clusterv1.ReadyCondition
)

const (
	// WaitingForOwnerReason is set until the Cluster owner reference exists.
	WaitingForOwnerReason = "WaitingForOwner"
	// WaitingForControlPlaneEndpointReason is set until Cluster.spec.controlPlaneEndpoint is valid.
	WaitingForControlPlaneEndpointReason = "WaitingForControlPlaneEndpoint"
	// WaitingForClusterInfrastructureReason is set until HyperVCluster is provisioned.
	WaitingForClusterInfrastructureReason = "WaitingForClusterInfrastructure"
	// WaitingForBootstrapDataReason is set until the bootstrap Secret exists.
	WaitingForBootstrapDataReason = "WaitingForBootstrapData"
	// WaitingForIPReason is set until integration services report an IPv4 address.
	WaitingForIPReason = "WaitingForIP"
	// CredentialsInvalidReason is set when the Secret is missing or incomplete.
	CredentialsInvalidReason = "CredentialsInvalid"
	// HostConnectionFailedReason is set when SSH or WinRM cannot be established.
	HostConnectionFailedReason = "HostConnectionFailed"
	// VirtualSwitchNotFoundReason is set when the named switch does not exist.
	VirtualSwitchNotFoundReason = "VirtualSwitchNotFound"
	// ImageNotFoundReason is set when the golden VHDX is missing.
	ImageNotFoundReason = "ImageNotFound"
	// ImageNotVHDXReason is set when the image path is not a VHDX.
	ImageNotVHDXReason = "ImageNotVHDX"
	// BootstrapDataInvalidReason is set when user-data is not injectable kubeadm cloud-config.
	BootstrapDataInvalidReason = "BootstrapDataInvalid"
	// KubeVIPConfigInvalidReason is set when kube-vip is enabled but the endpoint or guest interface cannot be used.
	KubeVIPConfigInvalidReason = "KubeVIPConfigInvalid"
	// HostCommandFailedReason is set when a PowerShell command fails.
	HostCommandFailedReason = "HostCommandFailed"
	// ProvisionedReason is set when initialization has completed.
	ProvisionedReason = "Provisioned"
	// DeletingReason is set while the VM is being removed.
	DeletingReason = "Deleting"
	// PausedReason is set while reconciliation is paused.
	PausedReason = clusterv1.PausedReason
)
