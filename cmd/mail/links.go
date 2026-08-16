package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/analyze"
	"github.com/mirko/applemail/internal/output"
	"github.com/mirko/applemail/internal/scan"
)

var linksCmd = &cobra.Command{
	Use:   "links",
	Short: "Extract links from messages",
	Long: `Extract every URL from matching messages, normalized and deduplicated.

Tracking parameters are stripped and redirect wrappers are unwrapped
offline, so the emitted URL is the one worth opening. Recognized patterns
(LinkedIn job postings) collapse to a stable dedup key, so the same target
arriving in several emails appears once.

By default only "content" links are emitted — the ones that resolve to a
real page. Use --all to also see tracking beacons and action links
(unsubscribe, confirmation) which should never be opened automatically.

This command makes no network requests.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagGroupBy != "" && flagGroupBy != "domain" {
			return fmt.Errorf("invalid --group-by value %q (want \"domain\")", flagGroupBy)
		}

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

		domains := map[string]int{}
		scanner := scan.New(store, paths)

		seen := map[string]bool{}
		stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
			func(m output.Message) error {
				for _, l := range m.Links {
					key := l.Class + "|" + l.DedupKey
					if seen[key] {
						continue
					}
					seen[key] = true
					if !flagAllLinks && l.Class != string(analyze.ClassContent) {
						continue
					}
					if flagGroupBy == "domain" {
						domains[l.Domain]++
						continue
					}
					if err := renderer.Write(l); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			return err
		}

		if flagGroupBy == "domain" {
			counts := make([]output.Count, 0, len(domains))
			for k, v := range domains {
				counts = append(counts, output.Count{Key: k, Count: v})
			}
			sort.Slice(counts, func(i, j int) bool {
				if counts[i].Count != counts[j].Count {
					return counts[i].Count > counts[j].Count
				}
				return counts[i].Key < counts[j].Key
			})
			for _, c := range counts {
				if err := renderer.Write(c); err != nil {
					return err
				}
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
	},
}

func init() {
	addFilterFlags(linksCmd, 0)
	linksCmd.Flags().BoolVar(&flagAllLinks, "all", false,
		"include tracking and action links, not just content links")
	linksCmd.Flags().StringVar(&flagGroupBy, "group-by", "",
		"roll up results: \"domain\"")
	rootCmd.AddCommand(linksCmd)
}
