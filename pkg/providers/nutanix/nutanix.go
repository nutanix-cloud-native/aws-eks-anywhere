package nutanix

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"reflect"
	"time"

	etcdv1beta1 "github.com/mrajashree/etcdadm-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	kubeadmv1beta1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"

	"github.com/aws/eks-anywhere/pkg/api/v1alpha1"
	"github.com/aws/eks-anywhere/pkg/bootstrapper"
	"github.com/aws/eks-anywhere/pkg/cluster"
	"github.com/aws/eks-anywhere/pkg/constants"
	"github.com/aws/eks-anywhere/pkg/crypto"
	"github.com/aws/eks-anywhere/pkg/executables"
	"github.com/aws/eks-anywhere/pkg/features"
	"github.com/aws/eks-anywhere/pkg/logger"
	"github.com/aws/eks-anywhere/pkg/providers"
	"github.com/aws/eks-anywhere/pkg/providers/common"
	"github.com/aws/eks-anywhere/pkg/templater"
	"github.com/aws/eks-anywhere/pkg/types"

	releasev1alpha1 "github.com/aws/eks-anywhere/release/api/v1alpha1"
)

const (
	nutanixUsernameKey = "NUTANIX_USER"
	nutanixPasswordKey = "NUTANIX_PASSWORD"
	nutanixEndpointKey = "NUTANIX_ENDPOINT"
)

//go:embed config/cp-template.yaml
var defaultCAPIConfigCP string

//go:embed config/md-template.yaml
var defaultClusterConfigMD string

//go:embed config/machine-health-check-template.yaml
var mhcTemplate []byte

var (
	eksaNutanixDatacenterResourceType = fmt.Sprintf("nutanixdatacenterconfigs.%s", v1alpha1.GroupVersion.Group)
	eksaNutanixMachineResourceType    = fmt.Sprintf("nutanixmachineconfigs.%s", v1alpha1.GroupVersion.Group)
	requiredEnvs                      = []string{nutanixEndpointKey, nutanixUsernameKey, nutanixPasswordKey}
)

type basicAuthCreds struct {
	username string
	password string
}

type nutanixProvider struct {
	providerKubectlClient ProviderKubectlClient
	clusterConfig         *v1alpha1.Cluster
	datacenterConfig      *v1alpha1.NutanixDatacenterConfig
	machineConfigs        map[string]*v1alpha1.NutanixMachineConfig
	templateBuilder       *NutanixTemplateBuilder
}

type ProviderKubectlClient interface {
	ApplyKubeSpecFromBytes(ctx context.Context, cluster *types.Cluster, data []byte) error
	CreateNamespaceIfNotPresent(ctx context.Context, kubeconfig string, namespace string) error
	LoadSecret(ctx context.Context, secretObject string, secretObjType string, secretObjectName string, kubeConfFile string) error
	GetEksaCluster(ctx context.Context, cluster *types.Cluster, clusterName string) (*v1alpha1.Cluster, error)
	GetEksaNutanixDatacenterConfig(ctx context.Context, nutanixDatacenterConfigName string, kubeconfigFile string, namespace string) (*v1alpha1.NutanixDatacenterConfig, error)
	GetEksaNutanixMachineConfig(ctx context.Context, nutanixMachineConfigName string, kubeconfigFile string, namespace string) (*v1alpha1.NutanixMachineConfig, error)
	GetKubeadmControlPlane(ctx context.Context, cluster *types.Cluster, clusterName string, opts ...executables.KubectlOpt) (*kubeadmv1beta1.KubeadmControlPlane, error)
	GetMachineDeployment(ctx context.Context, workerNodeGroupName string, opts ...executables.KubectlOpt) (*clusterv1.MachineDeployment, error)
	GetEtcdadmCluster(ctx context.Context, cluster *types.Cluster, clusterName string, opts ...executables.KubectlOpt) (*etcdv1beta1.EtcdadmCluster, error)
	GetSecretFromNamespace(ctx context.Context, kubeconfigFile, name, namespace string) (*corev1.Secret, error)
	UpdateAnnotation(ctx context.Context, resourceType, objectName string, annotations map[string]string, opts ...executables.KubectlOpt) error
	SearchNutanixMachineConfig(ctx context.Context, name string, kubeconfigFile string, namespace string) ([]*v1alpha1.NutanixMachineConfig, error)
	SearchNutanixDatacenterConfig(ctx context.Context, name string, kubeconfigFile string, namespace string) ([]*v1alpha1.NutanixDatacenterConfig, error)
	DeleteEksaNutanixDatacenterConfig(ctx context.Context, nutanixDatacenterConfigName string, kubeconfigFile string, namespace string) error
	DeleteEksaNutanixMachineConfig(ctx context.Context, nutanixMachineConfigName string, kubeconfigFile string, namespace string) error
	SetEksaControllerEnvVar(ctx context.Context, envVar, envVarVal, kubeconfig string) error
}

