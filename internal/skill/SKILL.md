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
