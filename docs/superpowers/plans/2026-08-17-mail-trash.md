# Mail Trash Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `mail trash`, the first mutating command: move messages to Trash via AppleScript, with `--dry-run` and a distinct Automation-permission exit code.

**Architecture:** A new `internal/mailctl` package wraps the AppleScript bridge (build script from RFC Message-IDs → one `osascript` call per batch → classify Apple Event errors). The command reuses the existing scan pipeline unchanged; the RFC `Message-ID` header is surfaced from `emlx` (where it already lives) through a new non-serialized field on `output.Message`. A new `output.Trash` record is emitted per affected message.

**Tech Stack:** Go 1.26.5, Cobra, `osascript` (AppleScript), existing `internal/scan` / `internal/output` / `internal/emlx`.

**Spec:** `docs/superpowers/specs/2026-08-17-mail-trash-design.md`

## Global Constraints

- **Go 1.26.5**, module path `github.com/mirko/applemail`. Binary name `mail`.
- **Read-only Envelope Index always.** `trash` never writes the DB or any file under `~/Library/Mail`. Mutation goes through `osascript` → Mail.app only.
- **The Envelope Index `message_id` column is an internal ID** (e.g. `-1293673308154836185`), NOT the RFC header. The RFC `Message-ID` (e.g. `<CAF...@mail.gmail.com>`) is extracted by `internal/emlx` into `Message.MessageID`. AppleScript addresses messages by the RFC header.
- **stdout is pure JSONL.** Errors, skip counts, and not-found counts go to stderr.
- **Exit codes:** `0` success, `1` general error, `2` missing Full Disk Access, `3` missing Automation permission (new).
- **One unfound message must never fail a batch.** Missing/not-found IDs are counted on stderr and skipped.
- **`--limit` default for `trash` is 0 (unlimited),** like `links`/`export`/`stats`, not `search`. A silent cap on a destructive command would delete only the newest matches while looking successful.
- **No body-text query on `trash`.** The only positional is a single ROWID.
- **One `osascript` invocation per batch**, never one per message.
- **SQLite driver name is `"sqlite"`** (modernc), not `"sqlite3"`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mailctl/mailctl.go` | AppleScript bridge: `buildScript`, `MoveToTrash`, `CheckAutomation`, error classification |
| `internal/mailctl/mailctl_test.go` | Script-text and classification tests (no real osascript) |
| `internal/output/types.go` | Add `MessageIDHeader` field (json:"-") to `Message`; add `Trash` record type |
| `internal/output/render.go` | Add `Trash` cases to table and text renderers |
| `internal/scan/scan.go` | Populate `Message.MessageIDHeader` from `decoded.MessageID` |
| `internal/scan/scan_test.go` | Assert the RFC header is surfaced |
| `cmd/mail/trash.go` | `trash` command: single ID + batch, dry-run, not-found reporting |
| `cmd/mail/trash_test.go` | Command tests against fixture store (dry-run and batch paths) |
| `cmd/mail/context.go` | Add `exitAutomation` code, `automationMessage` helper, `exitCodeFor` branch |
| `cmd/mail/main.go` | Print `automationMessage` on `ErrAutomationDenied` |
| `cmd/mail/doctor.go` | Optional `--check-automation` flag |
| `cmd/mail/root.go` | Add `flagDryRun` and `flagCheckAutomation` globals |
| `cmd/mail/commands_test.go` | Reset new flags in `resetFlags` |
| `README.md` | Document `mail trash` |

---

### Task 1: Add `internal/mailctl` — the AppleScript bridge

**Files:**
- Create: `internal/mailctl/mailctl.go`
- Test: `internal/mailctl/mailctl_test.go`

**Interfaces:**
- Consumes: nothing (standalone package).
- Produces:
  - `ErrAutomationDenied` (`error`) — sentinel for the `-1743` Apple Event error.
  - `MoveToTrash(messageIDs []string) (notFound int, err error)` — moves messages to Trash; returns count of IDs that matched no message.
  - `CheckAutomation() error` — verifies the terminal can control Mail.
  - `buildScript() string` — the AppleScript template (exported for tests).

- [ ] **Step 1: Write the failing test**

Create `internal/mailctl/mailctl_test.go`:

```go
package mailctl

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildScriptContainsTrashMechanics(t *testing.T) {
	s := buildScript()
	for _, want := range []string{
		"on run argv",
		"message id",
		"deleted status",
		"delete eachMessage",
		"every mailbox",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("buildScript() is missing %q:\n%s", want, s)
		}
	}
}