func NewProvider(
	datacenterConfig *v1alpha1.NutanixDatacenterConfig,
	machineConfigs map[string]*v1alpha1.NutanixMachineConfig,
	clusterConfig *v1alpha1.Cluster,
	providerKubectlClient ProviderKubectlClient,
	now types.NowFunc,
) *nutanixProvider {
	var controlPlaneMachineSpec, etcdMachineSpec *v1alpha1.NutanixMachineConfigSpec
	if clusterConfig.Spec.ControlPlaneConfiguration.MachineGroupRef != nil && machineConfigs[clusterConfig.Spec.ControlPlaneConfiguration.MachineGroupRef.Name] != nil {
		controlPlaneMachineSpec = &machineConfigs[clusterConfig.Spec.ControlPlaneConfiguration.MachineGroupRef.Name].Spec
	}

	workerNodeGroupMachineSpecs := make(map[string]v1alpha1.NutanixMachineConfigSpec, len(machineConfigs))

	if clusterConfig.Spec.ExternalEtcdConfiguration != nil {
		if clusterConfig.Spec.ExternalEtcdConfiguration.MachineGroupRef != nil && machineConfigs[clusterConfig.Spec.ExternalEtcdConfiguration.MachineGroupRef.Name] != nil {
			etcdMachineSpec = &machineConfigs[clusterConfig.Spec.ExternalEtcdConfiguration.MachineGroupRef.Name].Spec
		}
	}
	creds := getCredsFromEnv()
	templateBuilder := NewNutanixTemplateBuilder(&datacenterConfig.Spec, controlPlaneMachineSpec, etcdMachineSpec, workerNodeGroupMachineSpecs, creds, now).(*NutanixTemplateBuilder)
	return &nutanixProvider{
		clusterConfig:         clusterConfig,
		datacenterConfig:      datacenterConfig,
		machineConfigs:        machineConfigs,
		providerKubectlClient: providerKubectlClient,
		templateBuilder:       templateBuilder,
	}
}
func (p *nutanixProvider) BootstrapClusterOpts(_ *cluster.Spec) ([]bootstrapper.BootstrapClusterOption, error) {
	return nil, nil
}

func (p *nutanixProvider) BootstrapSetup(ctx context.Context, clusterConfig *v1alpha1.Cluster, cluster *types.Cluster) error {
	// TODO: figure out if we need something else here
	return nil
}

func (p *nutanixProvider) PostBootstrapSetup(ctx context.Context, clusterConfig *v1alpha1.Cluster, cluster *types.Cluster) error {
	// TODO: figure out if we need something else here
	return nil
}

func (p *nutanixProvider) PostBootstrapDeleteForUpgrade(ctx context.Context) error {
	return nil
}

func (p *nutanixProvider) PostBootstrapSetupUpgrade(ctx context.Context, clusterConfig *v1alpha1.Cluster, cluster *types.Cluster) error {
	// TODO: figure out if we need something else here
	return nil
}

func (p *nutanixProvider) PostWorkloadInit(ctx context.Context, cluster *types.Cluster, clusterSpec *cluster.Spec) error {
	return nil
}

func (p *nutanixProvider) Name() string {
	return constants.NutanixProviderName
}

func (p *nutanixProvider) DatacenterResourceType() string {
	return eksaNutanixDatacenterResourceType
}

func (p *nutanixProvider) MachineResourceType() string {
	return eksaNutanixMachineResourceType
}

func (p *nutanixProvider) DeleteResources(ctx context.Context, clusterSpec *cluster.Spec) error {
	for _, mc := range p.machineConfigs {
		if err := p.providerKubectlClient.DeleteEksaNutanixMachineConfig(ctx, mc.Name, clusterSpec.ManagementCluster.KubeconfigFile, mc.Namespace); err != nil {
			return err
		}
	}
	return p.providerKubectlClient.DeleteEksaNutanixDatacenterConfig(ctx, clusterSpec.NutanixDatacenter.Name, clusterSpec.ManagementCluster.KubeconfigFile, clusterSpec.NutanixDatacenter.Namespace)
}

func (p *nutanixProvider) PostClusterDeleteValidate(ctx context.Context, managementCluster *types.Cluster) error {
	// TODO:
	return nil
}

func (p *nutanixProvider) SetupAndValidateCreateCluster(ctx context.Context, clusterSpec *cluster.Spec) error {
	logger.Info("Warning: The nutanix infrastructure provider is still in development and should not be used in production")
	if err := setupEnvVars(p.datacenterConfig); err != nil {
		return fmt.Errorf("failed setup and validations: %v", err)
	}

	// TODO
	return nil
}

func (p *nutanixProvider) SetupAndValidateDeleteCluster(ctx context.Context, cluster *types.Cluster) error {
	if err := setupEnvVars(p.datacenterConfig); err != nil {
		return fmt.Errorf("failed setup and validations: %v", err)
	}

	// TODO
	return nil
}

func (p *nutanixProvider) SetupAndValidateUpgradeCluster(ctx context.Context, _ *types.Cluster, _ *cluster.Spec, _ *cluster.Spec) error {
	if err := setupEnvVars(p.datacenterConfig); err != nil {
		return fmt.Errorf("failed setup and validations: %v", err)
	}

	return nil
}

func (p *nutanixProvider) UpdateSecrets(ctx context.Context, cluster *types.Cluster) error {
	// TODO: implement
	return nil
}

type NutanixTemplateBuilder struct {
	datacenterSpec              *v1alpha1.NutanixDatacenterConfigSpec
	controlPlaneMachineSpec     *v1alpha1.NutanixMachineConfigSpec
	etcdMachineSpec             *v1alpha1.NutanixMachineConfigSpec
	workerNodeGroupMachineSpecs map[string]v1alpha1.NutanixMachineConfigSpec
	creds                       *basicAuthCreds
	now                         types.NowFunc
}

