# Mail CLI — Agent Skills & Installation

**Date:** 2026-09-20
**Status:** approved design (pending spec review)

## 1. Goal

Let any AI agent/harness (Claude Code, opencode, Codex, Gemini CLI, Cursor,
and generic AGENTS.md readers) discover and safely drive the installed `mail`
CLI, distributed with a real Go install mechanism (no `curl | sh`, no repo
clone).

Two deliverables:

1. A single canonical **skill** (open "Agent Skills" standard) that teaches an
   agent how to use `mail` — including the safety rules — and is **embedded in
   the binary**.
2. A `mail install-skills` subcommand that extracts that embedded skill into
   each harness's discovery location.

## 2. Background / constraints

- `mail` is already agent-friendly: JSONL-by-default output, distinct exit
  codes (0 success / 1 error / 2 no Full Disk Access / 3 no Automation), and a
  `mail reference` command embedding the full contract. The skill must not
  duplicate that contract; it points to it.
- All target harnesses have converged on the **open Agent Skills standard**: a
  directory containing `SKILL.md` with YAML frontmatter (`name` +
  `description`). Codex and Gemini CLI additionally share the `.agents/skills/`
  discovery alias.
- The binary already embeds `internal/reference/reference.md` via `go:embed`;
  the skill follows the identical pattern, so the binary stays self-describing.
- `go install <module>@latest` compiles from the module proxy (no clone) but
  requires a public repo and a version tag. It installs only the binary — which
  is why the skill must be embedded and installed by the binary itself.

## 3. Deliverables (repo layout)

```
internal/skill/
  SKILL.md              # canonical skill, embedded (source of truth)
  skill.go              # go:embed + accessor (mirrors internal/reference)
  skill_test.go         # embed sanity test
cmd/mail/
  skill.go              # `mail install-skills` cobra command
.cursor/rules/
  mail.mdc              # Cursor project rule (description-driven)
AGENTS.md               # short always-on pointer for AGENTS.md readers
Makefile                # + install-skills / uninstall-skills targets
```

No `scripts/`, no new runtime dependencies (bash + markdown + Go).

## 4. The canonical skill — `internal/skill/SKILL.md`

Frontmatter (required by every harness):

```yaml
---
name: mail
description: >
  Search and analyze the local Apple Mail store with the `mail` CLI. Use when
  the user asks to read, search, summarize, export, or trash local email
  (senders like LinkedIn, invoices, receipts, newsletters), or mentions
  mail/email/INBOX. Read-only except `mail trash` (which only moves to Trash).
---
```

Body, in this order:

1. **What it is** — read-only macOS CLI, JSONL output, distinct exit codes.
2. **First run** — `mail doctor` to verify Full Disk Access (attaches to the
   terminal, not the binary).
3. **Commands** — one line each: `search`, `show`, `links`, `export`, `stats`,
   `trash`, `accounts`, `reference`. Shared filters (`--from`, `--to`,
   `--subject`, `--since`, `--until`, `--account`, `--limit`).
4. **Output** — JSONL, pipe to `jq`; `--format table` for humans.
5. **Safety rules** (must be unmissable):
   - Always `--dry-run` before `mail trash` batch; never trash without a filter
     or ROWID (`--limit 0` = unlimited would move everything).
   - `--limit 0` means unlimited and is the default everywhere except `search`.
   - Never open `action` links (unsubscribe/confirm) automatically.
   - The tool makes no network requests; fetch a URL only on explicit request.
   - `--unread` filters out read mail; don't add it when "all of X" is meant.
6. **Recipes** — the six from the reference doc.
7. **Full contract** — point to `mail reference` (do not inline the schema).

Keep `SKILL.md` under ~150 lines; the full schema stays in `mail reference`.

## 5. The installer — `mail install-skills`

A cobra subcommand (cross-platform, testable in Go). It reads the embedded
skill and **copies** it into each harness's discovery location. Copy, not
symlink: the source of truth is the binary, not a mutable repo, so re-running
after `go install @newversion` refreshes the skill.

| Harness | Location | Mechanism |
|---|---|---|
| Claude Code | `~/.claude/skills/mail/SKILL.md` | SKILL.md |
| opencode | `~/.config/opencode/skills/mail/SKILL.md` | SKILL.md |
| Codex | `~/.agents/skills/mail/SKILL.md` | SKILL.md (shared standard) |
| Gemini CLI | `~/.gemini/skills/mail/SKILL.md` | SKILL.md |

Behavior:

- Writes to all four locations unconditionally (a harness installed later finds
  the skill already present); reports what was written.
- Idempotent: re-running overwrites with the embedded (current) version.
- `mail install-skills --uninstall` removes the skill from all four locations.
- `mail install-skills --check` prints, per location, whether the skill is
  present and whether it matches the embedded copy.
- For Cursor: global rules have no standard filesystem location, so the
  installer does not push anything global. Instead the repo ships
  `.cursor/rules/mail.mdc`; the command prints a one-line note on using it in a
  project.

## 6. Secondary artifacts

- **`AGENTS.md`** (repo root): short always-on context — what `mail` is, that a
  skill exists, and the safety red lines. Read automatically by Codex, Cursor,
  opencode, and other AGENTS.md readers when this repo is opened.
- **`.cursor/rules/mail.mdc`**: Cursor project rule, frontmatter `description`
  + `alwaysApply: false` (description-driven activation).

## 7. Distribution

```bash
go install github.com/mirko/applemail/cmd/mail@latest
mail install-skills
```

`Makefile` gains two targets (developer convenience; both require `build`):

```make
install-skills:   ./mail install-skills
uninstall-skills: ./mail install-skills --uninstall
```

Prerequisite for `@latest`: the repo is public and carries a version tag
(e.g. `v0.1.0`). During development, `make build && ./mail install-skills`
achieves the same result. No curl, no clone, no third-party installer.

## 8. Verification

- `go test ./...` — embed sanity test (`internal/skill`) and `install-skills`
  behavior test.
- `make build && ./mail install-skills`, then confirm
  `~/.claude/skills/mail/SKILL.md` and `~/.config/opencode/skills/mail/SKILL.md`
  exist and match `internal/skill/SKILL.md`.
- Load an agent, ask "search my unread LinkedIn mail" — the skill must surface
  and the agent must run `mail search --from linkedin --unread`.
- `mail reference` still prints the full contract (no duplication drift).

## 9. Out of scope

- MCP server (structured tool-calling) — deferred to a follow-up.
- Homebrew formula/tap and plugin marketplaces (Claude Code, Codex) — later.
- Shipping `.agents/skills/mail/` in this repo for native in-repo discovery by
  Codex/Gemini — deferred (the global install covers it).
