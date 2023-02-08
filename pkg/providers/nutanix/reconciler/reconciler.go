package reconciler

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	apiv1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	anywherev1 "github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/cluster"
	"github.com/aws/eks-anywhere/pkg/controller"
	"github.com/aws/eks-anywhere/pkg/controller/clientutil"
	"github.com/aws/eks-anywhere/pkg/controller/clusters"
	"github.com/aws/eks-anywhere/pkg/controller/serverside"
	"github.com/aws/eks-anywhere/pkg/providers/nutanix"
)

// CNIReconciler is an interface for reconciling CNI in the Tinkerbell cluster reconciler.
type CNIReconciler interface {
	Reconcile(ctx context.Context, logger logr.Logger, client client.Client, spec *cluster.Spec) (controller.Result, error)
}

// RemoteClientRegistry is an interface that defines methods for remote clients.
type RemoteClientRegistry interface {
	GetClient(ctx context.Context, cluster client.ObjectKey) (client.Client, error)
}

// IPValidator is an interface that defines methods to validate the control plane IP.
type IPValidator interface {
	ValidateControlPlaneIP(ctx context.Context, log logr.Logger, spec *cluster.Spec) (controller.Result, error)
}

type Reconciler struct {
	client               client.Client
	validator            *nutanix.Validator
	defaulter            *nutanix.Defaulter
	cniReconciler        CNIReconciler
	remoteClientRegistry RemoteClientRegistry
	ipValidator          IPValidator
	*serverside.ObjectApplier
}

// New defines a new Nutanix reconciler.
func New(client client.Client, validator *nutanix.Validator, defaulter *nutanix.Defaulter, cniReconciler CNIReconciler, registry RemoteClientRegistry, ipValidator IPValidator) *Reconciler {
	return &Reconciler{
		client:               client,
		validator:            validator,
		defaulter:            defaulter,
		cniReconciler:        cniReconciler,
		remoteClientRegistry: registry,
		ipValidator:          ipValidator,
		ObjectApplier:        serverside.NewObjectApplier(client),
	}
}

