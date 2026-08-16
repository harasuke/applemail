# mail — Reference

`mail` is a read-only CLI for searching and analyzing Apple Mail, built to
be driven by scripts and LLM agents. Output is JSONL by default so it
streams and pipes cleanly; errors go to stderr, stdout stays parseable.

This document is the single source of truth for the tool's behavior. It is
embedded in the binary — run `mail reference` to read it.

## Requirements

- macOS with Apple Mail configured.
- **Full Disk Access** granted to the *terminal* application (not the
  `mail` binary). System Settings → Privacy & Security → Full Disk Access.
- **Automation** permission, for `mail trash`, `mail accounts`, and
  `--account`: the terminal must be allowed to control Mail (System Settings
  → Privacy & Security → Automation). Not required for other read commands.
- Run `mail doctor` first to verify access; `mail doctor --check-automation`
  verifies the trash path.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success (including zero results) |
| 1 | General error |
| 2 | Missing Full Disk Access |
| 3 | Missing Automation permission (trash, accounts, --account) |

A distinct code for each failure lets a script tell "no results" from "no
access" without parsing text.

## Shared filters

Every message-selecting command (`search`, `links`, `export`, `stats`,
`trash`) accepts the same flags:

```
--from, --to, --subject, --mailbox,
--since, --until,   (dates: YYYY-MM-DD or spans like 30d, 2w, 6m, 1y)
--unread, --flagged, --has-attachment,
--account, --limit
```

`--account` restricts results to one account, referenced by email address,
display name, or account identifier. It resolves the reference against Mail
(via AppleScript), so it requires Automation permission. `mail accounts`
lists the available accounts and their identifiers.

`--limit` defaults differ on purpose:
- `search`: 50 (interactive; a human reads it).
- `links`, `export`, `stats`, `trash`: **0 = unlimited**. A silent cap on
  these would truncate an archive or an aggregate and still look correct.

`--limit` counts **matches**, not candidates. With a body-text query on
`search`, the limit is applied after parsing, so it never silently misses
older matches.

## Commands

### `mail doctor [--check-automation]`
Verify access. Run this first. Reports the discovered version directory,
message/mailbox counts, and `.emlx` directory count. On permission failure,
explains how to grant access. `--check-automation` additionally probes
whether the terminal can control Mail (for `trash`).

### `mail accounts`
List the accounts configured in Mail: display name, email address(es), and
the identifier used by `--account`. Requires Automation permission. Output is
the same JSONL/table/text contract as the other commands.

```bash
mail accounts
mail accounts --format table
```

### `mail search [query]`
Metadata filters resolve instantly against Mail's index. A bare `[query]`
argument searches message *bodies*, which reads `.emlx` files from disk and
is slower. Filters compose.

```bash
mail search --from linkedin --since 30d
mail search "invoice" --since 90d
mail search --unread --limit 0 | jq -r '.subject'
```

### `mail show <id>`
One message, fully parsed, by ROWID (indexed lookup, no scan). `--raw`
dumps the original `.emlx` bytes untouched. `--max-body-chars N` truncates
the body (and sets `body.truncated`).

### `mail links`
Same filters. Emits every extracted URL (normalized, tracking stripped) with
its source message, domain, anchor text, and class. Default: `content` links
only; `--all` includes `tracking` and `action` links. `--group-by domain`
rolls up. **Never open `action` links automatically** (unsubscribe, confirm).

