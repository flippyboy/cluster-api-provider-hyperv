package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/cloudinit"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/kubevip"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
)

// HyperVMachineReconciler reconciles a HyperVMachine.
type HyperVMachineReconciler struct {
	client.Client
	// Dial opens a host connection. Tests inject a fake. Nil uses hyperv.Dial.
	Dial func(context.Context, hyperv.HostConfig) (*hyperv.Client, error)
}

// Reconcile creates one Hyper-V VM and removes it on delete.
func (r *HyperVMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)
	hvMachine := &infrav1.HyperVMachine{}
	if err := r.Get(ctx, req.NamespacedName, hvMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if annotations.IsExternallyManaged(hvMachine) {
		log.Info("HyperVMachine is externally managed, skipping")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(hvMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, hvMachine); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, fmt.Errorf("patch HyperVMachine: %w", err)})
		}
	}()

	machine, err := util.GetOwnerMachine(ctx, r.Client, hvMachine.ObjectMeta)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	cluster, err := r.ownerCluster(ctx, hvMachine, machine)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if isPaused(cluster, machine, hvMachine) {
		markPaused(hvMachine)
		return ctrl.Result{}, nil
	}
	clearPaused(hvMachine)

	if !hvMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, hvMachine, cluster, machine)
	}
	if cluster == nil || machine == nil {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForOwnerReason, "HyperVMachine is waiting for its Machine and Cluster")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if ptr.Deref(hvMachine.Status.Initialization.Provisioned, false) {
		controllerutil.AddFinalizer(hvMachine, infrav1.HyperVMachineFinalizer)
		markReady(hvMachine, metav1.ConditionTrue, infrav1.ProvisionedReason, "VM is running")
		return ctrl.Result{}, nil
	}
	return r.reconcileNormal(ctx, patchHelper, hvMachine, cluster, machine)
}