func NewNutanixTemplateBuilder(
	datacenterSpec *v1alpha1.NutanixDatacenterConfigSpec,
	controlPlaneMachineSpec,
	etcdMachineSpec *v1alpha1.NutanixMachineConfigSpec,
	workerNodeGroupMachineSpecs map[string]v1alpha1.NutanixMachineConfigSpec,
	creds *basicAuthCreds,
	now types.NowFunc) providers.TemplateBuilder {
	return &NutanixTemplateBuilder{
		datacenterSpec:              datacenterSpec,
		controlPlaneMachineSpec:     controlPlaneMachineSpec,
		etcdMachineSpec:             etcdMachineSpec,
		workerNodeGroupMachineSpecs: workerNodeGroupMachineSpecs,
		creds:                       creds,
		now:                         now,
	}
}

func (ntb *NutanixTemplateBuilder) WorkerMachineTemplateName(clusterName string) string {
	t := ntb.now().UnixNano() / int64(time.Millisecond)
	return fmt.Sprintf("%s-worker-node-template-%d", clusterName, t)
}

func (ntb *NutanixTemplateBuilder) CPMachineTemplateName(clusterName string) string {
	t := ntb.now().UnixNano() / int64(time.Millisecond)
	return fmt.Sprintf("%s-control-plane-template-%d", clusterName, t)
}

func (ntb *NutanixTemplateBuilder) EtcdMachineTemplateName(clusterName string) string {
	t := ntb.now().UnixNano() / int64(time.Millisecond)
	return fmt.Sprintf("%s-etcd-template-%d", clusterName, t)
}

func (ntb *NutanixTemplateBuilder) GenerateCAPISpecControlPlane(clusterSpec *cluster.Spec, buildOptions ...providers.BuildMapOption) (content []byte, err error) {
	var etcdMachineSpec v1alpha1.NutanixMachineConfigSpec
	if clusterSpec.Cluster.Spec.ExternalEtcdConfiguration != nil {
		etcdMachineSpec = *ntb.etcdMachineSpec
	}
	values := buildTemplateMapCP(ntb.datacenterSpec, clusterSpec, *ntb.controlPlaneMachineSpec, etcdMachineSpec, ntb.creds)

	for _, buildOption := range buildOptions {
		buildOption(values)
	}

	bytes, err := templater.Execute(defaultCAPIConfigCP, values)
	if err != nil {
		return nil, err
	}

	return bytes, nil
}

func (ntb *NutanixTemplateBuilder) GenerateCAPISpecWorkers(clusterSpec *cluster.Spec, workloadTemplateNames, kubeadmconfigTemplateNames map[string]string) (content []byte, err error) {
	workerSpecs := make([][]byte, 0, len(clusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations))
	for _, workerNodeGroupConfiguration := range clusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations {
		values := buildTemplateMapMD(clusterSpec, ntb.workerNodeGroupMachineSpecs[workerNodeGroupConfiguration.MachineGroupRef.Name], workerNodeGroupConfiguration)
		values["workloadTemplateName"] = workloadTemplateNames[workerNodeGroupConfiguration.Name]
		values["workloadkubeadmconfigTemplateName"] = kubeadmconfigTemplateNames[workerNodeGroupConfiguration.Name]

		bytes, err := templater.Execute(defaultClusterConfigMD, values)
		if err != nil {
			return nil, err
		}
		workerSpecs = append(workerSpecs, bytes)
	}

	return templater.AppendYamlResources(workerSpecs...), nil
}

func (p *nutanixProvider) generateCAPISpecForCreate(ctx context.Context, cluster *types.Cluster, clusterSpec *cluster.Spec) (controlPlaneSpec, workersSpec []byte, err error) {
	clusterName := clusterSpec.Cluster.Name

	cpOpt := func(values map[string]interface{}) {
		values["controlPlaneTemplateName"] = common.CPMachineTemplateName(clusterName, p.templateBuilder.now)
		values["etcdTemplateName"] = common.EtcdMachineTemplateName(clusterName, p.templateBuilder.now)
	}
	controlPlaneSpec, err = p.templateBuilder.GenerateCAPISpecControlPlane(clusterSpec, cpOpt)
	if err != nil {
		return nil, nil, err
	}

	workloadTemplateNames := make(map[string]string, len(clusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations))
	kubeadmconfigTemplateNames := make(map[string]string, len(clusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations))
	for _, workerNodeGroupConfiguration := range clusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations {
		workloadTemplateNames[workerNodeGroupConfiguration.Name] = common.WorkerMachineTemplateName(clusterSpec.Cluster.Name, workerNodeGroupConfiguration.Name, p.templateBuilder.now)
		kubeadmconfigTemplateNames[workerNodeGroupConfiguration.Name] = common.KubeadmConfigTemplateName(clusterSpec.Cluster.Name, workerNodeGroupConfiguration.Name, p.templateBuilder.now)
		p.templateBuilder.workerNodeGroupMachineSpecs[workerNodeGroupConfiguration.MachineGroupRef.Name] = p.machineConfigs[workerNodeGroupConfiguration.MachineGroupRef.Name].Spec
	}
	workersSpec, err = p.templateBuilder.GenerateCAPISpecWorkers(clusterSpec, workloadTemplateNames, kubeadmconfigTemplateNames)
	if err != nil {
		return nil, nil, err
	}
	return controlPlaneSpec, workersSpec, nil
}

func (p *nutanixProvider) GenerateCAPISpecForCreate(ctx context.Context, cluster *types.Cluster, clusterSpec *cluster.Spec) (controlPlaneSpec, workersSpec []byte, err error) {
	return p.generateCAPISpecForCreate(ctx, cluster, clusterSpec)
}

