# mail

A read-only CLI for searching and analyzing Apple Mail, built for piping
into scripts and LLM agents.

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

## Install

```bash
make build            # produces ./mail
make install          # copies the binary to /usr/local/bin + the manpage
```

## Documentation

- `mail reference` — prints the full reference guide (commands, output
  schema, exit codes, gotchas, recipes). It is embedded in the binary, so it
  is always in sync with the version you are running.
- `man mail` — the manpage, installed by `make install` alongside the binary.

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