func TestBuildScriptPassesIDsAsArgumentsNotInterpolated(t *testing.T) {
	s := buildScript()
	// IDs must arrive via argv, never be baked into the source, so the
	// script is identical for every call and safe from injection.
	if strings.Contains(s, "<fixture-") {
		t.Errorf("buildScript() should not hardcode any message id:\n%s", s)
	}
	if !strings.Contains(s, "msgIDs") || !strings.Contains(s, "argv") {
		t.Errorf("buildScript() must reference argv:\n%s", s)
	}
}

func TestClassifyAutomationDenied(t *testing.T) {
	err := classify("execution error: Not authorized to send Apple events to Mail. (-1743)", errors.New("exit status 1"))
	if !errors.Is(err, ErrAutomationDenied) {
		t.Errorf("classify(-1743) = %v, want ErrAutomationDenied", err)
	}
}

func TestClassifyGenericErrorKeepsMessage(t *testing.T) {
	err := classify("execution error: Some other failure. (-1)", errors.New("exit status 1"))
	if errors.Is(err, ErrAutomationDenied) {
		t.Errorf("classify(generic) = %v, want a non-automation error", err)
	}
	if !strings.Contains(err.Error(), "Some other failure") {
		t.Errorf("classify(generic) = %v, want the script stderr preserved", err)
	}
}

func TestClassifyNilRunErrorIsNil(t *testing.T) {
	if err := classify("", nil); err != nil {
		t.Errorf("classify(nil) = %v, want nil", err)
	}
}

func TestMoveToTrashEmptyListIsNoop(t *testing.T) {
	orig := runOsaScript
	runOsaScript = func(script string, args []string) (string, string, error) {
		t.Fatal("runOsaScript called for an empty id list")
		return "", "", nil
	}
	defer func() { runOsaScript = orig }()

	notFound, err := MoveToTrash(nil)
	if err != nil || notFound != 0 {
		t.Errorf("MoveToTrash(nil) = (%d, %v), want (0, nil)", notFound, err)
	}
}

func TestMoveToTrashReturnsNotFoundCount(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	var gotArgs []string
	runOsaScript = func(script string, args []string) (string, string, error) {
		gotArgs = append([]string{}, args...)
		return "3", "", nil // 3 of 5 matched
	}

	ids := []string{"<a@x>", "<b@x>", "<c@x>", "<d@x>", "<e@x>"}
	notFound, err := MoveToTrash(ids)
	if err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	if notFound != 2 {
		t.Errorf("notFound = %d, want 2", notFound)
	}
	if len(gotArgs) != 5 {
		t.Fatalf("got %d args, want 5", len(gotArgs))
	}
	if gotArgs[0] != "<a@x>" {
		t.Errorf("first arg = %q, want the first id", gotArgs[0])
	}
}

