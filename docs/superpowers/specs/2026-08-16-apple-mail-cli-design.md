# Apple Mail CLI — Design

**Date:** 2026-08-16
**Status:** Approved for planning
**Target:** macOS 26.5.2 (Tahoe), Go 1.26.5, arm64

## Purpose

A read-only command-line tool that reads, searches, and analyzes Apple Mail
messages, emitting LLM-consumable structured data. Distributed as a single
static binary so it can be copied to a new machine without a runtime.

The primary consumer is an LLM agent: the CLI extracts message content and
clean, fetchable links; the agent reads the pages behind those links and
performs the interpretation.

## Background: how Apple Mail data is reachable

There is no public Apple API for reading Mail. `MailKit` builds extensions
that run *inside* Mail.app; `MessageUI` is iOS-only and compose-only. Two
routes exist:

1. **Envelope Index** — a SQLite database of message metadata at
   `~/Library/Mail/V<N>/MailData/Envelope Index`. Fast (~30ms queries).
   Contains no message bodies.
2. **AppleScript / JXA** via `osascript` — the only route that can mutate or
   send. 100–1000x slower. Not used in this design.

Message bodies live in one `.emlx` file per message under
`~/Library/Mail/V<N>/<UUID>/…/*.mbox/…/Messages/<ROWID>.emlx`.

Because Mail maintains no queryable body index, full-text search requires
reading `.emlx` files directly.

### Key facts established during research

- **Version directory varies.** Sonoma/Sequoia used `V10`; Tahoe reports
  `V11` or `V12`. Must be discovered at runtime, never hardcoded.
- **Cocoa epoch.** Envelope Index timestamps are seconds since 2001-01-01.
  Add `978307200` to convert to Unix epoch.
- **Mail holds a write lock.** Open read-only with
  `file:<path>?mode=ro&immutable=1`.
- **`.emlx` format** is three parts: a decimal bytecount line terminated by
  `0x0a`, then RFC-822 MIME content of exactly that length, then an Apple
  plist trailer.
- **TCC attribution.** Full Disk Access attaches to the *responsible
  process* — the terminal emulator — not the invoked binary. On macOS 26.1+
  there are reports that granting FDA to a bare (non-`.app`) CLI binary does
  not work. Granting it to the terminal is the supported path.

## Scope

### In scope
- Read and search metadata (sender, recipient, subject, date, mailbox, flags)
- Full-text search over message bodies, read live from `.emlx`
- Content analysis: decoded plain text, link extraction, derived signals
- Link normalization, canonicalization, deduplication, and classification
- Corpus-level aggregation across a result set
- Export to `.eml` / `.mbox` / JSON

### Out of scope (deferred by explicit decision)
- Sending mail
- Moving messages, creating mailboxes or labels
- Marking read/unread, flagging, deleting
- Any write under `~/Library/Mail`
- Any network request made by the CLI itself

### Deferred but designed for
- **MCP server.** The core is structured so an MCP wrapper is a thin later
  addition (`cmd/mail-mcp`) over the same internal packages. Not built now:
  CLI ergonomics and pagination discipline should be validated by real use
  first, and FDA attribution is harder to diagnose under an MCP host.
- **Body cache.** At the target scale (<10k messages) live scanning is fast
  enough. If the mailbox grows, a SQLite FTS5 cache can be added behind the
  same interfaces without changing the command surface.

## Architecture

Single Go module, binary named `mail`.

```
cmd/mail/           Cobra commands (thin: flags in, stream out)
internal/mailstore/ Envelope Index reader: V* discovery, read-only SQLite,
                    metadata queries, .emlx path resolution
internal/emlx/      .emlx parser: bytecount → MIME → plist trailer
internal/analyze/   text extraction, link extraction/classification, signals
internal/corpus/    aggregate rollups over a result set
internal/output/    JSONL / JSON / table / text renderers
```

### Data flow

```
Envelope Index (SQLite, read-only)
    ↓  metadata filter: sender / date / mailbox / subject / flags
candidate ROWIDs + mailbox URLs
    ↓  resolve to .emlx paths (cached directory map, ~10–50 dirs)
    ↓  worker pool (GOMAXPROCS) reads + parses in parallel
per-message: headers, plain text, links, signals
    ↓  optional body-text filter applied here
    ↓  emit immediately → JSONL on stdout
    ↓  (if --stats) accumulate rollup → final summary object
```

### Design principles

**Read-only, always.** Mail's database is opened `mode=ro&immutable=1`.
Nothing under `~/Library/Mail` is ever written. All output goes to stdout or
to a path the user names.

**Runtime version discovery.** Glob `~/Library/Mail/V*/MailData/Envelope
Index`, select the highest version present. Survives OS upgrades.

**Streaming by default.** Results emit as parsed. Flat memory regardless of
mailbox size; first result is immediate; `--limit` short-circuits the walk.

**`--limit` counts matches, not candidates.** When a body-text query is
active, the limit must NOT be pushed into SQL — doing so would scan only
the newest N messages and silently report "no matches" for everything
older. With a body query, the scan walks as far as needed and stops once N
messages have actually matched.

