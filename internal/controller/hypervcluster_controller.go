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
	"sigs.k8s.io/controller-runtime/pkg/handler"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
)

// HyperVClusterReconciler reconciles a HyperVCluster.
type HyperVClusterReconciler struct {
	client.Client
	// Dial opens a host connection. Tests inject a fake. Nil uses hyperv.Dial.
	Dial func(context.Context, hyperv.HostConfig) (*hyperv.Client, error)
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=hypervmachinetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;machines,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// Reconcile probes the Hyper-V host and marks the cluster provisioned once.
func (r *HyperVClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)
	hv := &infrav1.HyperVCluster{}
	if err := r.Get(ctx, req.NamespacedName, hv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if annotations.IsExternallyManaged(hv) {
		log.Info("HyperVCluster is externally managed, skipping")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(hv, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, hv); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, fmt.Errorf("patch HyperVCluster: %w", err)})
		}
	}()

	if !hv.DeletionTimestamp.IsZero() {
		clearPaused(hv)
		markReady(hv, metav1.ConditionFalse, infrav1.DeletingReason, "HyperVCluster is deleting")
		return ctrl.Result{}, nil
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, hv.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		markReady(hv, metav1.ConditionFalse, infrav1.WaitingForOwnerReason, "HyperVCluster has no owner Cluster")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if annotations.IsPaused(cluster, hv) {
		markPaused(hv)
		return ctrl.Result{}, nil
	}
	clearPaused(hv)

	if !cluster.Spec.ControlPlaneEndpoint.IsValid() {
		markReady(hv, metav1.ConditionFalse, infrav1.WaitingForControlPlaneEndpointReason, "Cluster.spec.controlPlaneEndpoint is not set")
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	cfg, err := r.hostConfig(ctx, hv)
	if err != nil {
		log.Error(err, "credentials are not usable")
		markReady(hv, metav1.ConditionFalse, infrav1.CredentialsInvalidReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	host, err := r.dial(ctx, cfg)
	if err != nil {
		log.Error(err, "failed to connect to Hyper-V host")
		markReady(hv, metav1.ConditionFalse, infrav1.HostConnectionFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	defer host.Close()

	probe, err := host.Probe(ctx, hv.Spec.VirtualSwitch, hv.Spec.ImagePath)
	if err != nil {
		log.Error(err, "host probe failed")
		markReady(hv, metav1.ConditionFalse, infrav1.HostCommandFailedReason, err.Error())
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if !probe.SwitchFound {
		markReady(hv, metav1.ConditionFalse, infrav1.VirtualSwitchNotFoundReason, fmt.Sprintf("virtual switch %q was not found", hv.Spec.VirtualSwitch))
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if !probe.ImageFound {
		markReady(hv, metav1.ConditionFalse, infrav1.ImageNotFoundReason, fmt.Sprintf("image %q was not found", hv.Spec.ImagePath))
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if !probe.ImageIsVHDX {
		markReady(hv, metav1.ConditionFalse, infrav1.ImageNotVHDXReason, fmt.Sprintf("image %q is not a VHDX", hv.Spec.ImagePath))
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if !ptr.Deref(hv.Status.Initialization.Provisioned, false) {
		hv.Status.Initialization.Provisioned = ptr.To(true)
	}
	markReady(hv, metav1.ConditionTrue, infrav1.ProvisionedReason, "Hyper-V host is ready")
	return ctrl.Result{}, nil
}

func (r *HyperVClusterReconciler) hostConfig(ctx context.Context, hv *infrav1.HyperVCluster) (hyperv.HostConfig, error) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: hv.Namespace, Name: hv.Spec.CredentialsSecretRef.Name}
	if err := r.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return hyperv.HostConfig{}, fmt.Errorf("credentials secret %s was not found", key.Name)
		}
		return hyperv.HostConfig{}, err
	}
	return HostConfigFrom(hv.Spec, secret)
}

func (r *HyperVClusterReconciler) dial(ctx context.Context, cfg hyperv.HostConfig) (*hyperv.Client, error) {
	if r.Dial != nil {
		return r.Dial(ctx, cfg)
	}
	return hyperv.Dial(ctx, cfg)
}

// SetupWithManager registers the controller.
func (r *HyperVClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.HyperVCluster{}, builder.WithPredicates(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), ctrl.Log))).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(util.ClusterToInfrastructureMapFunc(
				context.Background(),
				infrav1.GroupVersion.WithKind("HyperVCluster"),
				mgr.GetClient(),
				&infrav1.HyperVCluster{},
			)),
		).
		Complete(r)
}