func NeedsNewControlPlaneTemplate(oldSpec, newSpec *cluster.Spec, oldNdc, newNdc *v1alpha1.NutanixDatacenterConfig, oldNmc, newNmc *v1alpha1.NutanixMachineConfig) bool {
	// Another option is to generate MachineTemplates based on the old and new eksa spec,
	// remove the name field and compare them with DeepEqual
	// We plan to approach this way since it's more flexible to add/remove fields and test out for validation
	if oldSpec.Cluster.Spec.KubernetesVersion != newSpec.Cluster.Spec.KubernetesVersion {
		return true
	}
	if oldSpec.Cluster.Spec.ControlPlaneConfiguration.Endpoint.Host != newSpec.Cluster.Spec.ControlPlaneConfiguration.Endpoint.Host {
		return true
	}
	if oldSpec.Bundles.Spec.Number != newSpec.Bundles.Spec.Number {
		return true
	}
	return AnyImmutableFieldChanged(oldNdc, newNdc, oldNmc, newNmc)
}

func AnyImmutableFieldChanged(oldNdc, newNdc *v1alpha1.NutanixDatacenterConfig, oldNmc, newNmc *v1alpha1.NutanixMachineConfig) bool {
	if oldNmc.Spec.Image != newNmc.Spec.Image {
		return true
	}
	if oldNmc.Spec.MemorySize != newNmc.Spec.MemorySize {
		return true
	}
	if oldNmc.Spec.SystemDiskSize != newNmc.Spec.SystemDiskSize {
		return true
	}
	if oldNmc.Spec.VCPUSockets != newNmc.Spec.VCPUSockets {
		return true
	}
	if oldNmc.Spec.VCPUsPerSocket != newNmc.Spec.VCPUsPerSocket {
		return true
	}
	if oldNmc.Spec.Cluster != newNmc.Spec.Cluster {
		return true
	}
	if oldNmc.Spec.Subnet != newNmc.Spec.Subnet {
		return true
	}
	if oldNmc.Spec.OSFamily != newNmc.Spec.OSFamily {
		return true
	}

	return false
}

func (p *nutanixProvider) getWorkerNodeMachineConfigs(ctx context.Context, workloadCluster *types.Cluster, newClusterSpec *cluster.Spec, workerNodeGroupConfiguration v1alpha1.WorkerNodeGroupConfiguration, prevWorkerNodeGroupConfigs map[string]v1alpha1.WorkerNodeGroupConfiguration) (*v1alpha1.NutanixMachineConfig, *v1alpha1.NutanixMachineConfig, error) {
	if _, ok := prevWorkerNodeGroupConfigs[workerNodeGroupConfiguration.Name]; ok {
		oldWorkerMachineConfig := p.machineConfigs[workerNodeGroupConfiguration.MachineGroupRef.Name]
		newWorkerMachineConfig, err := p.providerKubectlClient.GetEksaNutanixMachineConfig(ctx, workerNodeGroupConfiguration.MachineGroupRef.Name, workloadCluster.KubeconfigFile, newClusterSpec.Cluster.Namespace)
		if err != nil {
			return oldWorkerMachineConfig, nil, err
		}
		return oldWorkerMachineConfig, newWorkerMachineConfig, nil
	}
	return nil, nil, nil
}

func (p *nutanixProvider) needsNewMachineTemplate(currentSpec, newClusterSpec *cluster.Spec, workerNodeGroupConfiguration v1alpha1.WorkerNodeGroupConfiguration, ndc *v1alpha1.NutanixDatacenterConfig, prevWorkerNodeGroupConfigs map[string]v1alpha1.WorkerNodeGroupConfiguration, oldWorkerMachineConfig *v1alpha1.NutanixMachineConfig, newWorkerMachineConfig *v1alpha1.NutanixMachineConfig) (bool, error) {
	if _, ok := prevWorkerNodeGroupConfigs[workerNodeGroupConfiguration.Name]; ok {
		needsNewWorkloadTemplate := NeedsNewWorkloadTemplate(currentSpec, newClusterSpec, ndc, p.datacenterConfig, oldWorkerMachineConfig, newWorkerMachineConfig)
		return needsNewWorkloadTemplate, nil
	}
	return true, nil
}

func (p *nutanixProvider) needsNewKubeadmConfigTemplate(workerNodeGroupConfiguration v1alpha1.WorkerNodeGroupConfiguration, prevWorkerNodeGroupConfigs map[string]v1alpha1.WorkerNodeGroupConfiguration, oldWorkerNodeNmc *v1alpha1.NutanixMachineConfig, newWorkerNodeNmc *v1alpha1.NutanixMachineConfig) (bool, error) {
	if _, ok := prevWorkerNodeGroupConfigs[workerNodeGroupConfiguration.Name]; ok {
		existingWorkerNodeGroupConfig := prevWorkerNodeGroupConfigs[workerNodeGroupConfiguration.Name]
		return NeedsNewKubeadmConfigTemplate(&workerNodeGroupConfiguration, &existingWorkerNodeGroupConfig, oldWorkerNodeNmc, newWorkerNodeNmc), nil
	}
	return true, nil
}

func NeedsNewWorkloadTemplate(oldSpec, newSpec *cluster.Spec, oldNdc, newNdc *v1alpha1.NutanixDatacenterConfig, oldNmc, newNmc *v1alpha1.NutanixMachineConfig) bool {
	if oldSpec.Cluster.Spec.KubernetesVersion != newSpec.Cluster.Spec.KubernetesVersion {
		return true
	}
	if oldSpec.Bundles.Spec.Number != newSpec.Bundles.Spec.Number {
		return true
	}
	if !v1alpha1.WorkerNodeGroupConfigurationSliceTaintsEqual(oldSpec.Cluster.Spec.WorkerNodeGroupConfigurations, newSpec.Cluster.Spec.WorkerNodeGroupConfigurations) ||
		!v1alpha1.WorkerNodeGroupConfigurationsLabelsMapEqual(oldSpec.Cluster.Spec.WorkerNodeGroupConfigurations, newSpec.Cluster.Spec.WorkerNodeGroupConfigurations) {
		return true
	}
	return AnyImmutableFieldChanged(oldNdc, newNdc, oldNmc, newNmc)
}

