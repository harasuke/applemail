package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSkillsWritesToAllHarnesses(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }

	out := runCommand(t, "install-skills")
	for _, want := range []string{
		filepath.Join(home, ".claude/skills/mail/SKILL.md"),
		filepath.Join(home, ".config/opencode/skills/mail/SKILL.md"),
		filepath.Join(home, ".agents/skills/mail/SKILL.md"),
		filepath.Join(home, ".gemini/skills/mail/SKILL.md"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install-skills output missing %q", want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("skill not written to %s: %v", want, err)
		}
	}
}

func TestUninstallSkillsRemovesSkill(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }

	runCommand(t, "install-skills")
	runCommand(t, "install-skills", "--uninstall")

	path := filepath.Join(home, ".claude/skills/mail/SKILL.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed", path)
	}
}

func TestCheckSkillsReportsMissing(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }

	out := runCommand(t, "install-skills", "--check")
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' in check output, got %q", out)
	}
}
