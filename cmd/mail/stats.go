package main

import (
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Summarize a set of messages",
	Long: `Emit one aggregate record describing the matching messages:
top senders, link domains, volume by week, read and flag counts, and
thread count.

This is the view for reasoning about many messages together rather than
one at a time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScan(cmd, "", true)
	},
}

func init() {
	addFilterFlags(statsCmd, 0)
	rootCmd.AddCommand(statsCmd)
}