func NeedsNewKubeadmConfigTemplate(newWorkerNodeGroup *v1alpha1.WorkerNodeGroupConfiguration, oldWorkerNodeGroup *v1alpha1.WorkerNodeGroupConfiguration, oldWorkerNodeNmc *v1alpha1.NutanixMachineConfig, newWorkerNodeNmc *v1alpha1.NutanixMachineConfig) bool {
	return !v1alpha1.TaintsSliceEqual(newWorkerNodeGroup.Taints, oldWorkerNodeGroup.Taints) || !v1alpha1.LabelsMapEqual(newWorkerNodeGroup.Labels, oldWorkerNodeGroup.Labels) ||
		!v1alpha1.UsersSliceEqual(oldWorkerNodeNmc.Spec.Users, newWorkerNodeNmc.Spec.Users)
}

func (p *nutanixProvider) generateCAPISpecForUpgrade(ctx context.Context, bootstrapCluster, workloadCluster *types.Cluster, currentSpec, newClusterSpec *cluster.Spec) (controlPlaneSpec, workersSpec []byte, err error) {
	clusterName := newClusterSpec.Cluster.Name
	var controlPlaneTemplateName, workloadTemplateName, kubeadmconfigTemplateName, etcdTemplateName string

	// Get existing EKSA Cluster
	eksaCluster, err := p.providerKubectlClient.GetEksaCluster(ctx, workloadCluster, newClusterSpec.Cluster.Name)
	if err != nil {
		return nil, nil, err
	}

	// Get current Nutanix Datacenter Config
	ndc, err := p.providerKubectlClient.GetEksaNutanixDatacenterConfig(ctx, p.datacenterConfig.Name, workloadCluster.KubeconfigFile, newClusterSpec.Cluster.Namespace)
	if err != nil {
		return nil, nil, err
	}

	// Get current Nutanix Machine Config
	controlPlaneMachineConfig := p.machineConfigs[newClusterSpec.Cluster.Spec.ControlPlaneConfiguration.MachineGroupRef.Name]
	controlPlaneNutanixMachineConfig, err := p.providerKubectlClient.GetEksaNutanixMachineConfig(ctx,
		eksaCluster.Spec.ControlPlaneConfiguration.MachineGroupRef.Name,
		workloadCluster.KubeconfigFile,
		newClusterSpec.Cluster.Namespace)
	if err != nil {
		return nil, nil, err
	}
	needsNewControlPlaneTemplate := NeedsNewControlPlaneTemplate(currentSpec,
		newClusterSpec,
		ndc,
		p.datacenterConfig,
		controlPlaneNutanixMachineConfig,
		controlPlaneMachineConfig)
	if !needsNewControlPlaneTemplate {
		cp, err := p.providerKubectlClient.GetKubeadmControlPlane(ctx, workloadCluster, eksaCluster.Name, executables.WithCluster(bootstrapCluster), executables.WithNamespace(constants.EksaSystemNamespace))
		if err != nil {
			return nil, nil, err
		}
		controlPlaneTemplateName = cp.Spec.MachineTemplate.InfrastructureRef.Name
	} else {
		controlPlaneTemplateName = common.CPMachineTemplateName(clusterName, p.templateBuilder.now)
	}

	previousWorkerNodeGroupConfigs := cluster.BuildMapForWorkerNodeGroupsByName(currentSpec.Cluster.Spec.WorkerNodeGroupConfigurations)

	workloadTemplateNames := make(map[string]string, len(newClusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations))
	kubeadmconfigTemplateNames := make(map[string]string, len(newClusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations))
	for _, workerNodeGroupConfiguration := range newClusterSpec.Cluster.Spec.WorkerNodeGroupConfigurations {

		oldWorkerNodeNmc, newWorkerNodeNmc, err := p.getWorkerNodeMachineConfigs(ctx, workloadCluster, newClusterSpec, workerNodeGroupConfiguration, previousWorkerNodeGroupConfigs)
		if err != nil {
			return nil, nil, err
		}
		needsNewWorkloadTemplate, err := p.needsNewMachineTemplate(currentSpec, newClusterSpec, workerNodeGroupConfiguration, ndc, previousWorkerNodeGroupConfigs, oldWorkerNodeNmc, newWorkerNodeNmc)
		if err != nil {
			return nil, nil, err
		}
		needsNewKubeadmConfigTemplate, err := p.needsNewKubeadmConfigTemplate(workerNodeGroupConfiguration, previousWorkerNodeGroupConfigs, oldWorkerNodeNmc, newWorkerNodeNmc)
		if err != nil {
			return nil, nil, err
		}
		if !needsNewKubeadmConfigTemplate {
			mdName := machineDeploymentName(newClusterSpec.Cluster.Name, workerNodeGroupConfiguration.Name)
			md, err := p.providerKubectlClient.GetMachineDeployment(ctx, mdName, executables.WithCluster(bootstrapCluster), executables.WithNamespace(constants.EksaSystemNamespace))
			if err != nil {
				return nil, nil, err
			}
			kubeadmconfigTemplateName = md.Spec.Template.Spec.Bootstrap.ConfigRef.Name
			kubeadmconfigTemplateNames[workerNodeGroupConfiguration.Name] = kubeadmconfigTemplateName
		} else {
			kubeadmconfigTemplateName = common.KubeadmConfigTemplateName(clusterName, workerNodeGroupConfiguration.Name, p.templateBuilder.now)
			kubeadmconfigTemplateNames[workerNodeGroupConfiguration.Name] = kubeadmconfigTemplateName
		}

		if !needsNewWorkloadTemplate {
			mdName := machineDeploymentName(newClusterSpec.Cluster.Name, workerNodeGroupConfiguration.Name)
			md, err := p.providerKubectlClient.GetMachineDeployment(ctx, mdName, executables.WithCluster(bootstrapCluster), executables.WithNamespace(constants.EksaSystemNamespace))
			if err != nil {
				return nil, nil, err
			}
			workloadTemplateName = md.Spec.Template.Spec.InfrastructureRef.Name
			workloadTemplateNames[workerNodeGroupConfiguration.Name] = workloadTemplateName
		} else {
			workloadTemplateName = common.WorkerMachineTemplateName(clusterName, workerNodeGroupConfiguration.Name, p.templateBuilder.now)
			workloadTemplateNames[workerNodeGroupConfiguration.Name] = workloadTemplateName
		}
		p.templateBuilder.workerNodeGroupMachineSpecs[workerNodeGroupConfiguration.MachineGroupRef.Name] = p.machineConfigs[workerNodeGroupConfiguration.MachineGroupRef.Name].Spec
	}

	cpOpt := func(values map[string]interface{}) {
		values["controlPlaneTemplateName"] = controlPlaneTemplateName
		values["etcdTemplateName"] = etcdTemplateName
	}
	controlPlaneSpec, err = p.templateBuilder.GenerateCAPISpecControlPlane(newClusterSpec, cpOpt)
	if err != nil {
		return nil, nil, err
	}

	workersSpec, err = p.templateBuilder.GenerateCAPISpecWorkers(newClusterSpec, workloadTemplateNames, kubeadmconfigTemplateNames)
	if err != nil {
		return nil, nil, err
	}
	return controlPlaneSpec, workersSpec, nil
}

