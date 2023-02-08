package controllers

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	anywherev1 "github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/providers/nutanix"
	"github.com/aws/eks-anywhere/pkg/providers/nutanix/reconciler"
)

// NutanixDatacenterReconciler reconciles a NutanixDatacenterConfig object.
type NutanixDatacenterReconciler struct {
	client        client.Client
	clientBuilder *nutanix.ClientBuilder
	defaulter     *nutanix.Defaulter
	validator     *nutanix.Validator
}

func (r *NutanixDatacenterReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	dc := &anywherev1.NutanixDatacenterConfig{}
	if err := r.client.Get(ctx, request.NamespacedName, dc); err != nil {
		return ctrl.Result{}, err
	}

	creds, err := reconciler.GetNutanixCredsFromSecret(ctx, r.client, dc.Name, "eksa-system")
	if err != nil {
		log.Error(err, "failed to get Nutanix credentials")
		return ctrl.Result{}, err
	}

	if !dc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, dc)
	}

	c, err := r.clientBuilder.GetNutanixClient(dc, creds)
	if err != nil {
		log.Error(err, "failed to create Nutanix client")
		return ctrl.Result{}, err
	}

	r.defaulter.SetDefaultsForDatacenterConfig(*dc)
	if err := r.validator.ValidateDatacenterConfig(ctx, c, dc); err != nil {
		log.Error(err, "failed to validate NutanixDatacenterConfig")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NutanixDatacenterReconciler) reconcileDelete(ctx context.Context, dc *anywherev1.NutanixDatacenterConfig) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// NewNutanixDatacenterReconciler constructs a new NutanixDatacenterReconciler.
func NewNutanixDatacenterReconciler(client client.Client, clientBuilder *nutanix.ClientBuilder, validator *nutanix.Validator, defaulter *nutanix.Defaulter) *NutanixDatacenterReconciler {
	return &NutanixDatacenterReconciler{
		client:        client,
		clientBuilder: clientBuilder,
		validator:     validator,
		defaulter:     defaulter,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NutanixDatacenterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&anywherev1.NutanixDatacenterConfig{}).
		Complete(r)
}
