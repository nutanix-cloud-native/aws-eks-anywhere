package v1alpha1

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// NutanixIdentifierType is an enumeration of different resource identifier types.
type NutanixIdentifierType string

func (c NutanixIdentifierType) String() string {
	return string(c)
}

const (
	// NutanixMachineConfigKind is the kind for a NutanixMachineConfig.
	NutanixMachineConfigKind = "NutanixMachineConfig"

	// NutanixIdentifierUUID is a resource identifier identifying the object by UUID.
	NutanixIdentifierUUID NutanixIdentifierType = "uuid"
	// NutanixIdentifierName is a resource identifier identifying the object by Name.
	NutanixIdentifierName NutanixIdentifierType = "name"

	defaultNutanixOSFamily         = Ubuntu
	defaultNutanixBootType         = "legacy"
	defaultNutanixSystemDiskSizeGi = "40Gi"
	defaultNutanixMemorySizeGi     = "4Gi"
	defaultNutanixVCPUsPerSocket   = 1
	defaultNutanixVCPUSockets      = 2

	// DefaultNutanixMachineConfigUser is the default username we set in machine config.
	DefaultNutanixMachineConfigUser string = "eksa"
)

// NutanixResourceIdentifier holds the identity of a Nutanix Prism resource (cluster, image, subnet, etc.)
//
// +union.
type NutanixResourceIdentifier struct {
	// Type is the identifier type to use for this resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=uuid;name
	Type NutanixIdentifierType `json:"type"`

	// uuid is the UUID of the resource in the PC.
	// +optional
	UUID *string `json:"uuid,omitempty"`

	// name is the resource name in the PC
	// +optional
	Name *string `json:"name,omitempty"`
}

// NutanixMachineConfigGenerateOpt is a functional option that can be passed to NewNutanixMachineConfigGenerate to
// customize the generated machine config
//
// +kubebuilder:object:generate=false
type NutanixMachineConfigGenerateOpt func(config *NutanixMachineConfigGenerate)

// NewNutanixMachineConfigGenerate returns a new instance of NutanixMachineConfigGenerate
// used for generating yaml for generate clusterconfig command.
func NewNutanixMachineConfigGenerate(name string, opts ...NutanixMachineConfigGenerateOpt) *NutanixMachineConfigGenerate {
	enterNameString := "<Enter %s name here>"
	machineConfig := &NutanixMachineConfigGenerate{
		TypeMeta: metav1.TypeMeta{
			Kind:       NutanixMachineConfigKind,
			APIVersion: SchemeBuilder.GroupVersion.String(),
		},
		ObjectMeta: ObjectMeta{
			Name: name,
		},
		Spec: NutanixMachineConfigSpec{
			OSFamily: defaultNutanixOSFamily,
			Users: []UserConfiguration{
				{
					Name:              DefaultNutanixMachineConfigUser,
					SshAuthorizedKeys: []string{"ssh-rsa AAAA..."},
				},
			},
			VCPUsPerSocket: defaultNutanixVCPUsPerSocket,
			VCPUSockets:    defaultNutanixVCPUSockets,
			MemorySize:     resource.MustParse(defaultNutanixMemorySizeGi),
			Image:          NutanixResourceIdentifier{Type: NutanixIdentifierName, Name: func() *string { s := fmt.Sprintf(enterNameString, "image"); return &s }()},
			Cluster:        NutanixResourceIdentifier{Type: NutanixIdentifierName, Name: func() *string { s := fmt.Sprintf(enterNameString, "Prism Element cluster"); return &s }()},
			Subnet:         NutanixResourceIdentifier{Type: NutanixIdentifierName, Name: func() *string { s := fmt.Sprintf(enterNameString, "subnet"); return &s }()},
			SystemDiskSize: resource.MustParse(defaultNutanixSystemDiskSizeGi),
		},
	}

	for _, opt := range opts {
		opt(machineConfig)
	}

	return machineConfig
}