func TestMoveToTrashSurfacesAutomationError(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	runOsaScript = func(script string, args []string) (string, string, error) {
		return "", "execution error: Not authorized (-1743)", errors.New("exit status 1")
	}

	_, err := MoveToTrash([]string{"<a@x>"})
	if !errors.Is(err, ErrAutomationDenied) {
		t.Errorf("err = %v, want ErrAutomationDenied", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailctl/...`
Expected: FAIL — `undefined: buildScript`, `undefined: classify`, `undefined: MoveToTrash`, etc.

- [ ] **Step 3: Write the implementation**

Create `internal/mailctl/mailctl.go`:

```go
// Package mailctl drives Apple Mail via AppleScript for the few operations
// this tool performs that mutate mail (moving messages to Trash).
package mailctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrAutomationDenied means macOS refused the Apple Event: the terminal
// lacks Automation permission for Mail.
var ErrAutomationDenied = errors.New("automation permission denied for Mail")

// buildScript returns the AppleScript that moves every message whose RFC
// Message-ID is in argv to the Trash. It is a fixed template — the IDs are
// passed as arguments, never interpolated into the source — so the text can
// be asserted in tests without executing it.
//
// For each account it walks the mailbox tree (top-level mailboxes and their
// nested children), collecting every message whose Message-ID matches the
// argv list. A message already in the Trash (deleted status true) is left
// alone, so "trash" never becomes a permanent delete. The script returns the
// number of matched messages.
func buildScript() string {
	return `on run argv
	set msgIDs to argv
	set found to {}
	tell application "Mail"
		repeat with eachAccount in accounts
			repeat with eachMailbox in every mailbox of eachAccount
				set found to found & my matchingMessages(eachMailbox, msgIDs)
			end repeat
		end repeat
		repeat with eachMessage in found
			if (deleted status of eachMessage) is false then
				delete eachMessage
			end if
		end repeat
		return (count of found)
	end tell
end run

on matchingMessages(theBox, msgIDs)
	set results to {}
	tell application "Mail"
		set results to (every message of theBox whose message id is in msgIDs)
		set children to every mailbox of theBox
	end tell
	repeat with child in children
		set results to results & my matchingMessages(child, msgIDs)
	end repeat
	return results
end matchingMessages
`
}

// runOsaScript executes an AppleScript with argv and returns its stdout,
// stderr, and any exec error. It is a package variable so tests can
// substitute a fake without invoking osascript.
var runOsaScript = func(script string, args []string) (stdout, stderr string, err error) {
	f, err := os.CreateTemp("", "mailctl-*.scpt")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}

	cmd := exec.Command("osascript", append([]string{f.Name()}, args...)...)
	var out, e strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &e
	err = cmd.Run()
	return out.String(), e.String(), err
}

// classify maps an osascript failure to a typed error. The -1743 Apple
// Event error is Automation permission missing; everything else stays a
// plain error carrying the script's stderr.
func classify(stderr string, runErr error) error {
	if runErr == nil {
		return nil
	}
	if strings.Contains(stderr, "-1743") ||
		strings.Contains(strings.ToLower(stderr), "not authorized") {
		return ErrAutomationDenied
	}
	return fmt.Errorf("osascript: %v: %s", runErr, strings.TrimSpace(stderr))
}

// MoveToTrash moves every message whose RFC Message-ID is in messageIDs to
// the Trash in a single AppleScript invocation. It returns the number of
// requested IDs that matched no message — counted, never fatal, since Mail
// may already have moved them or the index was stale.
func MoveToTrash(messageIDs []string) (notFound int, err error) {
	if len(messageIDs) == 0 {
		return 0, nil
	}
	stdout, stderr, err := runOsaScript(buildScript(), messageIDs)
	if err != nil {
		return 0, classify(stderr, err)
	}
	matched, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil {
		return 0, fmt.Errorf("parse osascript result %q: %w", stdout, err)
	}
	return len(messageIDs) - matched, nil
}

// CheckAutomation verifies the terminal can send Apple events to Mail
// without side effects. It returns nil when allowed, ErrAutomationDenied
// otherwise. Used by "mail doctor --check-automation".
func CheckAutomation() error {
	_, stderr, err := runOsaScript(`on run
tell application "Mail" to get version
return "ok"
end run
`, nil)
	if err != nil {
		return classify(stderr, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailctl/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mailctl/mailctl.go internal/mailctl/mailctl_test.go
git commit -m "feat: add mailctl AppleScript bridge for moving messages to Trash"
```

---

### Task 2: Surface the RFC Message-ID through `output.Message` and add the `Trash` record

**Files:**
- Modify: `internal/output/types.go`
- Modify: `internal/output/render.go`
- Modify: `internal/scan/scan.go:228-242` (set the field in `buildOne`)
- Test: `internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `emlx.Message.MessageID` (already populated by `emlx.Extract`).
- Produces:
  - `output.Message.MessageIDHeader string` (json:"-") — the RFC header.
  - `output.Trash` record type:
    ```go
    type Trash struct {
        ID        int64   `json:"id"`
        MessageID string  `json:"message_id"` // RFC 822 Message-ID header
        Subject   string  `json:"subject"`
        From      Address `json:"from"`
        Action    string  `json:"action"` // always "trash"
        DryRun    bool    `json:"dry_run"`
    }
    ```

- [ ] **Step 1: Write the failing test**

Append to `internal/scan/scan_test.go`:

```go
func TestRunSurfacesRFCMessageID(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{Filter: mailstore.Filter{Subject: "Plain text hello"}})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// The RFC Message-ID comes from the .emlx header, not the Envelope
	// Index message_id column (which the fixture stores as
	// <fixture-1@example.com>).
	if got[0].MessageIDHeader != "<plain-1@example.com>" {
		t.Errorf("MessageIDHeader = %q, want the RFC header <plain-1@example.com>", got[0].MessageIDHeader)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scan/... -run TestRunSurfacesRFCMessageID`
Expected: FAIL — `got[0].MessageIDHeader undefined (type output.Message has no field or method MessageIDHeader)`.

- [ ] **Step 3: Write the implementation**

In `internal/output/types.go`, add the field to `Message` (after the `MessageID` field, ~line 58) and add the `Trash` type at the end:

```go
	// MessageIDHeader is the RFC 822 Message-ID header. It is the identifier
	// AppleScript uses to address a message, and is consumed by the trash
	// command. Deliberately not serialized: the JSON contract's "message_id"
	// is Mail's internal index ID, a different value.
	MessageIDHeader string `json:"-"`

// ... (rest of Message unchanged) ...

// Trash is one message selected for moving to the Trash.
type Trash struct {
	ID        int64   `json:"id"`
	MessageID string  `json:"message_id"` // RFC 822 Message-ID header
	Subject   string  `json:"subject"`
	From      Address `json:"from"`
	Action    string  `json:"action"` // always "trash"
	DryRun    bool    `json:"dry_run"`
}
```

In `internal/scan/scan.go`, after the `emlx.Extract` error check (after line 264, before the `BodyQuery` check), set the field:

```go
	msg.MessageIDHeader = decoded.MessageID
```

In `internal/output/render.go`, add a `Trash` case to the `tableRenderer.Write` switch (alongside the existing `Message`, `Link`, `Count`, `Summary` cases):

```go
	case Trash:
		if !r.header {
			fmt.Fprintln(r.tw, "ID\tFROM\tSUBJECT\tACTION")
			r.header = true
		}
		action := rec.Action
		if rec.DryRun {
			action = "dry-run"
		}
		_, err := fmt.Fprintf(r.tw, "%d\t%s\t%s\t%s\n",
			rec.ID, truncate(rec.From.Address, 32), truncate(rec.Subject, 60), action)
		return err
```

And to `textRenderer.Write`:

```go
	case Trash:
		action := rec.Action
		if rec.DryRun {
			action = "dry-run"
		}
		_, err := fmt.Fprintf(r.w, "%s: %s <%s> — %s\n",
			action, rec.From.Name, rec.From.Address, rec.Subject)
		return err
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scan/... ./internal/output/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/output/types.go internal/output/render.go internal/scan/scan.go internal/scan/scan_test.go
git commit -m "feat: surface RFC Message-ID from scan and add Trash output record"
```

---

### Task 3: Add the exit code, message, and main.go handling for Automation

**Files:**
- Modify: `cmd/mail/context.go:17-32` (exit codes + `exitCodeFor`)
- Modify: `cmd/mail/context.go` (add `automationMessage`)
- Modify: `cmd/mail/main.go:12-27`
- Test: `cmd/mail/context_test.go`

**Interfaces:**
- Consumes: `mailctl.ErrAutomationDenied` (Task 1).
- Produces: `exitAutomation = 3`; `automationMessage() string`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/mail/context_test.go` (add `mailctl` import):

```go
func TestExitCodeForAutomationError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", mailctl.ErrAutomationDenied)
	if got := exitCodeFor(err); got != 3 {
		t.Errorf("exitCodeFor(ErrAutomationDenied) = %d, want 3", got)
	}
}

func TestAutomationMessageNamesTerminalAndSettingsPath(t *testing.T) {
	msg := automationMessage()
	if !strings.Contains(msg, "Automation") {
		t.Error("message does not mention Automation")
	}
	if !strings.Contains(strings.ToLower(msg), "terminal") {
		t.Error("message does not tell the user to grant access to their terminal")
	}
	if !strings.Contains(msg, "Privacy & Security") {
		t.Error("message does not give the System Settings path")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/... -run 'TestExitCodeForAutomationError|TestAutomationMessageNamesTerminalAndSettingsPath'`
Expected: FAIL — `undefined: automationMessage`, and `exitCodeFor(ErrAutomationDenied) = 1, want 3`.

- [ ] **Step 3: Write the implementation**

In `cmd/mail/context.go`, update the exit-code constants and `exitCodeFor`, and add `automationMessage`. Add `"github.com/mirko/applemail/internal/mailctl"` to the imports.

```go
const (
	exitOK         = 0
	exitError      = 1
	exitPermission = 2
	exitAutomation = 3
)

func exitCodeFor(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, mailstore.ErrNoPermission):
		return exitPermission
	case errors.Is(err, mailctl.ErrAutomationDenied):
		return exitAutomation
	default:
		return exitError
	}
}

// automationMessage explains how to grant Automation access.
//
// Controlling Mail via Apple Events is a second, separate TCC grant from
// Full Disk Access, and like FDA it attaches to the responsible process —
// the terminal — not to this binary.
func automationMessage() string {
	return `Cannot control Apple Mail — macOS denied Automation access.

Moving messages to Trash requires your terminal to control Mail via Apple
Events, a separate permission from Full Disk Access.

  1. Open System Settings → Privacy & Security → Automation
  2. Find your terminal application and enable it for "Mail"
  3. Quit and restart the terminal completely
  4. Run "mail doctor --check-automation" to confirm

No changes were made to your mail.`
}
```

In `cmd/mail/main.go`, add the `mailctl` import and handle the new error before the `ErrNoPermission` branch:

```go
		if errors.Is(err, mailctl.ErrAutomationDenied) {
			fmt.Fprintln(os.Stderr, automationMessage())
		} else if errors.Is(err, mailstore.ErrNoPermission) {
			root, _ := mailstore.DefaultRoot()
			if flagMailDir != "" {
				root = flagMailDir
			}
			fmt.Fprintln(os.Stderr, permissionMessage(root))
		} else {
			fmt.Fprintf(os.Stderr, "mail: %v\n", err)
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/mail/... -run 'TestExitCodeFor|TestAutomationMessage'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/mail/context.go cmd/mail/main.go cmd/mail/context_test.go
git commit -m "feat: add Automation exit code and message for the trash command"
```

---

### Task 4: Add the `trash` command

**Files:**
- Modify: `cmd/mail/root.go:19-33` (add `flagDryRun` global)
- Create: `cmd/mail/trash.go`
- Modify: `cmd/mail/commands_test.go:39-55` (`resetFlags`)
- Test: `cmd/mail/trash_test.go`

**Interfaces:**
- Consumes: `mailctl.MoveToTrash` (Task 1), `output.Trash` + `output.Message.MessageIDHeader` (Task 2), `exitAutomation` (Task 3).
- Produces: `trashCmd` (Cobra command), a package-level `moveToTrash` seam var (defaults to `mailctl.MoveToTrash`) so tests can substitute it.

- [ ] **Step 1: Write the failing test**

Create `cmd/mail/trash_test.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirko/applemail/internal/mailctl"
)

func TestTrashDryRunEmitsRecordsWithoutActing(t *testing.T) {
	orig := moveToTrash
	moveToTrash = func(ids []string) (int, error) {
		t.Fatal("moveToTrash called during --dry-run")
		return 0, nil
	}
	defer func() { moveToTrash = orig }()

	out := runCommand(t, "trash", "--dry-run", "--subject", "Plain text hello")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatal(err)
	}
	if r["action"] != "trash" {
		t.Errorf("action = %v, want trash", r["action"])
	}
	if r["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", r["dry_run"])
	}
	if r["message_id"] != "<plain-1@example.com>" {
		t.Errorf("message_id = %v, want the RFC header <plain-1@example.com>", r["message_id"])
	}
}

func TestTrashBatchUsesRFCMessageIDNotIndexID(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	out := runCommand(t, "trash", "--subject", "Plain text hello")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	if len(got) != 1 {
		t.Fatalf("moveToTrash got %d ids, want 1", len(got))
	}
	// The Envelope Index message_id for fixture 1 is <fixture-1@example.com>;
	// the RFC header in the .emlx is <plain-1@example.com>. AppleScript needs
	// the RFC header, so this proves trash consumed the right value.
	if got[0] != "<plain-1@example.com>" {
		t.Errorf("moveToTrash id = %q, want <plain-1@example.com>", got[0])
	}
}

func TestTrashSingleID(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	out := runCommand(t, "trash", "3")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d records, want 1:\n%s", len(lines), out)
	}
	if len(got) != 1 || got[0] != "<multi-3@example.net>" {
		t.Errorf("moveToTrash ids = %v, want [<multi-3@example.net>]", got)
	}
}

func TestTrashSingleIDUnknownFails(t *testing.T) {
	orig := moveToTrash
	moveToTrash = func(ids []string) (int, error) {
		t.Fatal("moveToTrash called for a nonexistent id")
		return 0, nil
	}
	defer func() { moveToTrash = orig }()

	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "trash", "99999")
	if err == nil {
		t.Fatal("trash 99999 returned nil error, want not-found")
	}
}

