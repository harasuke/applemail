// Package mailctl drives Apple Mail via AppleScript for the few operations
// this tool performs that mutate mail (moving messages to Trash).
package mailctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrAutomationDenied means macOS refused the Apple Event: the terminal
// lacks Automation permission for Mail.
var ErrAutomationDenied = errors.New("automation permission denied for Mail")

// buildScript returns the AppleScript that moves every message whose RFC
// Message-ID is in argv to the Trash. It is a fixed template — the IDs are
// passed as arguments, never interpolated into the source — so the text can
// be asserted in tests without executing it.
//
// The script iterates every mailbox of every account in a single `tell`
// block, comparing each message's `message id` against the argv list. A
// recursive handler is deliberately avoided: passing a mailbox reference
// through `my` re-enters Mail's `tell` context and fails with -1728.
//
// Trash and junk mailboxes are skipped by name, so a message already in the
// Trash is never matched and never `delete`d again — in Mail's AppleScript
// dictionary, `delete` on an already-trashed message is a permanent delete,
// and the `deleted status` property reports false even for trashed messages,
// so the mailbox name is the reliable guard. The script returns the number
// of matched messages.
func buildScript() string {
	return `on run argv
	set msgIDs to argv
	set found to {}
	tell application "Mail"
		repeat with eachAccount in accounts
			repeat with eachMailbox in every mailbox of eachAccount
				if not (my isTrashMailbox(name of eachMailbox)) then
					try
						set msgs to (get every message of eachMailbox)
						repeat with eachMessage in msgs
							if (message id of eachMessage) is in msgIDs then
								set end of found to eachMessage
							end if
						end repeat
					end try
				end if
			end repeat
		end repeat
		repeat with eachMessage in found
			delete eachMessage
		end repeat
		return (count of found)
	end tell
end run

on isTrashMailbox(mbName)
	return (mbName is "Deleted Messages") or (mbName is "Cestino") or (mbName is "Posta eliminata") or (mbName is "Trash") or (mbName is "Junk") or (mbName is "Spam") or (mbName is "Posta indesiderata")
end isTrashMailbox
`
}

// runOsaScript executes an AppleScript with argv and returns its stdout,
// stderr, and any exec error. It is a package variable so tests can
// substitute a fake without invoking osascript.
var runOsaScript = func(script string, args []string) (stdout, stderr string, err error) {
	f, err := os.CreateTemp("", "mailctl-*.scpt")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}

	cmd := exec.Command("osascript", append([]string{f.Name()}, args...)...)
	var out, e strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &e
	err = cmd.Run()
	return out.String(), e.String(), err
}

// classify maps an osascript failure to a typed error. The -1743 Apple
// Event error is Automation permission missing; everything else stays a
// plain error carrying the script's stderr.
func classify(stderr string, runErr error) error {
	if runErr == nil {
		return nil
	}
	if strings.Contains(stderr, "-1743") ||
		strings.Contains(strings.ToLower(stderr), "not authorized") {
		return ErrAutomationDenied
	}
	return fmt.Errorf("osascript: %v: %s", runErr, strings.TrimSpace(stderr))
}

// normalizeID strips the surrounding angle brackets from an RFC Message-ID.
// The .emlx header stores the ID as "<...>" but Mail's AppleScript `message
// id` property returns it without the brackets, so they must be removed for
// the comparison to match.
func normalizeID(id string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(id), "<"), ">")
}