func (r *HyperVMachineReconciler) reconcileDelete(ctx context.Context, hvMachine *infrav1.HyperVMachine, cluster *clusterv1.Cluster, machine *clusterv1.Machine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	markReady(hvMachine, metav1.ConditionFalse, infrav1.DeletingReason, "VM is being deleted")
	if !controllerutil.ContainsFinalizer(hvMachine, infrav1.HyperVMachineFinalizer) {
		return ctrl.Result{}, nil
	}
	if hvMachine.Status.VMName == "" && hvMachine.Status.VMID == "" {
		controllerutil.RemoveFinalizer(hvMachine, infrav1.HyperVMachineFinalizer)
		return ctrl.Result{}, nil
	}

	hvCluster, err := r.hyperVCluster(ctx, hvMachine.Namespace, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			markReady(hvMachine, metav1.ConditionFalse, infrav1.CredentialsInvalidReason, "HyperVCluster was not found; VM deletion is waiting for credentials")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}
	cfg, err := hostConfig(ctx, r.Client, hvCluster)
	if err != nil {
		log.Error(err, "credentials are not usable during delete")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.CredentialsInvalidReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	host, err := r.dial(ctx, cfg)
	if err != nil {
		log.Error(err, "failed to connect to Hyper-V host during delete")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostConnectionFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	defer host.Close()

	vmName := hvMachine.Status.VMName
	if vmName == "" && cluster != nil && machine != nil {
		vmName = hyperv.VMName(cluster.Name, machine.Name)
	}
	if vmName == "" {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.DeletingReason, "VM name is unknown")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if err := host.Delete(ctx, vmName, imagePath(hvCluster, hvMachine)); err != nil {
		log.Error(err, "failed to delete VM")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	controllerutil.RemoveFinalizer(hvMachine, infrav1.HyperVMachineFinalizer)
	return ctrl.Result{}, nil
}

func (r *HyperVMachineReconciler) reconcileNormal(ctx context.Context, patchHelper *patch.Helper, hvMachine *infrav1.HyperVMachine, cluster *clusterv1.Cluster, machine *clusterv1.Machine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	controllerutil.AddFinalizer(hvMachine, infrav1.HyperVMachineFinalizer)

	hvCluster, err := r.hyperVCluster(ctx, hvMachine.Namespace, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForClusterInfrastructureReason, "HyperVCluster was not found")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}
	if !ptr.Deref(hvCluster.Status.Initialization.Provisioned, false) {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForClusterInfrastructureReason, "HyperVCluster is not provisioned")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	userData, err := bootstrapData(ctx, r.Client, hvMachine.Namespace, machine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForBootstrapDataReason, "bootstrap data secret was not found")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}
	if len(userData) == 0 {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForBootstrapDataReason, "bootstrap data secret has no value")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if hvMachine.Status.VMName == "" {
		hvMachine.Status.VMName = hyperv.VMName(cluster.Name, machine.Name)
		if err := patchHelper.Patch(ctx, hvMachine); err != nil {
			return ctrl.Result{}, err
		}
	}

	cfg, err := hostConfig(ctx, r.Client, hvCluster)
	if err != nil {
		log.Error(err, "credentials are not usable")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.CredentialsInvalidReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	host, err := r.dial(ctx, cfg)
	if err != nil {
		log.Error(err, "failed to connect to Hyper-V host")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostConnectionFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	defer host.Close()

	mac := hvMachine.Status.MACAddress
	if mac == "" {
		mac, err = hyperv.RandomMAC()
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	vm, err := host.EnsureVM(ctx, hyperv.VMInput{
		Name:          hvMachine.Status.VMName,
		VMPath:        hvCluster.Spec.VMPath,
		MemoryMiB:     hvMachine.Spec.EffectiveMemoryMiB(),
		VCPUs:         hvMachine.Spec.EffectiveVCPUs(),
		VirtualSwitch: switchName(hvCluster, hvMachine),
		MACAddress:    mac,
	})
	if err != nil {
		log.Error(err, "failed to ensure VM")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	hvMachine.Status.VMID = vm.VMID
	hvMachine.Status.VMName = vm.VMName
	hvMachine.Status.MACAddress = vm.MACAddress

	if _, err := host.EnsureDisk(ctx, hyperv.DiskInput{
		VMName:    vm.VMName,
		ImagePath: imagePath(hvCluster, hvMachine),
		CloneMode: string(hvMachine.Spec.Disk.NormalizedCloneMode()),
		SizeGiB:   hvMachine.Spec.Disk.SizeGiB,
	}); err != nil {
		log.Error(err, "failed to ensure disk")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	providerID, err := hyperv.ProviderID(vm.VMID)
	if err != nil {
		return ctrl.Result{}, err
	}
	injected, err := cloudinit.InjectProviderID(userData, providerID)
	if err != nil {
		if cloudinit.IsInvalidBootstrap(err) {
			markReady(hvMachine, metav1.ConditionFalse, infrav1.BootstrapDataInvalidReason, err.Error())
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}
	netcfg := cloudinit.NetworkConfig(vm.MACAddress)
	if hvCluster.Spec.KubeVIP.Enabled && isControlPlane(machine) {
		injected, netcfg, err = injectKubeVIP(injected, vm.MACAddress, cluster, hvCluster)
		if err != nil {
			markReady(hvMachine, metav1.ConditionFalse, infrav1.KubeVIPConfigInvalidReason, err.Error())
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
	}
	isoBytes, err := cloudinit.Build(injected, cloudinit.MetaData(vm.VMID, machine.Name), netcfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build cidata iso: %w", err)
	}
	if vm.VMPath == "" {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, "VM path is empty")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	isoPath := hyperv.JoinWin(vm.VMPath, "cidata.iso")
	if err := host.Upload(ctx, isoPath, isoBytes); err != nil {
		log.Error(err, "failed to upload cidata iso")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	iso, err := host.EnsureISO(ctx, vm.VMName, isoPath)
	if err != nil {
		log.Error(err, "failed to attach cidata iso")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if iso.NeedsRestart {
		if _, err := host.Stop(ctx, vm.VMName); err != nil {
			log.Error(err, "failed to stop VM")
			markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
	}
	if _, err := host.Start(ctx, vm.VMName); err != nil {
		log.Error(err, "failed to start VM")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	inspected, err := host.Inspect(ctx, vm.VMName)
	if err != nil {
		log.Error(err, "failed to inspect VM")
		markReady(hvMachine, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if inspected.VMID != "" {
		hvMachine.Status.VMID = inspected.VMID
	}
	if inspected.MACAddress != "" {
		hvMachine.Status.MACAddress = inspected.MACAddress
	}
	ips := hyperv.IPv4Addresses(inspected.IPAddresses)
	if len(ips) == 0 {
		markReady(hvMachine, metav1.ConditionFalse, infrav1.WaitingForIPReason, "waiting for an IPv4 address from Hyper-V integration services")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	hvMachine.Spec.ProviderID = providerID
	hvMachine.Status.Addresses = machineAddresses(machine.Name, ips)
	hvMachine.Status.Initialization.Provisioned = ptr.To(true)
	markReady(hvMachine, metav1.ConditionTrue, infrav1.ProvisionedReason, "VM is running and has an IP address")
	return ctrl.Result{}, nil
}

func (r *HyperVMachineReconciler) ownerCluster(ctx context.Context, hvMachine *infrav1.HyperVMachine, machine *clusterv1.Machine) (*clusterv1.Cluster, error) {
	if machine != nil && machine.Spec.ClusterName != "" {
		return util.GetClusterByName(ctx, r.Client, hvMachine.Namespace, machine.Spec.ClusterName)
	}
	if name := hvMachine.Labels[clusterv1.ClusterNameLabel]; name != "" {
		return util.GetClusterByName(ctx, r.Client, hvMachine.Namespace, name)
	}
	return nil, nil
}

func (r *HyperVMachineReconciler) hyperVCluster(ctx context.Context, namespace string, cluster *clusterv1.Cluster) (*infrav1.HyperVCluster, error) {
	if cluster == nil || !cluster.Spec.InfrastructureRef.IsDefined() {
		return nil, apierrors.NewNotFound(infrav1.GroupVersion.WithResource("hypervclusters").GroupResource(), "")
	}
	hvCluster := &infrav1.HyperVCluster{}
	key := client.ObjectKey{Namespace: namespace, Name: cluster.Spec.InfrastructureRef.Name}
	if err := r.Get(ctx, key, hvCluster); err != nil {
		return nil, err
	}
	return hvCluster, nil
}

func (r *HyperVMachineReconciler) dial(ctx context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
	if r.Dial != nil {
		return r.Dial(ctx, cfg)
	}
	return hyperv.Dial(ctx, cfg)
}

func hostConfig(ctx context.Context, c client.Client, hvCluster *infrav1.HyperVCluster) (hyperv.HostConfig, error) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: hvCluster.Namespace, Name: hvCluster.Spec.CredentialsSecretRef.Name}
	if err := c.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return hyperv.HostConfig{}, fmt.Errorf("credentials secret %s was not found", key.Name)
		}
		return hyperv.HostConfig{}, err
	}
	return HostConfigFrom(hvCluster.Spec, secret)
}

func bootstrapData(ctx context.Context, c client.Client, namespace string, machine *clusterv1.Machine) ([]byte, error) {
	if machine.Spec.Bootstrap.DataSecretName == nil || *machine.Spec.Bootstrap.DataSecretName == "" {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), "")
	}
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: namespace, Name: *machine.Spec.Bootstrap.DataSecretName}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, err
	}
	return secret.Data["value"], nil
}

func isPaused(cluster *clusterv1.Cluster, machine *clusterv1.Machine, hvMachine *infrav1.HyperVMachine) bool {
	if annotations.HasPaused(hvMachine) {
		return true
	}
	if machine != nil && annotations.HasPaused(machine) {
		return true
	}
	return cluster != nil && annotations.IsPaused(cluster, hvMachine)
}

func imagePath(cluster *infrav1.HyperVCluster, machine *infrav1.HyperVMachine) string {
	if machine.Spec.Disk.ImagePath != "" {
		return machine.Spec.Disk.ImagePath
	}
	return cluster.Spec.ImagePath
}

func switchName(cluster *infrav1.HyperVCluster, machine *infrav1.HyperVMachine) string {
	if machine.Spec.VirtualSwitch != "" {
		return machine.Spec.VirtualSwitch
	}
	return cluster.Spec.VirtualSwitch
}

func injectKubeVIP(userData []byte, mac string, cluster *clusterv1.Cluster, hvCluster *infrav1.HyperVCluster) ([]byte, []byte, error) {
	endpoint := cluster.Spec.ControlPlaneEndpoint
	if !endpoint.IsValid() {
		return nil, nil, fmt.Errorf("Cluster.spec.controlPlaneEndpoint is not set")
	}
	spec := hvCluster.Spec.KubeVIP
	manifest, err := kubevip.Manifest(kubevip.Settings{
		Address:   endpoint.Host,
		Port:      int(endpoint.Port),
		Interface: spec.EffectiveInterface(),
		Image:     spec.EffectiveImage(),
	})
	if err != nil {
		return nil, nil, err
	}
	updated, err := cloudinit.AddWriteFile(userData, "/etc/kubernetes/manifests/kube-vip.yaml", string(manifest))
	if err != nil {
		return nil, nil, err
	}
	netcfg, err := cloudinit.NetworkConfigNamed(mac, spec.EffectiveInterface())
	if err != nil {
		return nil, nil, err
	}
	return updated, netcfg, nil
}

func isControlPlane(machine *clusterv1.Machine) bool {
	if machine == nil || machine.Labels == nil {
		return false
	}
	_, ok := machine.Labels[clusterv1.MachineControlPlaneLabel]
	return ok
}

func machineAddresses(hostname string, ips []string) clusterv1.MachineAddresses {
	addrs := clusterv1.MachineAddresses{{
		Type:    clusterv1.MachineHostName,
		Address: hostname,
	}}
	for _, ip := range ips {
		addrs = append(addrs, clusterv1.MachineAddress{
			Type:    clusterv1.MachineInternalIP,
			Address: ip,
		})
	}
	return addrs
}

// SetupWithManager registers the controller.
func (r *HyperVMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.HyperVMachine{}, builder.WithPredicates(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), ctrl.Log))).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("HyperVMachine"))),
		).
		Complete(r)
}
