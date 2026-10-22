package crmcli

import "github.com/spf13/cobra"

// Distribution-owned commands retain their existing independent authorization.
// The V4 shared core remains byte-for-byte reproducible after import remapping.
func init() {
	distributionCommands = append(distributionCommands, func(root *cobra.Command, o *Options) {
		root.AddCommand(docsCommands(o))
		for _, c := range root.Commands() {
			if c.Name() == "schema" {
				c.AddCommand(&cobra.Command{Use: "webhook", Args: cobra.NoArgs, Short: "signed Group Ops webhook contract", RunE: func(*cobra.Command, []string) error { return o.print(groupOpsWebhookSchema()) }})
			}
			if c.Name() == "group-ops" {
				c.AddCommand(groupOpsWebhookCommands(o))
			}
		}
	})
}
