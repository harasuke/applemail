package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/mailctl"
	"github.com/harasuke/applemail/internal/mailstore"
)

// listAccounts is the seam tests substitute to avoid invoking AppleScript.
// It is mailctl.ListAccounts in production.
var listAccounts = mailctl.ListAccounts

// Exit codes. A distinct code for permissions lets a script tell
// "no results" from "no access" without parsing text.
const (
	exitOK         = 0
	exitError      = 1
	exitPermission = 2
	exitAutomation = 3
)

func exitCodeFor(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, mailstore.ErrNoPermission):
		return exitPermission
	case errors.Is(err, mailctl.ErrAutomationDenied):
		return exitAutomation
	default:
		return exitError
	}
}

// permissionMessage explains how to grant Full Disk Access.
//
// macOS TCC evaluates the *responsible process*, which for a CLI is the
// terminal application, not this binary. On macOS 26.1+ adding a bare
// (non-.app) binary to the Full Disk Access list is reported not to work,
// so the terminal is the supported path.
func permissionMessage(root string) string {
	return fmt.Sprintf(`Cannot read %s — macOS denied access.

Apple Mail's data is protected by macOS privacy controls. Grant Full Disk
Access to your TERMINAL application (Terminal, iTerm, or whichever you are
using) — not to the "mail" binary itself, which macOS does not accept.

  1. Open System Settings → Privacy & Security → Full Disk Access
  2. Enable your terminal application (add it with + if it is not listed)
  3. Quit and restart the terminal completely
  4. Run "mail doctor" to confirm

Nothing is written to your mail: this tool opens the database read-only.`, root)
}

// automationMessage explains how to grant Automation access.
//
// Controlling Mail via Apple Events is a second, separate TCC grant from
// Full Disk Access, and like FDA it attaches to the responsible process —
// the terminal — not to this binary.
func automationMessage() string {
	return `Cannot control Apple Mail — macOS denied Automation access.

Moving messages to Trash requires your terminal to control Mail via Apple
Events, a separate permission from Full Disk Access.

  1. Open System Settings → Privacy & Security → Automation
  2. Find your terminal application and enable it for "Mail"
  3. Quit and restart the terminal completely
  4. Run "mail doctor --check-automation" to confirm

No changes were made to your mail.`
}

// openMail resolves paths and opens the store and the .emlx path index.
func openMail() (*mailstore.Store, *mailstore.PathIndex, error) {
	paths, err := mailstore.DiscoverPaths(flagMailDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := mailstore.Open(paths)
	if err != nil {
		return nil, nil, err
	}
	idx, err := mailstore.NewPathIndex(paths)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return store, idx, nil
}

var relativeDateRe = regexp.MustCompile(`^(\d+)([dwmy])$`)

// parseSince accepts either a relative span (30d, 2w, 6m, 1y) or an
// absolute YYYY-MM-DD date.
func parseSince(s string) (time.Time, error) {
	if m := relativeDateRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration %q", s)
		}
		now := time.Now()
		switch m[2] {
		case "d":
			return now.AddDate(0, 0, -n), nil
		case "w":
			return now.AddDate(0, 0, -n*7), nil
		case "m":
			return now.AddDate(0, -n, 0), nil
		case "y":
			return now.AddDate(-n, 0, 0), nil
		}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid date %q (want YYYY-MM-DD or a span like 30d, 2w, 6m, 1y)", s)
	}
	return t, nil
}

// resolveAccountUUID maps a user-supplied account reference (email address,
// display name, or identifier) to the account identifier used in mailbox
// URLs, by querying Mail. Exact matches win; a substring match follows; a
// miss is an error naming the resolution command.
func resolveAccountUUID(value string) (string, error) {
	accounts, err := listAccounts()
	if err != nil {
		return "", err
	}
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return "", nil
	}
	for _, a := range accounts {
		if strings.ToLower(a.ID) == v {
			return a.ID, nil
		}
		for _, e := range a.Emails {
			if strings.ToLower(e) == v {
				return a.ID, nil
			}
		}
	}
	for _, a := range accounts {
		if strings.Contains(strings.ToLower(a.ID), v) ||
			strings.Contains(strings.ToLower(a.Name), v) {
			return a.ID, nil
		}
		for _, e := range a.Emails {
			if strings.Contains(strings.ToLower(e), v) {
				return a.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no account matches %q (run `mail accounts` to list them)", value)
}

// buildFilter assembles the shared filter from the persistent flags.
func buildFilter(cmd *cobra.Command) (mailstore.Filter, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	f := mailstore.Filter{
		From:          flagFrom,
		To:            flagTo,
		Subject:       flagSubject,
		Mailbox:       flagMailbox,
		Unread:        flagUnread,
		Flagged:       flagFlagged,
		HasAttachment: flagHasAttachment,
		Limit:         limit,
	}
	if flagAccount != "" {
		uuid, err := resolveAccountUUID(flagAccount)
		if err != nil {
			return f, err
		}
		f.Account = uuid
	}
	if flagSince != "" {
		t, err := parseSince(flagSince)
		if err != nil {
			return f, err
		}
		f.Since = &t
	}
	if flagUntil != "" {
		t, err := parseSince(flagUntil)
		if err != nil {
			return f, err
		}
		// An absolute --until date means "through that day", so push the
		// boundary to the next midnight; <= then includes the whole day.
		// Relative spans (30d, 2w, …) are already a point in time.
		if !relativeDateRe.MatchString(flagUntil) {
			t = t.AddDate(0, 0, 1)
		}
		f.Until = &t
	}
	return f, nil
}
