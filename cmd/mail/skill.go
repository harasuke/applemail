package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/skill"
)

// userHomeDir is a function so tests can point it at a fake home directory.
var userHomeDir = os.UserHomeDir

// skillDirs maps each agent harness to its skill directory, relative to the
// home directory. `detect` is the config directory whose presence marks the
// harness as installed (used by `mail setup` for pre-selection). Codex and
// Gemini CLI both honor .agents/skills, so the Codex entry covers both; the
// Gemini entry keeps the explicit ~/.gemini/skills.
var skillDirs = []struct{ name, dir, detect string }{
	{"Claude Code", ".claude/skills/mail", ".claude"},
	{"opencode", ".config/opencode/skills/mail", ".config/opencode"},
	{"Codex", ".agents/skills/mail", ".codex"},
	{"Gemini CLI", ".gemini/skills/mail", ".gemini"},
}

var installSkillsCmd = &cobra.Command{
	Use:   "install-skills",
	Short: "Install the mail skill for AI agents",
	Long: `Write the embedded mail skill (SKILL.md) into the skill directories
of every agent harness, so Claude Code, opencode, Codex and Gemini CLI can
discover and drive this tool.

The skill is embedded in this binary, so re-running this command after an
upgrade refreshes it. Use --uninstall to remove it and --check to verify.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := userHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}
		out := cmd.OutOrStdout()
		switch {
		case flagSkillUninstall:
			return uninstallSkills(out, home)
		case flagSkillCheck:
			return checkSkills(out, home)
		default:
			return installSkills(out, home)
		}
	},
}

func installSkills(out io.Writer, home string) error {
	for _, h := range skillDirs {
		dir := filepath.Join(home, h.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(skill.Markdown()), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Fprintf(out, "installed %s\n", path)
	}
	return nil
}

func uninstallSkills(out io.Writer, home string) error {
	for _, h := range skillDirs {
		path := filepath.Join(home, h.dir, "SKILL.md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Fprintf(out, "not installed %s\n", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		fmt.Fprintf(out, "removed %s\n", path)
	}
	return nil
}

func checkSkills(out io.Writer, home string) error {
	for _, h := range skillDirs {
		path := filepath.Join(home, h.dir, "SKILL.md")
		got, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			fmt.Fprintf(out, "missing  %s\n", path)
		case string(got) != skill.Markdown():
			fmt.Fprintf(out, "stale    %s\n", path)
		default:
			fmt.Fprintf(out, "ok       %s\n", path)
		}
	}
	return nil
}

func init() {
	installSkillsCmd.Flags().BoolVar(&flagSkillUninstall, "uninstall", false,
		"remove the skill instead of installing it")
	installSkillsCmd.Flags().BoolVar(&flagSkillCheck, "check", false,
		"report whether the installed skill matches this binary")
	rootCmd.AddCommand(installSkillsCmd)
}