func TestTrashDefaultLimitIsUnlimited(t *testing.T) {
	orig := moveToTrash
	defer func() { moveToTrash = orig }()

	var got []string
	moveToTrash = func(ids []string) (int, error) {
		got = append([]string{}, ids...)
		return 0, nil
	}

	// Five fixture messages; no --limit means all five are selected.
	runCommand(t, "trash")
	if len(got) != 5 {
		t.Errorf("moveToTrash got %d ids, want 5 (unlimited default)", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/... -run 'TestTrash'`
Expected: FAIL — `undefined: moveToTrash`, and `unknown command "trash"`.

- [ ] **Step 3: Write the implementation**

In `cmd/mail/root.go`, add `flagDryRun bool` to the globals var block (after `flagExportAs`):

```go
	flagDryRun          bool
	flagCheckAutomation bool
```

Create `cmd/mail/trash.go`:

```go
package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mirko/applemail/internal/mailctl"
	"github.com/mirko/applemail/internal/output"
	"github.com/mirko/applemail/internal/scan"
)

// moveToTrash is the seam tests substitute to avoid invoking AppleScript.
// It is mailctl.MoveToTrash in production.
var moveToTrash = mailctl.MoveToTrash

var trashCmd = &cobra.Command{
	Use:   "trash [id]",
	Short: "Move messages to Trash",
	Long: `Move matching messages to the Trash via AppleScript.

Pass a single ROWID to move one message, or use the shared filter flags
(--from, --to, --subject, --mailbox, --since, --until, --unread, --flagged,
--has-attachment) to move a batch. Trash is recoverable until it is emptied
in Mail.

--dry-run reports what would be moved without changing anything. Moving
messages requires Automation permission for your terminal (see "mail doctor
--check-automation").`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return runTrashOne(cmd, args[0])
		}
		return runTrashBatch(cmd)
	},
}

