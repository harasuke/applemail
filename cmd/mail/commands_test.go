package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mirko/applemail/internal/mailctl"
	"github.com/mirko/applemail/internal/testdata"
)

// runCommand executes the CLI against a fixture Mail directory and
// returns stdout.
func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))

	t.Cleanup(resetFlags)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return stdout.String()
}

// resetFlags restores flag defaults between tests, since Cobra binds them
// to package-level variables that persist across Execute calls.
//
// This covers flags introduced in later tasks too. A flag left set by one
// test silently changes what the next test exercises — for example a
// leaked --raw makes a "message not found" test pass through the wrong
// code path.
func resetFlags() {
	flagFormat = "jsonl"
	flagMailDir = ""
	flagFrom, flagTo, flagSubject, flagMailbox = "", "", "", ""
	flagAccount = ""
	flagSince, flagUntil = "", ""
	flagUnread, flagFlagged, flagHasAttachment = false, false, false
	flagMaxBodyChars = 0
	flagStats = false
	flagRaw = false
	flagAllLinks = false
	flagGroupBy = ""
	flagOut = ""
	flagExportAs = "eml"
	flagDryRun = false
	flagCheckAutomation = false
	flagVerify = false
	flagSkillUninstall = false
	flagSkillCheck = false
	userHomeDir = os.UserHomeDir
	moveToTrash = mailctl.MoveToTrash
	verifyRemaining = mailctl.RemainingInMailboxes
	listAccounts = mailctl.ListAccounts
	for c, def := range limitDefaults {
		_ = c.Flags().Set("limit", strconv.Itoa(def))
	}
}

// setupFixtureRoot builds a fixture Mail directory and returns its root.
func setupFixtureRoot(t *testing.T) string {
	t.Helper()
	return testdata.BuildMailDir(t, testdata.DefaultMessages())
}

// executeExpectingError runs the CLI and returns the error rather than
// failing the test, for cases where a failure is the expected outcome.
func executeExpectingError(t *testing.T, root string, args ...string) error {
	t.Helper()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))
	t.Cleanup(resetFlags)
	return rootCmd.Execute()
}

func TestDoctorReportsCounts(t *testing.T) {
	out := runCommand(t, "doctor")
	if !strings.Contains(out, "5") {
		t.Errorf("doctor output does not report the message count:\n%s", out)
	}
	if !strings.Contains(out, "V12") {
		t.Errorf("doctor output does not report the version directory:\n%s", out)
	}
}

func TestSearchEmitsJSONLPerMessage(t *testing.T) {
	out := runCommand(t, "search")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestSearchAppliesSubjectFilter(t *testing.T) {
	out := runCommand(t, "search", "--subject", "Plain text hello")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), out)
	}
}

func TestSearchBodyQueryMatchesContent(t *testing.T) {
	out := runCommand(t, "search", "Just saying hello")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), out)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	if m["subject"] != "Plain text hello" {
		t.Errorf("subject = %v, want the matching message", m["subject"])
	}
}

func TestSearchLimitCapsResults(t *testing.T) {
	out := runCommand(t, "search", "--limit", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}
}

func TestSearchTableFormat(t *testing.T) {
	out := runCommand(t, "search", "--format", "table", "--limit", "1")
	if !strings.Contains(out, "SUBJECT") {
		t.Errorf("table output has no header:\n%s", out)
	}
}

func TestSearchNoMatchesEmitsNothing(t *testing.T) {
	out := runCommand(t, "search", "--subject", "no-such-subject-anywhere")
	if strings.TrimSpace(out) != "" {
		t.Errorf("output = %q, want empty for no matches", out)
	}
}

func TestAccountsCommandListsAccounts(t *testing.T) {
	orig := listAccounts
	defer func() { listAccounts = orig }()
	listAccounts = func() ([]mailctl.Account, error) {
		return []mailctl.Account{
			{Name: "Omnys", ID: "4F602833", Emails: []string{"mirko.spinato@omnys.com"}},
		}, nil
	}

	out := runCommand(t, "accounts")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	var a map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &a); err != nil {
		t.Fatal(err)
	}
	if a["name"] != "Omnys" || a["id"] != "4F602833" {
		t.Errorf("record = %v, want name=Omnys id=4F602833", a)
	}
	if a["emails"] == nil {
		t.Errorf("emails = %v, want the account email", a["emails"])
	}
}
