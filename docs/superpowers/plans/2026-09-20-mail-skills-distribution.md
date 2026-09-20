# Mail Skills Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed a single agent skill (SKILL.md) in the `mail` binary and add a `mail install-skills` command that installs it into every agent harness.

**Architecture:** The skill lives at `internal/skill/SKILL.md` and is embedded via `go:embed`, mirroring the existing `internal/reference` package. A new cobra command `mail install-skills` copies the embedded skill into the four harness discovery directories (`~/.claude/skills/mail`, `~/.config/opencode/skills/mail`, `~/.agents/skills/mail`, `~/.gemini/skills/mail`), with `--uninstall` and `--check` variants. `Makefile` targets and two repo-level artifacts (`AGENTS.md`, `.cursor/rules/mail.mdc`) round it out.

**Tech Stack:** Go 1.x, cobra, `go:embed`, bash (Makefile), markdown.

## Global Constraints

- Module path is `github.com/mirko/applemail`.
- Follow the existing embed pattern exactly: `internal/<pkg>/<pkg>.go` with `//go:embed <file>.md` and a `Markdown() string` accessor.
- All command flags are package-level globals declared in `cmd/mail/root.go` and reset in `cmd/mail/commands_test.go` (`resetFlags`), because cobra binds flags to variables that persist across `Execute` calls in a test binary.
- The skill body must stay under ~150 lines; it must not inline the full JSON schema (it points to `mail reference`).
- No new runtime dependencies.

---

### Task 1: Embed the skill

**Files:**
- Create: `internal/skill/SKILL.md`
- Create: `internal/skill/skill.go`
- Create: `internal/skill/skill_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `skill.Markdown() string` — the full SKILL.md content. Consumed by Task 2.

- [ ] **Step 1: Write the skill content**

Create `internal/skill/SKILL.md` with exactly this content:

````markdown
---
name: mail
description: Search and analyze the local Apple Mail store with the `mail` CLI. Use when the user asks to read, search, summarize, export, or trash local email (senders like LinkedIn, invoices, receipts, newsletters), or mentions mail/email/INBOX. Read-only except `mail trash` (which only moves to Trash).
---

# mail

A read-only macOS CLI for searching and analyzing Apple Mail, built to be
driven by scripts and LLM agents. Output is JSONL by default (one object per
line, streamed); errors go to stderr so stdout stays parseable.

## First run

Run `mail doctor` to verify access. Full Disk Access attaches to the
*terminal*, not the `mail` binary. `mail doctor --check-automation` verifies
the trash path.

## Commands

- `mail doctor [--check-automation]` — verify access; run first.
- `mail accounts` — list configured accounts (name/email/id).
- `mail search [query]` — metadata filters resolve instantly; a bare query
  searches bodies (slower).
- `mail show <id>` — one message by ROWID, fully parsed; `--raw` dumps the
  original .emlx.
- `mail links` — extracted URLs (normalized, tracking stripped) with domain,
  anchor text, and class; `--group-by domain` rolls up.
- `mail export --out <dir> [--as eml|json|mbox]` — the only command that
  writes files, and only into the named directory.
- `mail stats` — corpus rollup over the filtered set.
- `mail trash [id]` — move to Trash (recoverable, never permanent).
- `mail reference` — the full embedded reference (commands, schema, gotchas).

Shared filters on `search`, `links`, `export`, `stats`, `trash`:
`--from --to --subject --mailbox --since --until --unread --flagged
--has-attachment --account --limit`.

## Output

JSONL by default. Pipe to `jq`. `--format table` for humans. Use `mail
reference` for the complete JSON schema and exit codes (0 ok / 1 error / 2 no
Full Disk Access / 3 no Automation).

## Safety rules

- Always `mail trash --dry-run` before a batch; never trash without a filter
  or ROWID (`--limit 0` = unlimited would move everything).
- `--limit 0` means unlimited and is the default everywhere except `search`.
- Never open `action` links (unsubscribe/confirm) automatically.
- This tool makes no network requests; fetch a URL only on explicit request.
- `--unread` filters out read mail; don't add it when "all of X" is meant.

## Recipes

Find recent unread mail from a sender:
  mail search --from linkedin --unread --limit 0 | jq -r '.subject'

Read one message in full:
  mail show 48213 | jq -r '.body.text'

Extract clean URLs from a newsletter:
  mail links --from linkedin | jq -r '.url_canonical'

Corpus overview of the last 6 months:
  mail stats --since 6m

Preview a batch deletion, then perform it:
  mail trash --from linkedin --dry-run
  mail trash --from linkedin

Archive a year of mail as a re-importable mbox:
  mail export --out ./archive --since 1y --as mbox
````

- [ ] **Step 2: Write the embed accessor**

Create `internal/skill/skill.go`:

```go
// Package skill embeds the mail agent skill so the binary can install it
// into every agent harness (Claude Code, opencode, Codex, Gemini CLI)
// without needing the source repo.
package skill

import _ "embed"

//go:embed SKILL.md
var doc string

// Markdown returns the full skill document.
func Markdown() string { return doc }
```

- [ ] **Step 3: Write the sanity test**

Create `internal/skill/skill_test.go`:

```go
package skill

import (
	"strings"
	"testing"
)

// TestMarkdownHasFrontmatter guards the skill against drift: every harness
// requires YAML frontmatter with name and description, or the skill is
// silently ignored.
func TestMarkdownHasFrontmatter(t *testing.T) {
	doc := Markdown()
	for _, want := range []string{"name: mail", "description:"} {
		if !strings.Contains(doc, want) {
			t.Errorf("skill is missing frontmatter field %q", want)
		}
	}
}

