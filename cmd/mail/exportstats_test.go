package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExportWritesEmlFiles(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest)

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("wrote %d files, want 5", len(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".eml") {
			t.Errorf("wrote %q, want a .eml file", e.Name())
		}
	}
}

func TestExportEmlContainsOriginalHeaders(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--subject", "Plain text hello")

	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dest, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Subject: Plain text hello") {
		t.Error("exported .eml does not contain the original headers")
	}
	if strings.Contains(string(raw), "<?xml") {
		t.Error("exported .eml still contains the .emlx plist trailer")
	}
}

func TestExportJSONFormat(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--as", "json", "--limit", "1")

	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(dest, entries[0].Name()))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("exported file is not valid JSON: %v", err)
	}
}

func TestExportMboxWritesSingleArchive(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--as", "mbox")

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "archive.mbox" {
		t.Fatalf("wrote %v, want a single archive.mbox", entries)
	}

	raw, err := os.ReadFile(filepath.Join(dest, "archive.mbox"))
	if err != nil {
		t.Fatal(err)
	}
	// One "From " separator line per message, at the start of a line.
	var separators int
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "From ") {
			separators++
		}
	}
	if separators != 5 {
		t.Errorf("got %d separator lines, want 5", separators)
	}
	if !strings.Contains(string(raw), "Subject: Plain text hello") {
		t.Error("archive does not contain the message headers")
	}
}

func TestIsFromLineEscapesMboxrdForms(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"From someone@example.com Mon Aug  5 20:11:22 2026", true},
		{">From already escaped", true},
		{">>From twice escaped", true},
		{"From: Alice <alice@example.com>", false}, // a header, not a separator
		{"Fromage is not a separator", false},
		{"  From indented", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isFromLine(tt.line); got != tt.want {
			t.Errorf("isFromLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestBuildFilterUsesPerCommandLimitDefault(t *testing.T) {
	resetFlags()
	sf, err := buildFilter(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if sf.Limit != 50 {
		t.Errorf("search default limit = %d, want 50", sf.Limit)
	}
	ef, err := buildFilter(exportCmd)
	if err != nil {
		t.Fatal(err)
	}
	if ef.Limit != 0 {
		t.Errorf("export default limit = %d, want 0", ef.Limit)
	}
}

func TestExportDefaultsToUnlimited(t *testing.T) {
	// export must not inherit search's limit of 50: a silent cap would
	// produce a partial archive that still looks successful.
	if exportCmd.Flags().Lookup("limit").DefValue != "0" {
		t.Errorf("export --limit default = %q, want 0 (unlimited)",
			exportCmd.Flags().Lookup("limit").DefValue)
	}
	for _, c := range []struct {
		name string
		cmd  *cobra.Command
	}{{"stats", statsCmd}, {"links", linksCmd}} {
		if c.cmd.Flags().Lookup("limit").DefValue != "0" {
			t.Errorf("%s --limit default = %q, want 0 (unlimited)",
				c.name, c.cmd.Flags().Lookup("limit").DefValue)
		}
	}
	if searchCmd.Flags().Lookup("limit").DefValue != "50" {
		t.Errorf("search --limit default = %q, want 50",
			searchCmd.Flags().Lookup("limit").DefValue)
	}
}

func TestExportRefusesToWriteIntoMailDirectory(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "export", "--out", root)
	if err == nil {
		t.Fatal("export into the Mail directory returned nil error, want a refusal")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "mail directory") {
		t.Errorf("error = %v, want it to explain the refusal", err)
	}
}

func TestExportRequiresOutFlag(t *testing.T) {
	root := setupFixtureRoot(t)
	if err := executeExpectingError(t, root, "export"); err == nil {
		t.Error("export without --out returned nil error, want a requirement error")
	}
}

func TestExportRejectsUnknownFormat(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "export", "--out", t.TempDir(), "--as", "bogus")
	if err == nil {
		t.Fatal("export --as bogus returned nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to name the bad format", err)
	}
}

func TestStatsEmitsOnlySummary(t *testing.T) {
	out := runCommand(t, "stats")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want exactly the summary:\n%s", len(lines), out)
	}

	var s map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &s); err != nil {
		t.Fatal(err)
	}
	if s["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\"", s["type"])
	}
	if int(s["total"].(float64)) != 5 {
		t.Errorf("total = %v, want 5", s["total"])
	}
}

func TestStatsCountsTopSenders(t *testing.T) {
	out := runCommand(t, "stats")
	var s map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil {
		t.Fatal(err)
	}
	senders, ok := s["top_senders"].([]any)
	if !ok || len(senders) == 0 {
		t.Fatalf("top_senders = %v, want entries", s["top_senders"])
	}
}