func (p *nutanixProvider) GenerateCAPISpecForUpgrade(ctx context.Context, bootstrapCluster, workloadCluster *types.Cluster, currentSpec, newClusterSpec *cluster.Spec) (controlPlaneSpec, workersSpec []byte, err error) {
	return p.generateCAPISpecForUpgrade(ctx, bootstrapCluster, workloadCluster, currentSpec, newClusterSpec)
}

func (p *nutanixProvider) GenerateStorageClass() []byte {
	// TODO: determine if we need something else here
	return nil
}

func (p *nutanixProvider) GenerateMHC(clusterSpec *cluster.Spec) ([]byte, error) {
	data := map[string]string{
		"clusterName":         p.clusterConfig.Name,
		"eksaSystemNamespace": constants.EksaSystemNamespace,
	}
	mhc, err := templater.Execute(string(mhcTemplate), data)
	if err != nil {
		return nil, err
	}

	return mhc, nil
}

func (p *nutanixProvider) UpdateKubeConfig(content *[]byte, clusterName string) error {
	// TODO: Figure out if something is needed here
	return nil
}

func (p *nutanixProvider) machineConfigsSpecChanged(ctx context.Context, cc *v1alpha1.Cluster, cluster *types.Cluster, newClusterSpec *cluster.Spec) (bool, error) {
	machineConfigMap := make(map[string]*v1alpha1.NutanixMachineConfig)
	for _, config := range p.MachineConfigs(nil) {
		mc := config.(*v1alpha1.NutanixMachineConfig)
		machineConfigMap[mc.Name] = mc
	}

	for _, oldMcRef := range cc.MachineConfigRefs() {
		existingVmc, err := p.providerKubectlClient.GetEksaNutanixMachineConfig(ctx, oldMcRef.Name, cluster.KubeconfigFile, newClusterSpec.Cluster.Namespace)
		if err != nil {
			return false, err
		}
		csmc, ok := machineConfigMap[oldMcRef.Name]
		if !ok {
			logger.V(3).Info(fmt.Sprintf("Old machine config spec %s not found in the existing spec", oldMcRef.Name))
			return true, nil
		}
		if !reflect.DeepEqual(existingVmc.Spec, csmc.Spec) {
			logger.V(3).Info(fmt.Sprintf("New machine config spec %s is different from the existing spec", oldMcRef.Name))
			return true, nil
		}
	}

	return false, nil
}

func (p *nutanixProvider) Version(clusterSpec *cluster.Spec) string {
	return clusterSpec.VersionsBundle.Nutanix.Version
}

func (p *nutanixProvider) EnvMap(_ *cluster.Spec) (map[string]string, error) {
	// TODO: determine if any env vars are needed and add them to requiredEnvs
	envMap := make(map[string]string)
	for _, key := range requiredEnvs {
		if env, ok := os.LookupEnv(key); ok && len(env) > 0 {
			envMap[key] = env
		} else {
			return envMap, fmt.Errorf("warning required env not set %s", key)
		}
	}
	return envMap, nil
}

func (p *nutanixProvider) GetDeployments() map[string][]string {
	return map[string][]string{
		"capx-system": {"capx-controller-manager"},
	}
}

