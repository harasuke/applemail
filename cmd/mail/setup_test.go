package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/mailstore"
)

func runSetupCmd(t *testing.T, input, home string) string {
	t.Helper()
	userHomeDir = func() (string, error) { return home, nil }
	var stdout bytes.Buffer
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"setup"})
	t.Cleanup(resetFlags)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(setup): %v", err)
	}
	return stdout.String()
}

func TestSetupInstallsAllSkills(t *testing.T) {
	home := t.TempDir()
	checkMailAccess = func(string) error { return nil }
	checkAutomationAccess = func() error { return nil }

	runSetupCmd(t, "n\nall\n", home)

	for _, dir := range []string{
		".claude/skills/mail/SKILL.md",
		".config/opencode/skills/mail/SKILL.md",
		".agents/skills/mail/SKILL.md",
		".gemini/skills/mail/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(home, dir)); err != nil {
			t.Errorf("skill not written to %s: %v", dir, err)
		}
	}
}

func TestSetupInstallsOnlySelected(t *testing.T) {
	home := t.TempDir()
	checkMailAccess = func(string) error { return nil }
	checkAutomationAccess = func() error { return nil }

	runSetupCmd(t, "n\n1,2\n", home)

	if _, err := os.Stat(filepath.Join(home, ".claude/skills/mail/SKILL.md")); err != nil {
		t.Errorf("expected Claude skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/opencode/skills/mail/SKILL.md")); err != nil {
		t.Errorf("expected opencode skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/mail/SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("did not expect Codex skill")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/skills/mail/SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("did not expect Gemini skill")
	}
}

func TestSetupGuidesFullDiskAccess(t *testing.T) {
	home := t.TempDir()
	calls := 0
	checkMailAccess = func(string) error {
		calls++
		if calls == 1 {
			return mailstore.ErrNoPermission
		}
		return nil
	}
	checkAutomationAccess = func() error { return nil }

	out := runSetupCmd(t, "\nn\nall\n", home)

	if !strings.Contains(out, "Full Disk Access") {
		t.Errorf("expected FDA guidance, got %q", out)
	}
	if calls != 2 {
		t.Errorf("expected 2 access checks, got %d", calls)
	}
}

func TestSetupEmptySelectsDetected(t *testing.T) {
	home := t.TempDir()
	checkMailAccess = func(string) error { return nil }
	checkAutomationAccess = func() error { return nil }
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)

	runSetupCmd(t, "n\n\n", home)

	if _, err := os.Stat(filepath.Join(home, ".claude/skills/mail/SKILL.md")); err != nil {
		t.Errorf("expected detected Claude skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/mail/SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("did not expect Codex skill (not detected)")
	}
}