func trashRecord(m output.Message) output.Trash {
	return output.Trash{
		ID:        m.ID,
		MessageID: m.MessageIDHeader,
		Subject:   m.Subject,
		From:      m.From,
		Action:    "trash",
		DryRun:    flagDryRun,
	}
}

func runTrashOne(cmd *cobra.Command, arg string) error {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid message id %q: %w", arg, err)
	}

	store, paths, err := openMail()
	if err != nil {
		return err
	}
	defer store.Close()

	renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	scanner := scan.New(store, paths)
	msg, found, err := scanner.One(id, scan.Options{})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("message %d not found", id)
	}
	if msg.MessageIDHeader == "" {
		return fmt.Errorf("message %d has no Message-ID header and cannot be addressed", id)
	}

	if !flagDryRun {
		if _, err := moveToTrash([]string{msg.MessageIDHeader}); err != nil {
			return err
		}
	}
	if err := renderer.Write(trashRecord(msg)); err != nil {
		return err
	}
	return renderer.Close()
}

func runTrashBatch(cmd *cobra.Command) error {
	filter, err := buildFilter(cmd)
	if err != nil {
		return err
	}

	store, paths, err := openMail()
	if err != nil {
		return err
	}
	defer store.Close()

	renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	scanner := scan.New(store, paths)
	var pending []output.Trash
	var rfcIDs []string
	var unaddressable int

	stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
		func(m output.Message) error {
			if m.MessageIDHeader == "" {
				unaddressable++
				return nil
			}
			rec := trashRecord(m)
			if flagDryRun {
				return renderer.Write(rec)
			}
			pending = append(pending, rec)
			rfcIDs = append(rfcIDs, m.MessageIDHeader)
			return nil
		})
	if err != nil {
		return err
	}

	if !flagDryRun && len(rfcIDs) > 0 {
		notFound, err := moveToTrash(rfcIDs)
		if err != nil {
			return err
		}
		if notFound > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%d messages not found in Mail (already moved or deleted)\n", notFound)
		}
		for _, rec := range pending {
			if err := renderer.Write(rec); err != nil {
				return err
			}
		}
	}

	if err := renderer.Close(); err != nil {
		return err
	}
	if stats.Skipped > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d messages skipped (%d missing files, %d parse errors)\n",
			stats.Skipped, stats.MissingFiles, stats.ParseErrors)
	}
	if unaddressable > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d messages skipped (no Message-ID header)\n", unaddressable)
	}
	return nil
}

