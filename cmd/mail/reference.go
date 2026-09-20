package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/reference"
)

var referenceCmd = &cobra.Command{
	Use:   "reference",
	Short: "Print the full reference documentation",
	Long: `Print the complete reference guide: commands, output schema, exit
codes, gotchas, and recipes for driving this tool from scripts and LLM
agents. It is embedded in the binary, so it is always in sync with the
version you are running.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), reference.Markdown())
		return err
	},
}

func init() {
	rootCmd.AddCommand(referenceCmd)
}
