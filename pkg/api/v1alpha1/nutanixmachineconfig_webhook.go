package v1alpha1

import (
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var nutanixmachineconfiglog = logf.Log.WithName("nutanixmachineconfig-resource")

func (in *NutanixMachineConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(in).
		Complete()
}

var _ webhook.Validator = &NutanixMachineConfig{}

func (in *NutanixMachineConfig) ValidateCreate() error {
	nutanixmachineconfiglog.Info("validate create", "name", in.Name)

	if err := in.Validate(); err != nil {
		return apierrors.NewInvalid(
			GroupVersion.WithKind(NutanixMachineConfigKind).GroupKind(),
			in.Name,
			field.ErrorList{
				field.Invalid(field.NewPath("spec"), in.Spec, err.Error()),
			},
		)
	}

	if in.IsReconcilePaused() {
		nutanixmachineconfiglog.Info("NutanixMachineConfig is paused, so allowing create", "name", in.Name)
		return nil
	}

	return nil
}

func (in *NutanixMachineConfig) ValidateUpdate(old runtime.Object) error {
	nutanixmachineconfiglog.Info("validate update", "name", in.Name)

	oldNutanixMachineConfig, ok := old.(*NutanixMachineConfig)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NutanixMachineConfig but got a %T", old))
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, validateImmutableFieldsNutantixMachineConfig(in, oldNutanixMachineConfig)...)

	if err := in.Validate(); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec"), in.Spec, err.Error()))
	}

	if len(allErrs) > 0 {
		return apierrors.NewInvalid(
			GroupVersion.WithKind(NutanixMachineConfigKind).GroupKind(),
			in.Name,
			allErrs,
		)
	}

	return nil
}

func (in *NutanixMachineConfig) ValidateDelete() error {
	nutanixmachineconfiglog.Info("validate delete", "name", in.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}

func validateImmutableFieldsNutantixMachineConfig(new, old *NutanixMachineConfig) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")
	if new.Spec.OSFamily != old.Spec.OSFamily {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("OSFamily"), "field is immutable"))
	}

	if len(new.Spec.Users) != len(old.Spec.Users) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Users"), "field is immutable"))
	}

	if new.Spec.Users[0].Name != old.Spec.Users[0].Name {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Users[0].Name"), "field is immutable"))
	}

	if len(new.Spec.Users[0].SshAuthorizedKeys) != len(old.Spec.Users[0].SshAuthorizedKeys) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Users[0].SshAuthorizedKeys"), "field is immutable"))
	}

	if len(new.Spec.Users[0].SshAuthorizedKeys) > 0 && (new.Spec.Users[0].SshAuthorizedKeys[0] != old.Spec.Users[0].SshAuthorizedKeys[0]) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Users[0].SshAuthorizedKeys[0]"), "field is immutable"))
	}

	if !reflect.DeepEqual(new.Spec.Cluster, old.Spec.Cluster) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Cluster"), "field is immutable"))
	}

	if !reflect.DeepEqual(new.Spec.Subnet, old.Spec.Subnet) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Subnet"), "field is immutable"))
	}

	if !reflect.DeepEqual(new.Spec.Image, old.Spec.Image) {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("Image"), "field is immutable"))
	}

	return allErrs
}