func (p *nutanixProvider) GetInfrastructureBundle(clusterSpec *cluster.Spec) *types.InfrastructureBundle {
	bundle := clusterSpec.VersionsBundle
	folderName := fmt.Sprintf("infrastructure-nutanix/%s/", bundle.Nutanix.Version)

	infraBundle := types.InfrastructureBundle{
		FolderName: folderName,
		Manifests: []releasev1alpha1.Manifest{
			bundle.Nutanix.Components,
			bundle.Nutanix.Metadata,
			bundle.Nutanix.ClusterTemplate,
		},
	}
	return &infraBundle
}

func (p *nutanixProvider) DatacenterConfig(_ *cluster.Spec) providers.DatacenterConfig {
	return p.datacenterConfig
}

func (p *nutanixProvider) MachineConfigs(_ *cluster.Spec) []providers.MachineConfig {
	configs := make(map[string]providers.MachineConfig, len(p.machineConfigs))
	controlPlaneMachineName := p.clusterConfig.Spec.ControlPlaneConfiguration.MachineGroupRef.Name
	p.machineConfigs[controlPlaneMachineName].Annotations = map[string]string{p.clusterConfig.ControlPlaneAnnotation(): "true"}
	if p.clusterConfig.IsManaged() {
		p.machineConfigs[controlPlaneMachineName].SetManagedBy(p.clusterConfig.ManagedBy())
	}
	configs[controlPlaneMachineName] = p.machineConfigs[controlPlaneMachineName]

	if p.clusterConfig.Spec.ExternalEtcdConfiguration != nil {
		etcdMachineName := p.clusterConfig.Spec.ExternalEtcdConfiguration.MachineGroupRef.Name
		p.machineConfigs[etcdMachineName].Annotations = map[string]string{p.clusterConfig.EtcdAnnotation(): "true"}
		if etcdMachineName != controlPlaneMachineName {
			configs[etcdMachineName] = p.machineConfigs[etcdMachineName]
			if p.clusterConfig.IsManaged() {
				p.machineConfigs[etcdMachineName].SetManagedBy(p.clusterConfig.ManagedBy())
			}
		}
	}

	for _, workerNodeGroupConfiguration := range p.clusterConfig.Spec.WorkerNodeGroupConfigurations {
		workerMachineName := workerNodeGroupConfiguration.MachineGroupRef.Name
		if _, ok := configs[workerMachineName]; !ok {
			configs[workerMachineName] = p.machineConfigs[workerMachineName]
			if p.clusterConfig.IsManaged() {
				p.machineConfigs[workerMachineName].SetManagedBy(p.clusterConfig.ManagedBy())
			}
		}
	}
	return configsMapToSlice(configs)
}

func configsMapToSlice(c map[string]providers.MachineConfig) []providers.MachineConfig {
	configs := make([]providers.MachineConfig, 0, len(c))
	for _, config := range c {
		configs = append(configs, config)
	}

	return configs
}

func (p *nutanixProvider) ValidateNewSpec(_ context.Context, _ *types.Cluster, _ *cluster.Spec) error {
	// TODO: Figure out if something is needed here
	return nil
}

func (p *nutanixProvider) ChangeDiff(currentSpec, newSpec *cluster.Spec) *types.ComponentChangeDiff {
	// TODO: implement
	return nil
}

func (p *nutanixProvider) RunPostControlPlaneUpgrade(ctx context.Context, oldClusterSpec *cluster.Spec, clusterSpec *cluster.Spec, workloadCluster *types.Cluster, managementCluster *types.Cluster) error {
	// TODO: Figure out if something is needed here
	return nil
}

func (p *nutanixProvider) UpgradeNeeded(ctx context.Context, newSpec, currentSpec *cluster.Spec, cluster *types.Cluster) (bool, error) {
	cc := currentSpec.Cluster
	existingVdc, err := p.providerKubectlClient.GetEksaNutanixDatacenterConfig(ctx, cc.Spec.DatacenterRef.Name, cluster.KubeconfigFile, newSpec.Cluster.Namespace)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(existingVdc.Spec, p.datacenterConfig.Spec) {
		logger.V(3).Info("New provider spec is different from the new spec")
		return true, nil
	}

	machineConfigsSpecChanged, err := p.machineConfigsSpecChanged(ctx, cc, cluster, newSpec)
	if err != nil {
		return false, err
	}
	return machineConfigsSpecChanged, nil
}

func (p *nutanixProvider) RunPostControlPlaneCreation(ctx context.Context, clusterSpec *cluster.Spec, cluster *types.Cluster) error {
	// TODO: Figure out if something is needed here
	return nil
}

func machineDeploymentName(clusterName, nodeGroupName string) string {
	return fmt.Sprintf("%s-%s", clusterName, nodeGroupName)
}