**Parallel reads.** `.emlx` parsing is IO-bound and independent per message;
a worker pool sized to `GOMAXPROCS` handles the scan. Output ordering is
preserved by sequencing emission, not by serializing the reads.

## Commands

All commands stream JSONL by default and accept
`--format table|json|jsonl|text`.

A single shared filter struct backs every command, so any filter valid for
`search` is valid for `links`, `export`, and `stats` with identical
semantics.

### `mail doctor`
Reports: discovered `V*` directory, Envelope Index readability, message and
mailbox counts, `.emlx` directory count. On permission failure, names the
terminal application and the exact System Settings path — printed once, to
stderr. Run this first; it determines whether anything else can work.

Mail.app's own version is deliberately not reported: reading it requires
access this tool does not otherwise need.

### `mail search [query]`
Metadata filters resolve in SQL and are instant:
`--from`, `--to`, `--subject`, `--mailbox`, `--since`, `--until`,
`--unread`, `--flagged`, `--has-attachment`.

A bare `[query]` argument searches body text, triggering the `.emlx` read
pass. `--limit` defaults to 50 here and counts matches. Filters compose.

**`--limit` defaults per command:** 50 for `search` (an interactive
command whose output a human reads); **0, meaning unlimited, for `links`,
`export`, and `stats`** — those either archive or aggregate, and a silent
cap of 50 would corrupt the result while looking successful.

### `mail show <id>`
One message, fully parsed: headers, decoded plain text, links, attachment
list. Body content and link list are emitted as distinct blocks so the
message can be assessed independently of its links. `--raw` dumps the
original `.emlx` untouched.

`show` resolves the message by ROWID through an indexed query — it must
never scan the mailbox to find one message, since an agent calls this
command per message in a loop.

### `mail links`
Same filter flags as `search`. Emits every extracted URL with its source
message, canonical and original forms, domain, anchor text, and
classification. `--group-by domain` rolls up. Default output is `content`
links only; `--all` includes tracking and action links.

### `mail export`
Same filters. Writes to a user-named directory in one of three formats via
`--as`:

- `eml` (default) — one file per message, openable directly
- `json` — one file per message, the full analyzed record
- `mbox` — a single `archive.mbox` file holding every message, separated by
  `From ` lines. This is the interchange format Apple Mail, Thunderbird,
  and Gmail Takeout import, so it is the format for a re-importable backup

This is the only command that writes, and only where directed. It refuses
to write anywhere inside the Mail directory.

### `mail stats`
Corpus rollup over a filtered set: top senders, domain frequencies, volume
over time, read/unread/flagged breakdown, thread clusters.

## Output format

