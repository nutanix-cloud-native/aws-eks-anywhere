package nutanix

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"

	"github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/clients/kubernetes"
	"github.com/aws/eks-anywhere/pkg/cluster"
	"github.com/aws/eks-anywhere/pkg/clusterapi"
	yamlcapi "github.com/aws/eks-anywhere/pkg/clusterapi/yaml"
	"github.com/aws/eks-anywhere/pkg/yamlutil"
)

// BaseControlPlane represents a CAPI Nutanix control plane.
type BaseControlPlane = clusterapi.ControlPlane[*nutanixv1.NutanixCluster, *nutanixv1.NutanixMachineTemplate]

// ControlPlane holds the Nutanix specific objects for a CAPI Nutanix control plane.
type ControlPlane struct {
	BaseControlPlane
}

// Objects returns the control plane objects associated with the Nutanix cluster.
func (p ControlPlane) Objects() []kubernetes.Object {
	o := p.BaseControlPlane.Objects()
	return o
}

// ControlPlaneBuilder defines the builder for all objects in the CAPI Nutanix control plane.
type ControlPlaneBuilder struct {
	BaseBuilder  *yamlcapi.ControlPlaneBuilder[*nutanixv1.NutanixCluster, *nutanixv1.NutanixMachineTemplate]
	ControlPlane *ControlPlane
}

// BuildFromParsed implements the base yamlcapi.BuildFromParsed and processes any additional objects for the Nutanix control plane.
func (b *ControlPlaneBuilder) BuildFromParsed(lookup yamlutil.ObjectLookup) error {
	if err := b.BaseBuilder.BuildFromParsed(lookup); err != nil {
		return err
	}

	b.ControlPlane.BaseControlPlane = *b.BaseBuilder.ControlPlane
	return nil
}

// ControlPlaneSpec builds a nutanix ControlPlane definition based on an eks-a cluster spec.
func ControlPlaneSpec(ctx context.Context, logger logr.Logger, client kubernetes.Client, spec *cluster.Spec) (*ControlPlane, error) {
	ndcs := spec.NutanixDatacenter.Spec
	machineConfigs := spec.NutanixMachineConfigs
	var controlPlaneMachineSpec, etcdMachineSpec *v1alpha1.NutanixMachineConfigSpec
	if spec.Cluster.Spec.ControlPlaneConfiguration.MachineGroupRef != nil && machineConfigs[spec.Cluster.Spec.ControlPlaneConfiguration.MachineGroupRef.Name] != nil {
		controlPlaneMachineSpec = &machineConfigs[spec.Cluster.Spec.ControlPlaneConfiguration.MachineGroupRef.Name].Spec
	}

	if spec.Cluster.Spec.ExternalEtcdConfiguration != nil {
		if spec.Cluster.Spec.ExternalEtcdConfiguration.MachineGroupRef != nil && machineConfigs[spec.Cluster.Spec.ExternalEtcdConfiguration.MachineGroupRef.Name] != nil {
			etcdMachineSpec = &machineConfigs[spec.Cluster.Spec.ExternalEtcdConfiguration.MachineGroupRef.Name].Spec
		}
	}

	wnmcs := make(map[string]v1alpha1.NutanixMachineConfigSpec)
	for _, machineConfig := range machineConfigs {
		machineConfig.SetDefaults()
	}

	creds := GetCredsFromEnv()
	templateBuilder := NewNutanixTemplateBuilder(&ndcs, controlPlaneMachineSpec, etcdMachineSpec, wnmcs, creds, time.Now)
	for _, workerNodeGroupConfiguration := range spec.Cluster.Spec.WorkerNodeGroupConfigurations {
		templateBuilder.workerNodeGroupMachineSpecs[workerNodeGroupConfiguration.MachineGroupRef.Name] = machineConfigs[workerNodeGroupConfiguration.MachineGroupRef.Name].Spec
	}

	controlPlaneYaml, err := templateBuilder.GenerateCAPISpecControlPlane(
		spec,
		func(values map[string]interface{}) {
			values["controlPlaneTemplateName"] = clusterapi.ControlPlaneMachineTemplateName(spec.Cluster)
			values["etcdTemplateName"] = clusterapi.EtcdMachineTemplateName(spec.Cluster)
		},
	)
	if err != nil {
		return nil, err
	}

	parser, builder, err := newControlPlaneParser(logger)
	if err != nil {
		return nil, err
	}

	if err := parser.Parse(controlPlaneYaml, builder); err != nil {
		return nil, err
	}

	cp := builder.ControlPlane
	if err := cp.UpdateImmutableObjectNames(ctx, client, getMachineTemplate, machineTemplateEquals); err != nil {
		return nil, err
	}

	return cp, nil
}

func newControlPlaneParser(logger logr.Logger) (*yamlutil.Parser, *ControlPlaneBuilder, error) {
	parser, baseBuilder, err := yamlcapi.NewControlPlaneParserAndBuilder(
		logger,
		yamlutil.NewMapping(
			"NutanixCluster",
			func() *nutanixv1.NutanixCluster {
				return &nutanixv1.NutanixCluster{}
			},
		),
		yamlutil.NewMapping(
			"NutanixMachineTemplate",
			func() *nutanixv1.NutanixMachineTemplate {
				return &nutanixv1.NutanixMachineTemplate{}
			},
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed building nutanix control plane parser: %w", err)
	}

	builder := &ControlPlaneBuilder{
		BaseBuilder:  baseBuilder,
		ControlPlane: &ControlPlane{},
	}

	return parser, builder, nil
}
