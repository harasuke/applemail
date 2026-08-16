package main

import (
	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/output"
)

var accountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "List configured Mail accounts",
	Long: `List the accounts configured in Mail: display name, email address(es),
and the identifier used by --account. Requires Automation permission for
your terminal (see "mail doctor --check-automation").`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		accounts, err := listAccounts()
		if err != nil {
			return err
		}
		for _, a := range accounts {
			if err := renderer.Write(output.Account{
				Name:   a.Name,
				ID:     a.ID,
				Emails: a.Emails,
			}); err != nil {
				return err
			}
		}
		return renderer.Close()
	},
}

func init() {
	rootCmd.AddCommand(accountsCmd)
}