func buildTemplateMapCP(
	datacenterSpec *v1alpha1.NutanixDatacenterConfigSpec,
	clusterSpec *cluster.Spec,
	controlPlaneMachineSpec v1alpha1.NutanixMachineConfigSpec,
	etcdMachineSpec v1alpha1.NutanixMachineConfigSpec,
	creds *basicAuthCreds) map[string]interface{} {
	bundle := clusterSpec.VersionsBundle
	format := "cloud-config"

	values := map[string]interface{}{
		"clusterName":                  clusterSpec.Cluster.Name,
		"controlPlaneEndpointIp":       clusterSpec.Cluster.Spec.ControlPlaneConfiguration.Endpoint.Host,
		"controlPlaneReplicas":         clusterSpec.Cluster.Spec.ControlPlaneConfiguration.Count,
		"controlPlaneSshAuthorizedKey": controlPlaneMachineSpec.Users[0].SshAuthorizedKeys[0],
		"controlPlaneSshUsername":      controlPlaneMachineSpec.Users[0].Name,
		"eksaSystemNamespace":          constants.EksaSystemNamespace,
		"format":                       format,
		"podCidrs":                     clusterSpec.Cluster.Spec.ClusterNetwork.Pods.CidrBlocks,
		"serviceCidrs":                 clusterSpec.Cluster.Spec.ClusterNetwork.Services.CidrBlocks,
		"kubernetesVersion":            bundle.KubeDistro.Kubernetes.Tag,
		"kubernetesRepository":         bundle.KubeDistro.Kubernetes.Repository,
		"corednsRepository":            bundle.KubeDistro.CoreDNS.Repository,
		"corednsVersion":               bundle.KubeDistro.CoreDNS.Tag,
		"etcdRepository":               bundle.KubeDistro.Etcd.Repository,
		"etcdImageTag":                 bundle.KubeDistro.Etcd.Tag,
		"kubeVipImage":                 "ghcr.io/kube-vip/kube-vip:latest",
		"externalEtcdVersion":          bundle.KubeDistro.EtcdVersion,
		"etcdCipherSuites":             crypto.SecureCipherSuitesString(),
		"nutanixEndpoint":              datacenterSpec.Endpoint,
		"nutanixPort":                  datacenterSpec.Port,
		"nutanixAdditionalTrustBundle": datacenterSpec.AdditionalTrustBundle,
		"nutanixUser":                  creds.username,
		"nutanixPassword":              creds.password,
		"vcpusPerSocket":               controlPlaneMachineSpec.VCPUsPerSocket,
		"vcpuSockets":                  controlPlaneMachineSpec.VCPUSockets,
		"memorySize":                   controlPlaneMachineSpec.MemorySize.String(),
		"systemDiskSize":               controlPlaneMachineSpec.SystemDiskSize.String(),
		"imageName":                    controlPlaneMachineSpec.Image.Name,   //TODO pass name or uuid based on type of identifier
		"nutanixPEClusterName":         controlPlaneMachineSpec.Cluster.Name, //TODO pass name or uuid based on type of identifier
		"subnetName":                   controlPlaneMachineSpec.Subnet.Name,  //TODO pass name or uuid based on type of identifier
	}

	if clusterSpec.Cluster.Spec.ExternalEtcdConfiguration != nil {
		values["externalEtcd"] = true
		values["externalEtcdReplicas"] = clusterSpec.Cluster.Spec.ExternalEtcdConfiguration.Count
		values["etcdSshUsername"] = etcdMachineSpec.Users[0].Name
	}

	return values
}

func buildTemplateMapMD(clusterSpec *cluster.Spec, workerNodeGroupMachineSpec v1alpha1.NutanixMachineConfigSpec, workerNodeGroupConfiguration v1alpha1.WorkerNodeGroupConfiguration) map[string]interface{} {
	bundle := clusterSpec.VersionsBundle
	format := "cloud-config"

	values := map[string]interface{}{
		"clusterName":            clusterSpec.Cluster.Name,
		"eksaSystemNamespace":    constants.EksaSystemNamespace,
		"format":                 format,
		"kubernetesVersion":      bundle.KubeDistro.Kubernetes.Tag,
		"workerReplicas":         workerNodeGroupConfiguration.Count,
		"workerPoolName":         "md-0",
		"workerSshAuthorizedKey": workerNodeGroupMachineSpec.Users[0].SshAuthorizedKeys[0],
		"workerSshUsername":      workerNodeGroupMachineSpec.Users[0].Name,
		"vcpusPerSocket":         workerNodeGroupMachineSpec.VCPUsPerSocket,
		"vcpuSockets":            workerNodeGroupMachineSpec.VCPUSockets,
		"memorySize":             workerNodeGroupMachineSpec.MemorySize.String(),
		"systemDiskSize":         workerNodeGroupMachineSpec.SystemDiskSize.String(),
		"imageName":              workerNodeGroupMachineSpec.Image.Name,   //TODO pass name or uuid based on type of identifier
		"nutanixPEClusterName":   workerNodeGroupMachineSpec.Cluster.Name, //TODO pass name or uuid based on type of identifier
		"subnetName":             workerNodeGroupMachineSpec.Subnet.Name,  //TODO pass name or uuid based on type of identifier
		"workerNodeGroupName":    fmt.Sprintf("%s-%s", clusterSpec.Cluster.Name, workerNodeGroupConfiguration.Name),
	}
	return values
}

func (p *nutanixProvider) MachineDeploymentsToDelete(workloadCluster *types.Cluster, currentSpec, newSpec *cluster.Spec) []string {
	nodeGroupsToDelete := cluster.NodeGroupsToDelete(currentSpec, newSpec)
	machineDeployments := make([]string, 0, len(nodeGroupsToDelete))
	for _, nodeGroup := range nodeGroupsToDelete {
		mdName := machineDeploymentName(workloadCluster.Name, nodeGroup.Name)
		machineDeployments = append(machineDeployments, mdName)
	}
	return machineDeployments
}

func (p *nutanixProvider) InstallCustomProviderComponents(ctx context.Context, kubeconfigFile string) error {
	if err := p.providerKubectlClient.SetEksaControllerEnvVar(ctx, features.NutanixProviderEnvVar, "true", kubeconfigFile); err != nil {
		return err
	}
	return nil
}

func (p *nutanixProvider) PreCAPIInstallOnBootstrap(ctx context.Context, cluster *types.Cluster, clusterSpec *cluster.Spec) error {
	return nil
}
