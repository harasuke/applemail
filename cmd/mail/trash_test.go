package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirko/applemail/internal/testdata"
)

func TestTrashDryRunEmitsRecordsWithoutActing(t *testing.T) {
	orig := moveToTrash
	moveToTrash = func(ids []string) (int, error) {
		t.Fatal("moveToTrash called during --dry-run")
		return 0, nil
	}
	defer func() { moveToTrash = orig }()

	out := runCommand(t, "trash", "--dry-run", "--subject", "Plain text hello")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	if r["action"] != "trash" {
		t.Errorf("action = %v, want trash", r["action"])
	}
	if r["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", r["dry_run"])
	}
	if r["message_id"] != "<plain-1@example.com>" {
		t.Errorf("message_id = %v, want the RFC header <plain-1@example.com>", r["message_id"])
	}
}

func TestTrashBatchUsesRFCMessageIDNotIndexID(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	out := runCommand(t, "trash", "--subject", "Plain text hello")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	if len(got) != 1 {
		t.Fatalf("moveToTrash got %d ids, want 1", len(got))
	}
	// The Envelope Index message_id for fixture 1 is <fixture-1@example.com>;
	// the RFC header in the .emlx is <plain-1@example.com>. AppleScript needs
	// the RFC header, so this proves trash consumed the right value.
	if got[0] != "<plain-1@example.com>" {
		t.Errorf("moveToTrash id = %q, want <plain-1@example.com>", got[0])
	}
}

func TestTrashSingleID(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	out := runCommand(t, "trash", "3")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	if len(got) != 1 || got[0] != "<multi-3@example.net>" {
		t.Errorf("moveToTrash ids = %v, want [<multi-3@example.net>]", got)
	}
}

func TestTrashSingleIDUnknownFails(t *testing.T) {
	orig := moveToTrash
	moveToTrash = func(ids []string) (int, error) {
		t.Fatal("moveToTrash called for a nonexistent id")
		return 0, nil
	}
	defer func() { moveToTrash = orig }()

	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "trash", "99999")
	if err == nil {
		t.Fatal("trash 99999 returned nil error, want not-found")
	}
}

func TestTrashDefaultLimitIsUnlimited(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	// Five fixture messages; no --limit means all five are selected.
	runCommand(t, "trash")
	if len(got) != 5 {
		t.Errorf("moveToTrash got %d ids, want 5 (unlimited default)", len(got))
	}
}

func TestTrashBatchEmitsDryRunFalse(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()
	moveToTrash = func(ids []string) (int, error) { return 0, nil }

	out, _ := runCommandFull(t, testdata.DefaultMessages(), "trash", "--subject", "Plain text hello")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	if r["dry_run"] != false {
		t.Errorf("dry_run = %v, want false for a real batch", r["dry_run"])
	}
}

func TestTrashBatchReportsNotFoundOnStderr(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()
	moveToTrash = func(ids []string) (int, error) { return 1, nil }

	_, stderr := runCommandFull(t, testdata.DefaultMessages(), "trash", "--subject", "Plain text hello")
	if !strings.Contains(stderr, "not found in Mail") {
		t.Errorf("stderr = %q, want the not-found warning", stderr)
	}
}

func TestTrashSkipsMessagesWithoutMessageID(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	noID := testdata.FixtureMessage{
		ROWID: 42, Subject: "No Message-ID",
		SenderName: "Nobody", SenderAddr: "nobody@example.com",
		ToAddr: "user@example.com", DateSent: 1785960682, DateRecv: 1785960700,
		MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
		Summary: "no header",
		RawMIME: "From: Nobody <nobody@example.com>\r\n" +
			"To: user@example.com\r\n" +
			"Subject: No Message-ID\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
			"no header\r\n",
	}

	_, stderr := runCommandFull(t, []testdata.FixtureMessage{noID}, "trash")
	if len(got) != 0 {
		t.Errorf("moveToTrash got %v, want no ids for an unaddressable message", got)
	}
	if !strings.Contains(stderr, "no Message-ID header") {
		t.Errorf("stderr = %q, want the unaddressable warning", stderr)
	}
}

// runCommandFull executes the CLI against the given fixture set and returns
// both stdout and stderr, since trash reports warnings on stderr that the
// shared runCommand helper discards.
func runCommandFull(t *testing.T, msgs []testdata.FixtureMessage, args ...string) (stdout, stderr string) {
	t.Helper()
	root := testdata.BuildMailDir(t, msgs)

	var out, errB bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errB)
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))
	t.Cleanup(resetFlags)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return out.String(), errB.String()
}

func TestTrashVerifyReportsRemainingOnStderr(t *testing.T) {
	origMove := moveToTrash
	origVerify := verifyRemaining
	defer func() { moveToTrash = origMove; verifyRemaining = origVerify }()

	moveToTrash = func(ids []string) (int, error) { return 0, nil }
	verifyRemaining = func(ids []string) ([]string, error) {
		return []string{"plain-1@example.com"}, nil
	}

	_, stderr := runCommandFull(t, testdata.DefaultMessages(), "trash", "--verify", "--subject", "Plain text hello")
	if !strings.Contains(stderr, "still present") {
		t.Errorf("stderr = %q, want the still-present warning", stderr)
	}
	if !strings.Contains(stderr, "plain-1@example.com") {
		t.Errorf("stderr = %q, want the remaining message id", stderr)
	}
}

func TestTrashVerifySilentWhenNoneRemain(t *testing.T) {
	origMove := moveToTrash
	origVerify := verifyRemaining
	defer func() { moveToTrash = origMove; verifyRemaining = origVerify }()

	moveToTrash = func(ids []string) (int, error) { return 0, nil }
	verifyRemaining = func(ids []string) ([]string, error) { return nil, nil }

	_, stderr := runCommandFull(t, testdata.DefaultMessages(), "trash", "--verify", "--subject", "Plain text hello")
	if strings.Contains(stderr, "still present") {
		t.Errorf("stderr = %q, want no warning when everything moved", stderr)
	}
}

func TestTrashDryRunDoesNotVerify(t *testing.T) {
	origMove := moveToTrash
	origVerify := verifyRemaining
	defer func() { moveToTrash = origMove; verifyRemaining = origVerify }()

	moveToTrash = func(ids []string) (int, error) { return 0, nil }
	verifyRemaining = func(ids []string) ([]string, error) {
		t.Fatal("verifyRemaining called during --dry-run")
		return nil, nil
	}

	runCommand(t, "trash", "--dry-run", "--verify", "--subject", "Plain text hello")
}
