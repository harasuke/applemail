# Mail Trash — Design

**Date:** 2026-08-17
**Status:** Approved for planning
**Target:** macOS 26.5.2 (Tahoe), Go 1.26.5, arm64

## Purpose

Add the first mutating command to the otherwise read-only Apple Mail CLI:
`mail trash`, which moves messages to the Trash. This reverses the earlier
explicit decision to defer all writes ("Marking read/unread, flagging,
deleting" was out of scope) for exactly one operation, done safely through
Mail.app itself rather than by writing the Envelope Index directly.

## Background

The CLI reads metadata from the Envelope Index (`mode=ro&immutable=1`) and
bodies from `.emlx` files. Two facts shape this design:

1. **The Envelope Index `message_id` column is an internal ID, not the RFC
   Message-ID.** A real row value is `-1293673308154836185`. The RFC header
   (e.g. `<CAF...@mail.gmail.com>`) lives only inside the `.emlx` body and is
   already extracted by `internal/emlx` as `Message.MessageID`.

2. **AppleScript identifies a message by its RFC Message-ID**, via `message
   whose message id is "<...>"`, and `delete` on a non-trashed message moves
   it to the account's Trash. AppleScript/JXA is the only supported route for
   mutating Mail — the Envelope Index is Mail's own file, held under a write
   lock while Mail runs, and the design deliberately never opens it for
   writing.

## Scope

### In scope
- `mail trash <id>` — move a single message to Trash
- `mail trash` with the shared filter flags (`--from`, `--to`, `--subject`,
  `--mailbox`, `--since`, `--until`, `--unread`, `--flagged`,
  `--has-attachment`) — move a batch to Trash
- `--dry-run` — report what would be moved, without acting
- Clear handling of the Automation (Apple Events) permission failure

### Out of scope (unchanged)
- Permanent deletion (emptying Trash)
- Sending, moving to an arbitrary mailbox, flagging, marking read
- Direct writes to the Envelope Index or any file under `~/Library/Mail`

## Command surface

```
mail trash <id>
mail trash --from linkedin --since 30d --unread
mail trash --from X --dry-run
```

`trash` accepts the same filter flags as every other command. A bare
positional argument is a single ROWID. `--dry-run` changes only whether
AppleScript is invoked; the scan and selection are identical either way.

`trash` defaults to **unlimited** (`--limit 0`), matching `links`, `export`,
and `stats`, not `search`. A silent cap of 50 on a destructive command would
delete only the newest 50 matches while looking successful — the same
rationale that keeps the archive/aggregate commands uncapped. `trash` does
**not** take a body-text query: there is no `[query]` positional beyond the
single ROWID. Deleting by body text is out of scope; the existing `search`
pipeline already lets a caller find matches, and `trash` acts on what the
filters select.

## Data flow

```
Envelope Index (read-only)
    ↓  metadata filter (same as search/links/export/stats)
candidate ROWIDs
    ↓  single ID → Scanner.One (indexed lookup, no scan)
    ↓  batch      → Scanner.Run (parallel .emlx parse)
per message: RFC Message-ID from emlx.Message.MessageID
    ↓  --dry-run?  → emit JSONL list, stop
    ↓  else        → one AppleScript invocation per batch
Mail.app moves each message to its account Trash
```

The scan reuses `internal/scan` unchanged: it already parses bodies in
parallel and already surfaces the RFC `MessageID` (currently dropped when
building `output.Message`; `trash` consumes it directly from `emlx`).

## Components

### `internal/mailctl` (new)

The AppleScript bridge, isolated so everything else remains unit-testable
without touching Mail.

- `MoveToTrash(messageIDs []string) error` — moves every message whose RFC
  Message-ID is in the list to its account's Trash.

The script iterates accounts/mailboxes, matching `message id` against the
list, and issues one `delete` per match. It is invoked **once per batch**, not
once per message, because each `osascript` round-trip costs roughly
0.5–1 second.

Errors are classified:
- Automation permission missing (`-1743`, errAEEventNotPermitted) → a distinct
  typed error, surfaced with the System Settings → Privacy & Security →
  Automation path and the instruction to enable the terminal for Mail.
- A message ID not found in any mailbox → reported, not fatal: Mail may have
  already moved it, or the index was stale. The count is written to stderr.

### `cmd/mail/trash.go` (new)

Thin Cobra command, following the `search`/`links` pattern: bind the shared
filters, resolve single ID vs batch, run the scan, collect Message-IDs, then
either dry-run-emit or call `mailctl.MoveToTrash`. Output to stdout is JSONL
records describing each affected message (id, subject, sender, action); the
dry-run records carry `"dry_run": true`.

## Output

JSONL, consistent with the rest of the CLI. Each record:

```json
{
  "id": 48213,
  "message_id": "<CAF...@mail.gmail.com>",
  "subject": "…",
  "from": {"name": "LinkedIn Jobs", "address": "jobs-noreply@linkedin.com"},
  "action": "trash",
  "dry_run": false
}
```

Errors go to stderr. The stream stays pipeable.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success (including zero matches) |
| `1` | General error |
| `2` | Missing Full Disk Access (unchanged) |
| `3` | Missing Automation permission (new) |

A distinct code for Automation lets a script tell "no Apple Events grant"
from "no mail access" from "no results" without parsing text.

## Permissions

`trash` needs both existing grants to even read candidates — Full Disk Access
for `~/Library/Mail` — and a new one to act: **Automation**, allowing the
terminal to control Mail.app. On the `-1743` Apple Event error, print the
System Settings path and that the grant is per-terminal (same TCC responsible-
process model already documented for FDA), then exit `3`. The `doctor`
command is extended to optionally report whether Automation is available.

## Error handling

- **One unfound message must not fail a batch.** Missing/not-found message IDs
  are counted on stderr and skipped, matching the existing "one unreadable
  message never fails a scan" principle.
- **Stale index.** A message moved out of a mailbox between scan and AppleScript
  (or already deleted) resolves to "not found", not an error.
- **AppleScript unavailable.** If `osascript` is missing or the script errors
  for a non-permission reason, exit `1` with the raw cause; never attempt a
  fallback write to the Envelope Index.

## Testing

- **`internal/mailctl`**: the AppleScript generation is factored so the text of
  the script can be asserted without executing it (a pure "build script from
  IDs" function). The classifier that maps Apple Event error codes to typed
  errors is unit-tested with the documented codes.
- **`cmd/mail/trash.go`**: dry-run path is tested against the generated
  fixture store (same `testdata/` used by existing command tests), asserting
  emitted JSONL records and that no AppleScript is invoked.
- **Manual smoke test** against real mail: `mail trash <id> --dry-run`, then
  without the flag on a disposable message; confirm it lands in Trash in
  Mail.app. The only verification touching real data, run by the user.

## Open questions

None blocking. The Automation permission behavior is confirmed at first real
run; the design isolates the AppleScript bridge so an adjustment there does
not ripple outward.