// buildVerifyScript returns an AppleScript that reports which of the given
// message IDs are still present in any mailbox that is not Trash, Junk, or a
// Gmail "All Mail" folder. It is used by `mail trash --verify` to confirm a
// move actually landed, because the Envelope Index can lag behind Mail. It
// returns the remaining IDs joined by linefeeds (empty string = none).
func buildVerifyScript() string {
	return `on run argv
	set msgIDs to argv
	set remaining to ""
	tell application "Mail"
		repeat with eachAccount in accounts
			repeat with eachMailbox in every mailbox of eachAccount
				if not (my isIgnorableMailbox(name of eachMailbox)) then
					try
						set msgs to (get every message of eachMailbox)
						repeat with eachMessage in msgs
							if (message id of eachMessage) is in msgIDs then
								set remaining to remaining & (message id of eachMessage) & linefeed
							end if
						end repeat
					end try
				end if
			end repeat
		end repeat
	end tell
	return remaining
end run

on isIgnorableMailbox(mbName)
	return (mbName is "Deleted Messages") or (mbName is "Cestino") or (mbName is "Posta eliminata") or (mbName is "Trash") or (mbName is "Junk") or (mbName is "Spam") or (mbName is "Posta indesiderata") or (mbName is "Tutti i messaggi") or (mbName is "All Mail") or (mbName is "All Messages")
end isIgnorableMailbox
`
}

// RemainingInMailboxes returns the subset of messageIDs that Mail still
// reports in a non-Trash, non-Junk, non-"All Mail" mailbox. An empty result
// means every message left those mailboxes (moved, already trashed, or
// already gone).
func RemainingInMailboxes(messageIDs []string) ([]string, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(messageIDs))
	for i, id := range messageIDs {
		ids[i] = normalizeID(id)
	}
	stdout, stderr, err := runOsaScript(buildVerifyScript(), ids)
	if err != nil {
		return nil, classify(stderr, err)
	}
	var remaining []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			remaining = append(remaining, line)
		}
	}
	return remaining, nil
}

// MoveToTrash moves every message whose RFC Message-ID is in messageIDs to
// the Trash in a single AppleScript invocation. It returns the number of
// requested IDs that matched no message — counted, never fatal, since Mail
// may already have moved them or the index was stale.
func MoveToTrash(messageIDs []string) (notFound int, err error) {
	if len(messageIDs) == 0 {
		return 0, nil
	}
	ids := make([]string, len(messageIDs))
	for i, id := range messageIDs {
		ids[i] = normalizeID(id)
	}
	stdout, stderr, err := runOsaScript(buildScript(), ids)
	if err != nil {
		return 0, classify(stderr, err)
	}
	matched, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil {
		return 0, fmt.Errorf("parse osascript result %q: %w", stdout, err)
	}
	if matched >= len(ids) {
		return 0, nil
	}
	return len(ids) - matched, nil
}

// Account is one configured Mail account, as reported by Mail itself.
type Account struct {
	Name   string   // the display name in Mail (e.g. "Omnys")
	ID     string   // the identifier used in mailbox URLs (e.g. a UUID)
	Emails []string // the account's email addresses
}

// buildListAccountsScript returns an AppleScript that lists each account's
// name, identifier, and email addresses — one account per line, fields
// pipe-separated, emails comma-separated within the field.
func buildListAccountsScript() string {
	return `on run
	set out to ""
	tell application "Mail"
		repeat with a in accounts
			set theEmails to ""
			try
				set theEmails to (email addresses of a) as text
			end try
			set out to out & (name of a) & "|" & (id of a) & "|" & theEmails & linefeed
		end repeat
	end tell
	return out
end run
`
}

// ListAccounts returns the accounts configured in Mail, via AppleScript. It
// requires Automation permission, like every Mail Apple Event.
func ListAccounts() ([]Account, error) {
	stdout, stderr, err := runOsaScript(buildListAccountsScript(), nil)
	if err != nil {
		return nil, classify(stderr, err)
	}
	var accounts []Account
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		a := Account{Name: strings.TrimSpace(parts[0]), ID: strings.TrimSpace(parts[1])}
		if len(parts) >= 3 {
			for _, e := range strings.Split(parts[2], ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					a.Emails = append(a.Emails, e)
				}
			}
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// CheckAutomation verifies the terminal can send Apple events to Mail
// without side effects. It returns nil when allowed, ErrAutomationDenied
// otherwise. Used by "mail doctor --check-automation".
func CheckAutomation() error {
	_, stderr, err := runOsaScript(`on run
tell application "Mail" to get version
return "ok"
end run
`, nil)
	if err != nil {
		return classify(stderr, err)
	}
	return nil
}