func (c *NutanixMachineConfigGenerate) APIVersion() string {
	return c.TypeMeta.APIVersion
}

func (c *NutanixMachineConfigGenerate) Kind() string {
	return c.TypeMeta.Kind
}

func (c *NutanixMachineConfigGenerate) Name() string {
	return c.ObjectMeta.Name
}

func GetNutanixMachineConfigs(fileName string) (map[string]*NutanixMachineConfig, error) {
	configs := make(map[string]*NutanixMachineConfig)
	content, err := os.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("unable to read file due to: %v", err)
	}
	for _, c := range strings.Split(string(content), YamlSeparator) {
		config := NutanixMachineConfig{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{},
			},
		}
		if err = yaml.UnmarshalStrict([]byte(c), &config); err == nil {
			if config.Kind == NutanixMachineConfigKind {
				configs[config.Name] = &config
				continue
			}
		}
		_ = yaml.Unmarshal([]byte(c), &config) // this is to check if there is a bad spec in the file
		if config.Kind == NutanixMachineConfigKind {
			return nil, fmt.Errorf("unable to unmarshall content from file due to: %v", err)
		}
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("unable to find kind %v in file", NutanixMachineConfigKind)
	}
	return configs, nil
}

func setNutanixMachineConfigDefaults(machineConfig *NutanixMachineConfig) {
	initUser := UserConfiguration{
		Name:              DefaultNutanixMachineConfigUser,
		SshAuthorizedKeys: []string{""},
	}
	if machineConfig.Spec.Users == nil || len(machineConfig.Spec.Users) <= 0 {
		machineConfig.Spec.Users = []UserConfiguration{initUser}
	}

	user := machineConfig.Spec.Users[0]
	if user.Name == "" {
		machineConfig.Spec.Users[0].Name = DefaultNutanixMachineConfigUser
	}

	if user.SshAuthorizedKeys == nil || len(user.SshAuthorizedKeys) <= 0 {
		machineConfig.Spec.Users[0].SshAuthorizedKeys = []string{""}
	}

	if machineConfig.Spec.OSFamily == "" {
		machineConfig.Spec.OSFamily = defaultNutanixOSFamily
	}
}

func validateNutanixMachineConfig(c *NutanixMachineConfig) error {
	if err := validateObjectMeta(c.ObjectMeta); err != nil {
		return fmt.Errorf("NutanixMachineConfig: %v", err)
	}

	if c.Spec.Subnet.Type != NutanixIdentifierName && c.Spec.Subnet.Type != NutanixIdentifierUUID {
		return fmt.Errorf("NutanixMachineConfig: subnet type must be either name or uuid")
	}

	if c.Spec.Cluster.Type != NutanixIdentifierName && c.Spec.Cluster.Type != NutanixIdentifierUUID {
		return fmt.Errorf("NutanixMachineConfig: cluster type must be either name or uuid")
	}

	if c.Spec.Image.Type != NutanixIdentifierName && c.Spec.Image.Type != NutanixIdentifierUUID {
		return fmt.Errorf("NutanixMachineConfig: image type must be either name or uuid")
	}

	if c.Spec.VCPUSockets <= 0 {
		return fmt.Errorf("NutanixMachineConfig: vcpu sockets must be greater than 0")
	}

	if c.Spec.VCPUsPerSocket <= 0 {
		return fmt.Errorf("NutanixMachineConfig: vcpu per socket must be greater than 0")
	}

	if c.Spec.OSFamily != Ubuntu && c.Spec.OSFamily != Bottlerocket {
		return fmt.Errorf(
			"NutanixMachineConfig: unsupported spec.osFamily (%v); Please use one of the following: %s, %s",
			c.Spec.OSFamily,
			Ubuntu,
			Bottlerocket,
		)
	}

	if len(c.Spec.Users) <= 0 {
		return fmt.Errorf("TinkerbellMachineConfig: missing spec.Users: %s", c.Name)
	}

	return nil
}