// TestMarkdownHasSafetyRules pins the red lines that make the skill safe to
// hand to an agent. Removing any of these is a bug, not a wording choice.
func TestMarkdownHasSafetyRules(t *testing.T) {
	doc := Markdown()
	for _, want := range []string{
		"mail trash --dry-run",
		"never open `action` links",
		"no network requests",
		"mail doctor",
		"mail reference",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("skill is missing safety/usage text %q", want)
		}
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/skill/ -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/skill/SKILL.md internal/skill/skill.go internal/skill/skill_test.go
git commit -m "feat: embed agent skill in the binary"
```

---

### Task 2: Add `mail install-skills` command

**Files:**
- Create: `cmd/mail/skill.go`
- Create: `cmd/mail/skill_test.go`
- Modify: `cmd/mail/root.go` (declare two flags)
- Modify: `cmd/mail/commands_test.go` (reset new globals)

**Interfaces:**
- Consumes: `skill.Markdown() string` (Task 1).
- Produces: `mail install-skills`, `mail install-skills --uninstall`, `mail install-skills --check`; package var `userHomeDir func() (string, error)` overridable in tests.

- [ ] **Step 1: Declare the flags**

In `cmd/mail/root.go`, inside the existing `var (...)` block (after `flagVerify bool`), add:

```go
	flagSkillUninstall bool
	flagSkillCheck     bool
```

- [ ] **Step 2: Write the command**

Create `cmd/mail/skill.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/skill"
)

// userHomeDir is a function so tests can point it at a fake home directory.
var userHomeDir = os.UserHomeDir

// skillDirs maps each agent harness to its skill directory, relative to the
// home directory. Codex and Gemini CLI both honor .agents/skills, so the
// third entry covers both; the fourth keeps the explicit ~/.gemini/skills.
var skillDirs = []struct{ name, dir string }{
	{"Claude Code", ".claude/skills/mail"},
	{"opencode", ".config/opencode/skills/mail"},
	{"Codex / Gemini CLI", ".agents/skills/mail"},
	{"Gemini CLI", ".gemini/skills/mail"},
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
```

- [ ] **Step 3: Reset the new globals**

In `cmd/mail/commands_test.go`, add `"os"` to the import block, then inside
`resetFlags()` (after `flagVerify = false`) add:

```go
	flagSkillUninstall = false
	flagSkillCheck = false
	userHomeDir = os.UserHomeDir
```

- [ ] **Step 4: Write the command tests**

Create `cmd/mail/skill_test.go`:

```go
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
```

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/mail/ ./internal/skill/ -v`
Expected: PASS (all tests, including the three new ones).

- [ ] **Step 6: Commit**

```bash
git add cmd/mail/skill.go cmd/mail/skill_test.go cmd/mail/root.go cmd/mail/commands_test.go
git commit -m "feat: add mail install-skills command"
```

---

### Task 3: Makefile targets and repo artifacts

**Files:**
- Modify: `Makefile`
- Create: `AGENTS.md`
- Create: `.cursor/rules/mail.mdc`

**Interfaces:**
- Consumes: `mail install-skills` (Task 2).
- Produces: `make install-skills` / `make uninstall-skills`; AGENTS.md pointer; Cursor rule.

- [ ] **Step 1: Add Makefile targets**

Modify `Makefile` so the `.PHONY` line and targets read:

```make
.PHONY: build test race clean install install-skills uninstall-skills

PREFIX ?= /usr/local

build:
	go build -o mail ./cmd/mail

install-skills: build
	./mail install-skills

uninstall-skills: build
	./mail install-skills --uninstall

test:
	go test ./...

race:
	go test ./... -race

clean:
	rm -f mail

install: build
	install -m 0755 mail $(PREFIX)/bin/mail
	install -d $(PREFIX)/share/man/man1
	install -m 0644 docs/mail.1 $(PREFIX)/share/man/man1/mail.1
```

- [ ] **Step 2: Write AGENTS.md**

Create `AGENTS.md`:

```markdown
# AGENTS.md

This repository is the source of the `mail` CLI — a read-only macOS tool for
searching and analyzing Apple Mail, built to be driven by scripts and LLM
agents.

- Build and install the binary: `make build` / `make install`.
- Install the agent skill (teaches agents how to drive `mail`):
  `make install-skills` (or `./mail install-skills`).
- Full behavior contract: `mail reference` (embedded) or
  `internal/reference/reference.md`.

Safety red lines for `mail`: always `--dry-run` before `mail trash`; never
trash without a filter or ROWID; never open `action` links automatically; the
tool makes no network requests.
```

- [ ] **Step 3: Write the Cursor rule**

Create `.cursor/rules/mail.mdc`:

```markdown
---
description: How to use the `mail` CLI to search, read, and manage local Apple Mail. Applies when working with mail/email data or when a task mentions mail, email, or INBOX.
alwaysApply: false
---
# mail CLI

`mail` is a read-only macOS CLI for Apple Mail (JSONL output, `jq`-friendly).

- Verify access first: `mail doctor`.
- Read one message: `mail show <id>`; search: `mail search [query] --from X`.
- Full contract and schema: `mail reference`.

Safety: always `mail trash --dry-run` before a batch; never trash without a
filter or ROWID; never open `action` links automatically; no network requests.
```

- [ ] **Step 4: Verify the whole tree**

Run: `make build && go test ./... && make install-skills`
Expected: build succeeds, all tests pass, `make install-skills` prints four `installed …/SKILL.md` lines.

- [ ] **Step 5: Commit**

```bash
git add Makefile AGENTS.md .cursor/rules/mail.mdc
git commit -m "docs: add install-skills targets, AGENTS.md, and Cursor rule"
```