func init() {
	addFilterFlags(trashCmd, 0)
	trashCmd.Flags().BoolVar(&flagDryRun, "dry-run", false,
		"report what would be moved without changing anything")
	rootCmd.AddCommand(trashCmd)
}
```

In `cmd/mail/commands_test.go`, add two resets to `resetFlags` (and import `mailctl`):

```go
	flagDryRun = false
	flagCheckAutomation = false
	moveToTrash = mailctl.MoveToTrash
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/mail/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/mail/root.go cmd/mail/trash.go cmd/mail/trash_test.go cmd/mail/commands_test.go
git commit -m "feat: add trash command with dry-run and batch support"
```

---

### Task 5: Add `doctor --check-automation` and document the command

**Files:**
- Modify: `cmd/mail/doctor.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `mailctl.CheckAutomation` (Task 1), `flagCheckAutomation` (Task 4).

- [ ] **Step 1: Write the implementation**

In `cmd/mail/doctor.go`, add the `mailctl` import, and after the "All checks passed" line, before `return nil`:

```go
		if flagCheckAutomation {
			fmt.Fprintln(out, "\nAutomation access:")
			if err := mailctl.CheckAutomation(); err != nil {
				if errors.Is(err, mailctl.ErrAutomationDenied) {
					fmt.Fprintln(out, "  Not available — grant Automation for Mail to your terminal.")
				} else {
					fmt.Fprintf(out, "  Could not check: %v\n", err)
				}
			} else {
				fmt.Fprintln(out, "  Available — \"mail trash\" can control Mail.")
			}
		}
		return nil
```