func getSecret(ctx context.Context, kubectl client.Client, secretName, secretNS string) (*apiv1.Secret, error) {
	secret := &apiv1.Secret{}
	secretKey := client.ObjectKey{
		Namespace: secretNS,
		Name:      secretName,
	}
	if err := kubectl.Get(ctx, secretKey, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func GetNutanixCredsFromSecret(ctx context.Context, kubectl client.Client, secretName, secretNS string) (credentials.BasicAuthCredential, error) {
	secret, err := getSecret(ctx, kubectl, secretName, secretNS)
	if err != nil {
		return credentials.BasicAuthCredential{}, fmt.Errorf("failed getting nutanix credentials secret: %v", err)
	}

	creds, err := credentials.ParseCredentials(secret.Data["credentials"])
	if err != nil {
		return credentials.BasicAuthCredential{}, fmt.Errorf("failed parsing nutanix credentials: %v", err)
	}

	return credentials.BasicAuthCredential{PrismCentral: credentials.PrismCentralBasicAuth{
		BasicAuth: credentials.BasicAuth{
			Username: creds.Username,
			Password: creds.Password,
		},
	}}, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, log logr.Logger, c *anywherev1.Cluster) (controller.Result, error) {
	log = log.WithValues("provider", "nutanix")
	clusterSpec, err := cluster.BuildSpec(ctx, clientutil.NewKubeClient(r.client), c)
	if err != nil {
		return controller.Result{}, err
	}

	return controller.NewPhaseRunner().Register(
		r.ipValidator.ValidateControlPlaneIP,
		r.ValidateClusterSpec,
		clusters.CleanupStatusAfterValidate,
		r.ReconcileControlPlane,
		r.CheckControlPlaneReady,
		r.ReconcileCNI,
		r.ReconcileWorkers,
	).Run(ctx, log, clusterSpec)
}

// ReconcileCNI reconciles the CNI to the desired state.
func (r *Reconciler) ReconcileCNI(ctx context.Context, log logr.Logger, clusterSpec *cluster.Spec) (controller.Result, error) {
	log = log.WithValues("phase", "reconcileCNI")

	c, err := r.remoteClientRegistry.GetClient(ctx, controller.CapiClusterObjectKey(clusterSpec.Cluster))
	if err != nil {
		return controller.Result{}, err
	}

	return r.cniReconciler.Reconcile(ctx, log, c, clusterSpec)
}

// ValidateClusterSpec performs additional, context-aware validations on the cluster spec.
func (r *Reconciler) ValidateClusterSpec(ctx context.Context, log logr.Logger, clusterSpec *cluster.Spec) (controller.Result, error) {
	fmt.Println("Validating cluster spec")
	fmt.Printf("%+v", clusterSpec.Config.Cluster.Spec.DatacenterRef)
	log = log.WithValues("phase", "validateClusterSpec")

	creds, err := GetNutanixCredsFromSecret(ctx, r.client, clusterSpec.NutanixDatacenter.Name, "eksa-system")
	if err != nil {
		return controller.Result{}, err
	}

	if err := r.validator.ValidateClusterSpec(ctx, clusterSpec, creds); err != nil {
		log.Error(err, "Invalid cluster spec")
		failureMessage := err.Error()
		clusterSpec.Cluster.Status.FailureMessage = &failureMessage
		return controller.ResultWithReturn(), nil
	}

	return controller.Result{}, nil
}

func (r *Reconciler) ReconcileControlPlane(ctx context.Context, log logr.Logger, clusterSpec *cluster.Spec) (controller.Result, error) {
	log = log.WithValues("phase", "reconcileControlPlane")

	cp, err := nutanix.ControlPlaneSpec(ctx, log, clientutil.NewKubeClient(r.client), clusterSpec)
	if err != nil {
		return controller.Result{}, err
	}

	return clusters.ReconcileControlPlane(ctx, r.client, toClientControlPlane(cp))
}

func toClientControlPlane(cp *nutanix.ControlPlane) *clusters.ControlPlane {
	return &clusters.ControlPlane{
		Cluster:                     cp.Cluster,
		ProviderCluster:             cp.ProviderCluster,
		KubeadmControlPlane:         cp.KubeadmControlPlane,
		ControlPlaneMachineTemplate: cp.ControlPlaneMachineTemplate,
		EtcdCluster:                 cp.EtcdCluster,
		EtcdMachineTemplate:         cp.EtcdMachineTemplate,
	}
}

func (r *Reconciler) ReconcileWorkerNodes(ctx context.Context, log logr.Logger, eksCluster *anywherev1.Cluster) (controller.Result, error) {
	log = log.WithValues("provider", "nutanix", "reconcile type", "workers")
	clusterSpec, err := cluster.BuildSpec(ctx, clientutil.NewKubeClient(r.client), eksCluster)
	if err != nil {
		return controller.Result{}, err
	}

	return controller.NewPhaseRunner().Register(
		r.ValidateClusterSpec,
		r.ReconcileWorkers,
	).Run(ctx, log, clusterSpec)
}

func (r *Reconciler) ReconcileWorkers(ctx context.Context, log logr.Logger, spec *cluster.Spec) (controller.Result, error) {
	if spec.NutanixDatacenter == nil {
		return controller.Result{}, nil
	}
	log = log.WithValues("phase", "reconcileWorkers")
	log.Info("Applying worker CAPI objects")
	w, err := nutanix.WorkersSpec(ctx, log, clientutil.NewKubeClient(r.client), spec)
	if err != nil {
		return controller.Result{}, err
	}

	return clusters.ReconcileWorkersForEKSA(ctx, log, r.client, spec.Cluster, clusters.ToWorkers(w))
}

// CheckControlPlaneReady checks whether the control plane for an eks-a cluster is ready or not.
// Requeues with the appropriate wait times whenever the cluster is not ready yet.
func (r *Reconciler) CheckControlPlaneReady(ctx context.Context, log logr.Logger, clusterSpec *cluster.Spec) (controller.Result, error) {
	log = log.WithValues("phase", "checkControlPlaneReady")
	return clusters.CheckControlPlaneReady(ctx, r.client, log, clusterSpec.Cluster)
}
