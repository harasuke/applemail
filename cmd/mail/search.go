package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/corpus"
	"github.com/mirko/applemail/internal/output"
	"github.com/mirko/applemail/internal/scan"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search messages by metadata and body text",
	Long: `Search Apple Mail.

Metadata filters resolve against Mail's index and are instant. A bare
[query] argument searches message bodies, which reads .emlx files from
disk — combine it with filters to narrow the scan.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = args[0]
		}
		return runScan(cmd, query, flagStats)
	},
}

// runScan is the shared pipeline behind search, links, export, and stats:
// open the store, build the filter, scan, render, report skips.
func runScan(cmd *cobra.Command, bodyQuery string, withStats bool) error {
	filter, err := buildFilter(cmd)
	if err != nil {
		return err
	}

	store, paths, err := openMail()
	if err != nil {
		return err
	}
	defer store.Close()

	renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	acc := corpus.NewAccumulator()
	scanner := scan.New(store, paths)

	stats, err := scanner.Run(cmd.Context(), scan.Options{
		Filter:       filter,
		BodyQuery:    bodyQuery,
		MaxBodyChars: flagMaxBodyChars,
	}, func(m output.Message) error {
		if withStats {
			acc.Add(m)
			return nil // stats mode emits only the summary
		}
		acc.Add(m)
		return renderer.Write(m)
	})
	if err != nil {
		return err
	}

	if withStats {
		if err := renderer.Write(acc.Summary(stats.Skipped)); err != nil {
			return err
		}
	}
	if err := renderer.Close(); err != nil {
		return err
	}

	if stats.Skipped > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d messages skipped (%d missing files, %d parse errors)\n",
			stats.Skipped, stats.MissingFiles, stats.ParseErrors)
	}
	return nil
}

func init() {
	addFilterFlags(searchCmd, 50)
	searchCmd.Flags().IntVar(&flagMaxBodyChars, "max-body-chars", 0,
		"truncate message bodies to this many characters (0 for no limit)")
	searchCmd.Flags().BoolVar(&flagStats, "stats", false,
		"emit only the corpus summary instead of individual messages")
	rootCmd.AddCommand(searchCmd)
}
