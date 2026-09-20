# mail — read-only Apple Mail CLI

Search and analyze your local Apple Mail from the terminal. Streams
machine-readable JSONL for piping into scripts, `jq`, and LLM agents.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg)](https://go.dev/)

`mail` reads Apple Mail's on-disk store — the Envelope Index and `.emlx`
messages — without ever writing to it. It is built to be driven by other
programs: JSONL-by-default output, distinct exit codes, and a full reference
guide embedded in the binary.

## Install

### go install (recommended)

```bash
go install github.com/harasuke/applemail/cmd/mail@latest
mail setup   # interactive wizard: grant macOS permissions + install the agent skill
```

Requires Go 1.26+ and a Mac with Apple Mail configured.

### Build from source

```bash
make build      # produces ./mail
make install    # copies the binary to /usr/local/bin + the manpage
```

## Agent skills

`mail` installs a skill that teaches AI agents how to drive it safely:

```bash
mail install-skills            # install the skill into Claude Code, opencode, Codex, Gemini CLI
mail install-skills --check    # verify what is installed
mail install-skills --uninstall
mail setup                     # full wizard: macOS permissions + harness selection
```

Cursor users: copy `.cursor/rules/mail.mdc` into the project, or rely on
`AGENTS.md`.

## Quick start

```bash
mail doctor                              # verify access; run this first
mail search --from linkedin --since 30d  # who emailed you from LinkedIn
mail search "invoice" --since 90d        # full-body text search
mail show 48213                          # one message in full
mail stats --since 6m                    # corpus overview
```

## Requirements

macOS with Apple Mail configured, and **Full Disk Access granted to your
terminal application**.

macOS protects `~/Library/Mail`. Access is evaluated against the
responsible process — your terminal — not against this binary, and adding a
bare CLI binary to the Full Disk Access list does not work on recent macOS.

1. System Settings → Privacy & Security → Full Disk Access
2. Enable your terminal (Terminal, iTerm, …)
3. Quit and restart the terminal completely
4. Run `mail doctor`

`mail trash`, `mail accounts`, and the `--account` filter additionally
require **Automation** permission, so macOS lets the terminal control Mail.
Grant it when prompted, or run `mail doctor --check-automation` to verify.

## Commands

```bash
mail doctor                                  # verify access; run this first
mail reference                               # print the full embedded guide
mail accounts                                # list configured accounts (name/email/id)
mail search --from linkedin --since 30d      # metadata filters (instant)
mail search "invoice" --since 90d            # body text search
mail search --account omnys --unread         # restrict to one account
mail show 48213                              # one message in full
mail show 48213 --raw                        # the original .emlx
mail trash 48213                             # move one message to Trash
mail trash --from linkedin --since 30d       # move a batch
mail trash --from X --dry-run                # preview without acting
mail trash --from linkedin --unread --verify # move, then confirm against Mail
mail links --from linkedin.com               # clean, fetchable URLs
mail links --group-by domain                 # who links you where
mail export --out ./archive --since 1y       # write .eml files
mail export --out ./bk --as mbox             # single re-importable archive
mail stats --since 6m                        # aggregate view
```

All commands share the same filters: `--from`, `--to`, `--subject`,
`--mailbox`, `--account`, `--since`, `--until`, `--unread`, `--flagged`,
`--has-attachment`, `--limit`.

`--account` restricts results to one account, referenced by email address,
display name, or identifier (see `mail accounts`). It resolves the reference
against Mail and therefore requires Automation permission.

## Documentation

- `mail reference` — prints the full reference guide (commands, output
  schema, exit codes, gotchas, recipes). It is embedded in the binary, so it
  is always in sync with the version you are running.
- `man mail` — the manpage, installed by `make install` alongside the binary.

## Output

JSONL by default — one object per line, streamed as results are parsed.
`--format json` buffers a single array; `table` and `text` are for reading.

Errors go to stderr, so stdout stays pipeable:

```bash
mail search --since 7d | jq -r '.subject'
mail links --from linkedin | jq -r '.url_canonical'
```

Exit codes: `0` success, `1` error, `2` Full Disk Access missing,
`3` Automation permission missing.

## Design notes

**Read-only.** Mail's database is opened `mode=ro`. Nothing under
`~/Library/Mail` is ever written. `export` is the only command that
creates files, and it refuses to write inside the Mail directory.

**No network requests.** Tracking parameters are stripped and redirect
wrappers unwrapped offline, from the URL itself. This tool never fetches a
link — following a tracking URL would signal mail opens and leak activity.
Fetching is left to whoever consumes the output, on an explicit request.

**Signals are facts.** `is_bulk` means a `List-Unsubscribe` or
`Precedence: bulk` header exists. There is no `is_spam` or `is_interesting`
field: that judgment belongs to the reader.

## Not supported

Sending, creating mailboxes, flagging, and deleting are deliberately out of
scope. This tool reads — except `mail trash`, which moves messages to the
Trash (recoverable, never a permanent delete).

## License

[MIT](LICENSE)
