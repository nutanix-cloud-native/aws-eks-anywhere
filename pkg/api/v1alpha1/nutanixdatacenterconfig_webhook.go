package v1alpha1

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/aws/eks-anywhere/pkg/features"
)

var nutanixdatacenterconfiglog = logf.Log.WithName("nutanixdatacenterconfig-resource")

func (in *NutanixDatacenterConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(in).
		Complete()
}

var _ webhook.Validator = &NutanixDatacenterConfig{}

func (in *NutanixDatacenterConfig) ValidateCreate() error {
	nutanixdatacenterconfiglog.Info("validate create", "name", in.Name)
	if err := in.Validate(); err != nil {
		return apierrors.NewInvalid(
			GroupVersion.WithKind(NutanixDatacenterKind).GroupKind(),
			in.Name,
			field.ErrorList{
				field.Invalid(field.NewPath("spec"), in.Spec, err.Error()),
			})
	}

	if in.IsReconcilePaused() {
		nutanixdatacenterconfiglog.Info("NutanixDatacenterConfig is paused, allowing create", "name", in.Name)
		return nil
	}

	if !features.IsActive(features.FullLifecycleAPI()) {
		return apierrors.NewBadRequest("Creating new NutanixDatacenterConfig on existing cluster is not supported")
	}

	return nil
}

func (in *NutanixDatacenterConfig) ValidateUpdate(old runtime.Object) error {
	nutanixdatacenterconfiglog.Info("validate update", "name", in.Name)

	oldDatacenterConfig, ok := old.(*NutanixDatacenterConfig)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NutanixDatacenterConfig but got a %T", old))
	}

	if oldDatacenterConfig.IsReconcilePaused() {
		nutanixdatacenterconfiglog.Info("NutanixDatacenterConfig is paused, allowing update", "name", in.Name)
		return nil
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, validateImmutableFieldsNutanixDatacenterConfig(in, oldDatacenterConfig)...)

	if err := in.Validate(); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec"), in.Spec, err.Error()))
	}

	if len(allErrs) > 0 {
		return apierrors.NewInvalid(
			GroupVersion.WithKind(NutanixDatacenterKind).GroupKind(),
			in.Name,
			allErrs)
	}

	return nil
}

func (in *NutanixDatacenterConfig) ValidateDelete() error {
	nutanixdatacenterconfiglog.Info("validate delete", "name", in.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}

func validateImmutableFieldsNutanixDatacenterConfig(new, old *NutanixDatacenterConfig) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	if new.Spec.Endpoint != old.Spec.Endpoint {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("endpoint"), "field is immutable"))
	}

	return allErrs
}