**JSONL is the contract.** One JSON object per line, no wrapping array —
this is what makes streaming possible. `--format json` produces a complete
array for `jq` convenience at the cost of buffering everything. `table` and
`text` are human-facing.

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
  "body": {
    "text": "…decoded, cleaned text…",
    "truncated": false,
    "source": "text/plain"
  },
  "links": [ /* link objects */ ],
  "attachments": [{"name": "cv.pdf", "mime": "application/pdf", "size": 84213}],
  "signals": {
    "is_bulk": true,
    "is_automated": true,
    "has_unsubscribe": true,
    "reply_to_differs": false,
    "spf_dkim_present": true
  }
}
```

**`body.text` is always text, never HTML.** HTML-only messages are converted
(tags stripped, entities decoded, whitespace collapsed) and the origin is
declared in `body.source`. An LLM should not spend tokens on markup.

**`signals` are facts, not judgments.** `is_bulk` derives from the presence
of `List-Unsubscribe` / `Precedence: bulk` headers, not from heuristics.
There is deliberately no `is_spam` or `is_interesting` field — that
assessment belongs to the LLM, and encoding it here would assert a certainty
the tool does not have.

**`body.truncated`** is set when `--max-body-chars` (default 0 = unlimited)
cuts the text. A declared truncation is preferable to a silent one or to an
overflowing context window.

### Link object

```json
{
  "url_canonical": "https://www.linkedin.com/jobs/view/4021887364",
  "url_original": "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE...&eid=...",
  "domain": "linkedin.com",
  "anchor_text": "Senior Backend Engineer — Milano",
  "class": "content",
  "dedup_key": "linkedin:job:4021887364",
  "message_id": 48213,
  "anchor_mismatch": false
}
```

**Classification** (`class`):
- `content` — resolves to a real page; what the LLM should read
- `tracking` — pixels, beacons, analytics redirects; filtered by default
- `action` — links that *perform* something (unsubscribe, confirmation,
  one-click); never to be opened automatically, always surfaced explicitly

**`anchor_mismatch`** is true when the link's visible text resembles a
different domain than the actual href. Reported as a fact, not labeled
"suspicious".

### Link normalization

Bulk senders wrap nearly every URL in tracking redirects, so a naive
extractor returns noise. Normalization is therefore core, not cosmetic:

1. Strip known tracking parameters: `trk`, `trkEmail`, `midToken`, `eid`,
   `lipi`, `refId`, `utm_*`
2. Unwrap redirect wrappers when the destination is present in the query
   string
3. Derive a canonical ID where the pattern is recognizable — for LinkedIn,
   the numeric job ID from `/jobs/view/<id>` — producing `dedup_key`

The same job posting arriving in three separate emails collapses to one row,
making repeat exposure visible.

`url_original` is always retained alongside `url_canonical` as a fallback,
since canonicalized URLs may require authentication where the tracked
original would not.

**The CLI makes no network requests.** Following a tracking redirect would
signal mail opens, may invalidate tokens, and would turn a local tool into
one that talks to third parties without the user's knowledge. Everything
reconstructible from the URL parameters is reconstructed offline; anything
requiring a fetch is left to the agent, acting on explicit user request.

### Corpus summary

Emitted as the **final line**, tagged `"type": "summary"` so a streaming
consumer can identify it unambiguously.

```json
{
  "type": "summary",
  "total": 342,
  "date_range": ["2026-05-01T00:00:00Z", "2026-08-16T00:00:00Z"],
  "top_senders": [{"key": "jobs-noreply@linkedin.com", "count": 87}],
  "top_domains": [{"key": "linkedin.com", "count": 91}],
  "volume_by_week": [{"key": "2026-W28", "count": 23}],
  "unread": 41, "flagged": 3, "with_attachments": 12,
  "threads": 218
}
```

### Streams and exit codes

Errors go to stderr, never stdout, so stdout remains pure pipeable JSONL
under all conditions.

| Code | Meaning |
|------|---------|
| `0` | Success (including zero results) |
| `1` | General error |
| `2` | Missing permissions (Full Disk Access) |

A distinct code for permissions lets a script tell "no results" from "no
access" without parsing text.

## Permissions

`~/Library/Mail` is TCC-protected. On the target machine it currently
returns `Operation not permitted` — this is the starting state and the first
thing the user will encounter, not an edge case.

**Preflight on every command.** Before opening anything, `stat` the Mail
directory. On `EPERM`, exit `2` with a message stating: that the *terminal
application* needs the grant (not the `mail` binary — the responsible
process is what TCC evaluates), the exact path in System Settings → Privacy
& Security → Full Disk Access, and that the terminal must be restarted
afterward. Never surface a raw SQLite `unable to open database file`.

**Distinguishing failures:**
- `EPERM` → Full Disk Access missing
- `ENOENT` on `~/Library/Mail` → Mail was never configured; different message
- No `V*` directory found → unanticipated macOS layout; list what was found
  and ask for an issue rather than reporting an empty mailbox

## Error handling

**One unreadable message must never fail a scan over thousands.**

Missing `.emlx` (message deleted after indexing), malformed MIME,
unrecognized charset, inconsistent bytecount — each produces an output row
with `"error"` populated instead of a body, and the scan continues. A count
is written to stderr on completion:
`3 messages skipped (2 missing files, 1 parse error)`.

**Locking.** `mode=ro&immutable=1` avoids contention with Mail by
construction. If opening still fails, the message suggests quitting Mail.

**Charset handling.** Declared charsets are decoded through the IANA
registry (`golang.org/x/text/encoding/ianaindex`), which covers the
ISO-8859 family, KOI8-R, Shift_JIS, GB2312, EUC-KR, and the Windows-125x
family. Only when a label is unrecognized or its decode fails does the
chain fall back: UTF-8 if the bytes are already valid, then latin-1, which
maps every byte and therefore never fails.

**A fallback is declared, never silent.** When the chain falls back,
`body.source` records it — `text/plain; charset-fallback=latin-1` — so a
consumer can tell decoded text from best-effort text. Imperfect but
declared beats both an error and silent mojibake.

## Testing

**Constraint:** the tool cannot be tested reproducibly against real mail —
the data is private and mutable.

**Generated fixtures.** Synthetic `.emlx` files and a synthetic Envelope
Index SQLite database in `testdata/`, matching the real schema with invented
data. Coverage: multipart/alternative, HTML-only, attachments, encoded-word
headers (`=?UTF-8?B?...`), quoted-printable, exotic charsets, truncated
files, incorrect bytecount. These run anywhere — no FDA, no Mail installed,
CI-safe.

**TDD applies to the components that fail most often:**
- `.emlx` parser (bytecount / MIME / plist boundaries)
- URL normalization and dedup (real-but-anonymized LinkedIn cases)
- HTML → text extraction
- Cocoa epoch conversion
- `V*` directory discovery

**Manual smoke test** against real mail: `mail doctor`, then `search
--limit 5`. The only verification touching real data; run by the user. Its
purpose is to confirm that the reconstructed schema and path conventions
match the actual Tahoe 26.5.2 layout.

**Known gap:** fixtures reproduce the schema as reconstructed from research,
not the schema present on the target machine — the permission barrier
prevented direct inspection during design. The first `mail doctor` run is
the moment of truth; if the real schema differs, it is discovered there.

## Open questions

None blocking. The schema-verification gap above is resolved by the first
`doctor` run against real data, and the design isolates schema knowledge in
`internal/mailstore` so a correction there does not ripple outward.
