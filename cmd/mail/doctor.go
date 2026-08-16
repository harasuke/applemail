package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/mailctl"
	"github.com/mirko/applemail/internal/mailstore"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that Mail's data is reachable",
	Long: `Report whether this tool can read Apple Mail, and what it found.

Run this first. If Full Disk Access is missing, this command explains how
to grant it instead of failing with a database error.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()

		paths, err := mailstore.DiscoverPaths(flagMailDir)
		if err != nil {
			// A permission failure is explained once, by main. Returning
			// it here without printing avoids the message appearing twice.
			// Other failures get a diagnosis main would not provide.
			errOut := cmd.ErrOrStderr()
			switch {
			case errors.Is(err, mailstore.ErrNoMailDir):
				fmt.Fprintln(errOut, "No Mail directory found. Is Apple Mail set up on this machine?")
			case errors.Is(err, mailstore.ErrNoVersionDir):
				fmt.Fprintln(errOut, "Mail directory found, but no version directory contains an Envelope Index.")
				fmt.Fprintln(errOut, "This macOS layout is unexpected — please report it.")
			}
			return err
		}

		fmt.Fprintf(out, "Mail directory:     %s\n", paths.Root)
		fmt.Fprintf(out, "Version directory:  %s (V%d)\n", paths.VersionDir, paths.Version)
		fmt.Fprintf(out, "Envelope Index:     %s\n", paths.IndexPath)

		store, err := mailstore.Open(paths)
		if err != nil {
			return err
		}
		defer store.Close()

		messages, mailboxes, err := store.Counts()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Messages:           %d\n", messages)
		fmt.Fprintf(out, "Mailboxes:          %d\n", mailboxes)

		idx, err := mailstore.NewPathIndex(paths)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Message directories: %d\n", idx.DirCount())
		fmt.Fprintln(out, "\nAll checks passed. Mail data is readable.")
		if flagCheckAutomation {
			fmt.Fprintln(out, "\nAutomation access:")
			if err := mailctl.CheckAutomation(); err != nil {
				if errors.Is(err, mailctl.ErrAutomationDenied) {
					fmt.Fprintln(out, "  Not available — grant Automation for Mail to your terminal.")
				} else {
					fmt.Fprintf(out, "  Could not check: %v\n", err)
				}
			} else {
				fmt.Fprintln(out, "  Available — \"mail trash\" can control Mail.")
			}
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&flagCheckAutomation, "check-automation", false,
		"verify the terminal can control Mail (required by \"mail trash\")")
	rootCmd.AddCommand(doctorCmd)
}
