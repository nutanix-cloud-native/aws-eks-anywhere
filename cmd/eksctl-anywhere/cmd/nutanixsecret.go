package cmd

import (
	"encoding/base64"
	"encoding/json"
	"github.com/aws/eks-anywhere/pkg/templater"
	"github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	"github.com/spf13/cobra"
	"os"
)

var nutanixSecretCmd = &cobra.Command{
	Use:   "create-secret",
	Short: "Create a secret for nutanix",
	Long:  "Use eksctl anywhere nutanix create-secret to create a secret for nutanix",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds := credentials.BasicAuthCredential{
			PrismCentral: credentials.PrismCentralBasicAuth{
				BasicAuth: credentials.BasicAuth{
					Username: createSecretOptions.username,
					Password: createSecretOptions.password,
				},
			},
		}
		encodedCreds, err := json.Marshal(creds)
		if err != nil {
			return err
		}

		credsJSON, err := json.Marshal([]credentials.Credential{{
			Type: credentials.BasicAuthCredentialType,
			Data: encodedCreds,
		}})
		if err != nil {
			return err
		}

		template := `apiVersion: v1
kind: Secret
metadata:
  name: "{{.secretName}}"
  namespace: eksa-system
data:
  credentials: "{{.base64EncodedCredentials}}"
`
		data := map[string]interface{}{
			"secretName":               createSecretOptions.secretname,
			"base64EncodedCredentials": base64.StdEncoding.EncodeToString(credsJSON),
		}
		bytes, err := templater.Execute(template, data)
		if err != nil {
			return err
		}
		filename := createSecretOptions.secretname + ".yaml"
		// write bytes into file with filename
		return os.WriteFile(filename, bytes, 0644)
	},
}

type nutanixSecretOptions struct {
	secretname string
	username   string
	password   string
}

var createSecretOptions = &nutanixSecretOptions{}

func init() {
	nutanixSecretCmd.Flags().StringVarP(&createSecretOptions.secretname, "name", "n", "", "Name of the secret; filename to persist the secret will be secretname.yaml")
	nutanixSecretCmd.Flags().StringVarP(&createSecretOptions.username, "username", "u", "", "Username for the prism central")
	nutanixSecretCmd.Flags().StringVarP(&createSecretOptions.password, "password", "p", "", "Password for the prism central")
	nutanixCmd.AddCommand(nutanixSecretCmd)
}
