package main

import (
	"github.com/spf13/cobra"
)

var (
	flagFormat        string
	flagMailDir       string
	flagFrom          string
	flagTo            string
	flagSubject       string
	flagMailbox       string
	flagAccount       string
	flagSince         string
	flagUntil         string
	flagUnread        bool
	flagFlagged       bool
	flagHasAttachment bool
	flagMaxBodyChars  int

	// Declared here rather than beside their commands so tests can reset
	// every flag from one place — Cobra binds these to package globals
	// that persist across Execute calls within a test binary.
	flagStats    bool
	flagRaw      bool
	flagAllLinks bool
	flagGroupBy  string
	flagOut      string
	flagExportAs string

	flagDryRun          bool
	flagCheckAutomation bool
	flagVerify          bool

	// limitDefaults records each command's --limit default so tests can
	// reset the per-command flag after an Execute call mutates it.
	limitDefaults = map[*cobra.Command]int{}
)

var rootCmd = &cobra.Command{
	Use:   "mail",
	Short: "Read and search Apple Mail",
	Long: `Read, search, and analyze Apple Mail messages.

Reads Mail's Envelope Index and .emlx files directly, read-only. Output is
JSONL by default so it streams and pipes cleanly.

Requires Full Disk Access for your terminal application. Run "mail doctor"
to check.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// addFilterFlags attaches the shared filter flags to a command. Every
// command that selects messages uses the same set, with the same meaning.
//
// defaultLimit differs by command on purpose: search is interactive and
// caps at 50, while links, export, and stats default to 0 (unlimited)
// because a silent cap would corrupt an archive or an aggregate while
// still looking like it succeeded.
func addFilterFlags(cmd *cobra.Command, defaultLimit int) {
	cmd.Flags().StringVar(&flagFrom, "from", "", "filter by sender address or display name")
	cmd.Flags().StringVar(&flagTo, "to", "", "filter by recipient address")
	cmd.Flags().StringVar(&flagSubject, "subject", "", "filter by subject substring")
	cmd.Flags().StringVar(&flagMailbox, "mailbox", "", "filter by mailbox name or URL")
	cmd.Flags().StringVar(&flagAccount, "account", "", "restrict to one account (email, name, or identifier; run `mail accounts` to list)")
	cmd.Flags().StringVar(&flagSince, "since", "", "only messages on or after this date (YYYY-MM-DD or 30d, 2w, 6m, 1y)")
	cmd.Flags().StringVar(&flagUntil, "until", "", "only messages on or before this date")
	cmd.Flags().BoolVar(&flagUnread, "unread", false, "only unread messages")
	cmd.Flags().BoolVar(&flagFlagged, "flagged", false, "only flagged messages")
	cmd.Flags().BoolVar(&flagHasAttachment, "has-attachment", false, "only messages with attachments")
	cmd.Flags().Int("limit", defaultLimit,
		"maximum messages to return (0 for no limit)")
	limitDefaults[cmd] = defaultLimit
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "jsonl",
		"output format: jsonl, json, table, or text")
	rootCmd.PersistentFlags().StringVar(&flagMailDir, "mail-dir", "",
		"override the Mail directory (default ~/Library/Mail)")
}