In `cmd/mail/doctor.go`, register the flag in `init()`:

```go
	doctorCmd.Flags().BoolVar(&flagCheckAutomation, "check-automation", false,
		"verify the terminal can control Mail (required by \"mail trash\")")
```

In `README.md`, add the `trash` command to the Commands section (after `mail show 48213 --raw`):

```
mail trash 48213                              # move one message to Trash
mail trash --from linkedin --since 30d        # move a batch
mail trash --from X --dry-run                 # preview without acting
```

And extend the "Not supported" note and Requirements section to mention Automation:

In Requirements, after step 4, add a note that `mail trash` additionally requires Automation permission; and in "Not supported", change the first line to keep `deleting` out of scope while noting `trash` moves to Trash (not permanent delete).

- [ ] **Step 2: Run test and build to verify nothing broke**

Run: `go test ./... && go build -o mail ./cmd/mail`
Expected: PASS and build succeeds.

- [ ] **Step 3: Commit**

```bash
git add cmd/mail/doctor.go README.md
git commit -m "feat: add doctor --check-automation and document trash command"
```

---

### Task 6: Full verification

- [ ] **Step 1: Run the full suite and race detector**

Run: `make test && make race`
Expected: all tests pass, no race failures.

- [ ] **Step 2: Manual smoke test (run by the user against real mail)**

```bash
make build
./mail doctor --check-automation
./mail trash 7620 --dry-run      # pick a disposable message id
./mail trash 7620                # confirm it lands in Trash in Mail.app
```

Expected: dry-run prints one record with `"dry_run": true` and changes nothing; the real run moves the message to Trash. If Automation is not yet granted, the command exits `3` with the Automation message.

- [ ] **Step 3: Commit any final adjustments**

Only if the smoke test surfaced a needed fix. Otherwise the feature is complete.

---

## Self-Review Notes

- **Spec coverage:** trash single-ID + batch + filters → Task 4; dry-run → Task 4; RFC Message-ID surfacing → Task 2; `internal/mailctl` bridge + one-osascript-per-batch → Task 1; Automation permission + exit code 3 → Task 3; doctor check → Task 5; output record shape → Task 2; testing (script text, classifier, dry-run, fixtures) → Tasks 1, 2, 4; manual smoke → Task 6. All spec sections covered.
- **Type consistency:** `MessageIDHeader` is `string` on `output.Message` (Task 2) and read as `m.MessageIDHeader` in `trash.go` (Task 4); `MoveToTrash(messageIDs []string) (int, error)` signature matches the `moveToTrash` seam var in both Task 1 and Task 4; `output.Trash` fields match `trashRecord`.
- **Placeholder scan:** none — every step has concrete code or a concrete command with expected output.
