package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/mailctl"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

// moveToTrash is the seam tests substitute to avoid invoking AppleScript.
// It is mailctl.MoveToTrash in production.
var moveToTrash = mailctl.MoveToTrash

// verifyRemaining is the seam tests substitute to avoid invoking AppleScript.
// It is mailctl.RemainingInMailboxes in production.
var verifyRemaining = mailctl.RemainingInMailboxes

var trashCmd = &cobra.Command{
	Use:   "trash [id]",
	Short: "Move messages to Trash",
	Long: `Move matching messages to the Trash via AppleScript.

Pass a single ROWID to move one message, or use the shared filter flags
(--from, --to, --subject, --mailbox, --since, --until, --unread, --flagged,
--has-attachment) to move a batch. Trash is recoverable until it is emptied
in Mail.

--dry-run reports what would be moved without changing anything. Moving
messages requires Automation permission for your terminal (see "mail doctor
--check-automation").

--verify re-checks Mail after moving and reports any message still present in
a non-Trash mailbox, since the Envelope Index can lag behind Mail.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return runTrashOne(cmd, args[0])
		}
		return runTrashBatch(cmd)
	},
}

func trashRecord(m output.Message) output.Trash {
	return output.Trash{
		ID:        m.ID,
		MessageID: m.MessageIDHeader,
		Subject:   m.Subject,
		From:      m.From,
		Action:    "trash",
		DryRun:    flagDryRun,
	}
}

func runTrashOne(cmd *cobra.Command, arg string) error {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid message id %q: %w", arg, err)
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

	scanner := scan.New(store, paths)
	msg, found, err := scanner.One(id, scan.Options{})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("message %d not found", id)
	}
	if msg.MessageIDHeader == "" {
		if flagDryRun {
			if err := renderer.Write(trashRecord(msg)); err != nil {
				return err
			}
			return renderer.Close()
		}
		return fmt.Errorf("message %d has no Message-ID header and cannot be addressed", id)
	}

	if !flagDryRun {
		notFound, err := moveToTrash([]string{msg.MessageIDHeader})
		if err != nil {
			return err
		}
		if notFound > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%d messages not found in Mail (already moved or deleted)\n", notFound)
		}
		if flagVerify {
			if err := reportStillPresent(cmd, []string{msg.MessageIDHeader}); err != nil {
				return err
			}
		}
	}
	if err := renderer.Write(trashRecord(msg)); err != nil {
		return err
	}
	return renderer.Close()
}

// reportStillPresent re-checks Mail and reports any of rfcIDs still present
// in a non-Trash mailbox. It is only called after a real move (never with
// --dry-run), because the Envelope Index can lag behind Mail's own state.
func reportStillPresent(cmd *cobra.Command, rfcIDs []string) error {
	remaining, err := verifyRemaining(rfcIDs)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"%d messages still present in Mail after trash (verify):\n", len(remaining))
	for _, id := range remaining {
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", id)
	}
	return nil
}

func runTrashBatch(cmd *cobra.Command) error {
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

	scanner := scan.New(store, paths)
	var pending []output.Trash
	var rfcIDs []string
	var unaddressable int

	stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
		func(m output.Message) error {
			if m.MessageIDHeader == "" {
				unaddressable++
				return nil
			}
			rec := trashRecord(m)
			if flagDryRun {
				return renderer.Write(rec)
			}
			pending = append(pending, rec)
			rfcIDs = append(rfcIDs, m.MessageIDHeader)
			return nil
		})
	if err != nil {
		return err
	}

	if !flagDryRun && len(rfcIDs) > 0 {
		notFound, err := moveToTrash(rfcIDs)
		if err != nil {
			return err
		}
		if notFound > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%d messages not found in Mail (already moved or deleted)\n", notFound)
		}
		if flagVerify {
			if err := reportStillPresent(cmd, rfcIDs); err != nil {
				return err
			}
		}
		for _, rec := range pending {
			if err := renderer.Write(rec); err != nil {
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
	if unaddressable > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d messages skipped (no Message-ID header)\n", unaddressable)
	}
	return nil
}

func init() {
	addFilterFlags(trashCmd, 0)
	trashCmd.Flags().BoolVar(&flagDryRun, "dry-run", false,
		"report what would be moved without changing anything")
	trashCmd.Flags().BoolVar(&flagVerify, "verify", false,
		"re-check Mail after moving and report messages still present")
	rootCmd.AddCommand(trashCmd)
}