### `mail export --out <dir> [--as eml|json|mbox]`
Same filters. The only command that writes files, and only into the named
directory (refuses anything inside Mail's own data). `eml` (default): one
file per message; `json`: one analyzed record per message; `mbox`: a single
re-importable `archive.mbox`.

### `mail stats`
Corpus rollup over the filtered set: top senders, link domains, volume by
week, read/unread/flagged breakdown, thread count. Emits one `summary`
record as the final line.

### `mail trash [id]`
Move messages to **Trash** (recoverable, never permanent). Pass a single
ROWID, or use the shared filters for a batch. `--dry-run` reports what would
be moved without acting. Requires Automation permission. Trash/junk
mailboxes are skipped, so re-running is safe — a message already in Trash is
never permanently deleted. **Always `--dry-run` before a batch.**

`--verify` re-checks Mail (via AppleScript) after a real move and reports any
message still present in a non-Trash, non-Junk, non-"All Mail" mailbox. The
Envelope Index can lag behind Mail's own state, so `--verify` confirms the
move against Mail itself rather than the index. It has no effect with
`--dry-run`.

```bash
mail trash 48213
mail trash --from linkedin --since 30d
mail trash --from X --dry-run
mail trash --from linkedin --unread --verify
```

## Output format

`--format jsonl` (default), `json`, `table`, or `text`. JSONL is the
contract: one object per line, streamed. `json` buffers a single array.

### Message object

```json
{
  "id": 48213,
  "message_id": "<CAF...@mail.gmail.com>",
  "thread_id": 9912,
  "mailbox": "INBOX",
  "account": "example-account",
  "date": "2026-08-14T09:31:22Z",
  "from": {"name": "LinkedIn Jobs", "address": "jobs-noreply@linkedin.com"},
  "to": [{"name": "Recipient", "address": "user@example.com"}],
  "subject": "5 nuove posizioni per te",
  "flags": {"read": false, "flagged": false, "has_attachment": false},
  "body": {"text": "…", "truncated": false, "source": "text/plain"},
  "links": [],
  "attachments": [{"name": "cv.pdf", "mime": "application/pdf", "size": 84213}],
  "signals": {
    "is_bulk": true, "is_automated": true, "has_unsubscribe": true,
    "reply_to_differs": false, "spf_dkim_present": true
  },
  "error": "message body not found on disk (ROWID 48213)"
}
```

Field notes:
- `id` is the ROWID — the stable handle for `show` and `trash`.
- `body.text` is always plain text, never HTML. `body.source` records the
  origin (`text/plain`, `text/html`, or a `; charset-fallback=…` suffix).
- `body.truncated` is true when `--max-body-chars` cut the text.
- `error` is populated instead of `body` when a message could not be read;
  the scan continues and counts it on stderr.
- `signals` are facts, not judgments — there is deliberately no `is_spam`.

### Link object

```json
{
  "url_canonical": "https://www.linkedin.com/jobs/view/4021887364",
  "url_original": "https://www.linkedin.com/comm/jobs/view/4021887364?trk=…",
  "domain": "linkedin.com",
  "anchor_text": "Senior Backend Engineer — Milano",
  "class": "content",
  "dedup_key": "linkedin:job:4021887364",
  "message_id": 48213,
  "anchor_mismatch": false
}
```

- `class`: `content` (worth reading), `tracking` (pixels/beacons, filtered
  by default), `action` (performs something — never open automatically).
- `anchor_mismatch` is true when the visible text resembles a different
  domain than the href.
- `message_id` is the source message's ROWID, so a link can be traced back.

### Summary object (final line of `search --stats` / `stats`)

```json
{
  "type": "summary",
  "total": 342,
  "date_range": ["2026-05-01T00:00:00Z", "2026-08-16T00:00:00Z"],
  "top_senders": [{"key": "jobs-noreply@linkedin.com", "count": 87}],
  "top_domains": [{"key": "linkedin.com", "count": 91}],
  "volume_by_week": [{"key": "2026-W28", "count": 23}],
  "unread": 41, "flagged": 3, "with_attachments": 12,
  "threads": 218, "skipped": 0
}
```

`"type": "summary"` is the unambiguous discriminator for the final line of a
stream.

## Gotchas

- **`--unread` filters out read messages.** If you intend "all of X", do not
  add `--unread`.
- **`--limit 0` means unlimited**, and is the default everywhere except
  `search`. Add it explicitly when you want everything.
- **`trash` moves to Trash, not permanent delete.** The Envelope Index keeps
  showing trashed messages (under their trash mailbox) until Mail
  checkpoints; this is not a bug.
- **`message_id` in a Message is Mail's internal index ID, not the RFC
  header.** Don't use it to correlate with external systems; use `id` (ROWID)
  within this tool.
- **Never `trash` without `--dry-run` first** for a batch, and never run it
  without a filter or ROWID — the unlimited default would move everything.
- **Full Disk Access attaches to the terminal**, not the binary. If
  `mail doctor` reports a permission error, grant it to the terminal app.
- **The tool makes no network requests.** Links are normalized offline; an
  agent may fetch a URL only on explicit user request.

## Recipes for LLM agents

Find recent unread mail from a sender:
```bash
mail search --from linkedin --unread --limit 0 | jq -r '.subject'
```

Read one message in full:
```bash
mail show 48213 | jq -r '.body.text'
```

Extract clean, fetchable URLs from a newsletter:
```bash
mail links --from linkedin | jq -r '.url_canonical'
```

Corpus overview of the last 6 months:
```bash
mail stats --since 6m
```

Preview a batch deletion, then perform it:
```bash
mail trash --from linkedin --dry-run
mail trash --from linkedin
```

Archive a year of mail as a re-importable mbox:
```bash
mail export --out ./archive --since 1y --as mbox
```
