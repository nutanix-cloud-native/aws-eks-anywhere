package cmd

import "github.com/spf13/cobra"

var nutanixCmd = &cobra.Command{
	Use:   "nutanix",
	Short: "Utility nutanix operations",
	Long:  "Use eksctl anywhere nutanix to perform utility operations on nutanix",
}

func init() {
	expCmd.AddCommand(nutanixCmd)
}
