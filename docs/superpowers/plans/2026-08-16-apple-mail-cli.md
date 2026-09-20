# Apple Mail CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only Go CLI that searches and analyzes Apple Mail messages, emitting LLM-consumable JSONL.

**Architecture:** A shared core reads message metadata from Mail's Envelope Index (read-only SQLite), resolves each hit to its `.emlx` file on disk, and parses bodies in a parallel worker pool that streams results as they complete. Cobra commands are thin wrappers that build one shared filter struct and select an output renderer. All schema knowledge is isolated in `internal/mailstore` so a schema correction stays local.

**Tech Stack:** Go 1.26.5, Cobra (CLI), `modernc.org/sqlite` (cgo-free SQLite driver — keeps the binary a single portable file), `golang.org/x/net/html` (HTML→text), `golang.org/x/text` (charset decoding), Go stdlib `net/mail` + `mime` + `encoding/json`.

**Spec:** `docs/superpowers/specs/2026-08-16-apple-mail-cli-design.md`

## Global Constraints

- **Go 1.26.5**, target `darwin/arm64`. Module path: `github.com/harasuke/applemail`.
- **Binary name is `mail`.** Built from `cmd/mail`.
- **Read-only, always.** Mail's DB is opened `file:<path>?mode=ro&immutable=1`. Nothing under `~/Library/Mail` is ever written or modified. `export` is the only command that writes, and only to a user-named path.
- **No network requests, ever.** The CLI never fetches a URL. Redirect unwrapping is done offline from query-string parameters only.
- **SQLite driver name is `"sqlite"`** (modernc), not `"sqlite3"`.
- **Cocoa epoch offset is `978307200`.** Envelope Index timestamps are seconds since 2001-01-01.
- **Version directory is discovered at runtime**, never hardcoded. Glob `~/Library/Mail/V*/MailData/Envelope Index`, select the highest numeric version present.
- **stdout is pure JSONL.** All errors, warnings, and skip counts go to stderr.
- **Exit codes:** `0` success (including zero results), `1` general error, `2` missing Full Disk Access.
- **`signals` are facts, never judgments.** No `is_spam` or `is_interesting` field may be added.
- **One unreadable message must never fail a scan.** Per-message errors populate an `error` field and the scan continues.
- **Deferred, do not implement:** sending, moving messages, creating mailboxes, flagging, deleting, any body cache, the MCP server.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module definition and dependencies |
| `internal/mailstore/paths.go` | `V*` directory discovery, Mail root resolution, permission preflight |
| `internal/mailstore/paths_test.go` | Tests for discovery and preflight |
| `internal/mailstore/time.go` | Cocoa ↔ Unix epoch conversion |
| `internal/mailstore/time_test.go` | Tests for epoch conversion |
| `internal/mailstore/filter.go` | The shared `Filter` struct used by every command |
| `internal/mailstore/store.go` | Read-only SQLite open, metadata queries, `Message` metadata type |
| `internal/mailstore/store_test.go` | Tests against a synthetic Envelope Index |
| `internal/mailstore/emlxpath.go` | Cached `.mbox/Messages` directory map, ROWID → `.emlx` path |
| `internal/mailstore/emlxpath_test.go` | Tests for path resolution |
| `internal/emlx/parse.go` | `.emlx` three-part parser (bytecount / MIME / plist) |
| `internal/emlx/parse_test.go` | Parser tests including malformed input |
| `internal/emlx/text.go` | Body text extraction, charset fallback, HTML→text |
| `internal/emlx/text_test.go` | Extraction tests |
| `internal/analyze/links.go` | Link extraction, normalization, canonicalization, classification |
| `internal/analyze/links_test.go` | Link tests including LinkedIn cases |
| `internal/analyze/signals.go` | Header-derived signals |
| `internal/analyze/signals_test.go` | Signal tests |
| `internal/scan/scan.go` | Parallel worker pool, ordered streaming emission |
| `internal/scan/scan_test.go` | Concurrency and ordering tests |
| `internal/corpus/summary.go` | Corpus rollup accumulator |
| `internal/corpus/summary_test.go` | Rollup tests |
| `internal/output/types.go` | Wire-format JSON structs (the output contract) |
| `internal/output/render.go` | JSONL / JSON / table / text renderers |
| `internal/output/render_test.go` | Renderer tests |
| `internal/testdata/gen.go` | Synthetic Envelope Index + `.emlx` fixture builder |
| `cmd/mail/main.go` | Entry point, exit-code mapping |
| `cmd/mail/root.go` | Root command, persistent flags, shared filter binding |
| `cmd/mail/doctor.go` | `mail doctor` |
| `cmd/mail/search.go` | `mail search` |
| `cmd/mail/show.go` | `mail show` |
| `cmd/mail/links.go` | `mail links` |
| `cmd/mail/export.go` | `mail export` |
| `cmd/mail/stats.go` | `mail stats` |

**Task order rationale:** Tasks 1–3 establish the foundation everything else consumes (time, paths, fixtures). Tasks 4–8 build the read path bottom-up (filter → store → `.emlx` paths → parser → text extraction). Tasks 9–10 add analysis (links, signals). Task 11 defines the output contract. Task 12 is the scanner that joins them, and Task 13 the corpus rollup. Tasks 14–17 are the CLI: skeleton first, then the commands in pairs. Task 18 builds and validates against real mail.

---

### Task 1: Module setup and Cocoa epoch conversion

**Files:**
- Create: `go.mod`
- Create: `internal/mailstore/time.go`
- Test: `internal/mailstore/time_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces: `mailstore.CocoaToTime(float64) time.Time` and `mailstore.TimeToCocoa(time.Time) float64`

- [ ] **Step 1: Initialize the module**

```bash
cd /Users/mirko/Desktop/Projects/apple_mail_cli
go mod init github.com/harasuke/applemail
```

- [ ] **Step 2: Write the failing test**

Create `internal/mailstore/time_test.go`:

```go
package mailstore

import (
	"testing"
	"time"
)

func TestCocoaToTime(t *testing.T) {
	tests := []struct {
		name  string
		cocoa float64
		want  time.Time
	}{
		{"cocoa epoch zero is 2001-01-01", 0, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"one hour after cocoa epoch", 3600, time.Date(2001, 1, 1, 1, 0, 0, 0, time.UTC)},
		{"a real message timestamp", 807653482, time.Date(2026, 8, 5, 20, 11, 22, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CocoaToTime(tt.cocoa)
			if !got.Equal(tt.want) {
				t.Errorf("CocoaToTime(%v) = %v, want %v", tt.cocoa, got, tt.want)
			}
		})
	}
}

func TestTimeToCocoaRoundTrip(t *testing.T) {
	original := time.Date(2026, 8, 16, 12, 30, 0, 0, time.UTC)
	got := CocoaToTime(TimeToCocoa(original))
	if !got.Equal(original) {
		t.Errorf("round trip = %v, want %v", got, original)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mailstore/ -run TestCocoa -v`
Expected: FAIL — `undefined: CocoaToTime`

- [ ] **Step 4: Write minimal implementation**

Create `internal/mailstore/time.go`:

```go
// Package mailstore reads Apple Mail's Envelope Index database.
package mailstore

import "time"

// cocoaEpochOffset is the number of seconds between the Unix epoch
// (1970-01-01) and the Cocoa/Core Data reference date (2001-01-01).
// Envelope Index stores all timestamps in the Cocoa form.
const cocoaEpochOffset = 978307200

// CocoaToTime converts a Cocoa reference-date timestamp to a UTC time.
func CocoaToTime(cocoa float64) time.Time {
	sec := int64(cocoa)
	nsec := int64((cocoa - float64(sec)) * 1e9)
	return time.Unix(sec+cocoaEpochOffset, nsec).UTC()
}

// TimeToCocoa converts a time to a Cocoa reference-date timestamp.
func TimeToCocoa(t time.Time) float64 {
	return float64(t.Unix() - cocoaEpochOffset)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mailstore/ -run TestCocoa -v`
Expected: PASS (both tests)

- [ ] **Step 6: Add .gitignore and commit**

Create `.gitignore`:

```
/mail
*.test
.DS_Store
```

```bash
git add go.mod .gitignore internal/mailstore/time.go internal/mailstore/time_test.go
git commit -m "feat: add Cocoa epoch conversion"
```

---

### Task 2: Mail directory discovery and permission preflight

**Files:**
- Create: `internal/mailstore/paths.go`
- Test: `internal/mailstore/paths_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks
- Produces:
  - `mailstore.ErrNoPermission` (sentinel error)
  - `mailstore.ErrNoMailDir` (sentinel error)
  - `mailstore.ErrNoVersionDir` (sentinel error)
  - `mailstore.DiscoverPaths(root string) (*Paths, error)` where `root` is the Mail directory (empty string means `~/Library/Mail`)
  - `type Paths struct { Root, VersionDir, IndexPath string; Version int }`

**Context for the implementer:** `~/Library/Mail` is protected by macOS TCC. On the target machine it currently returns `Operation not permitted`. This is the normal starting state, not an edge case — the error message this task produces is the first thing the user will see. Full Disk Access must be granted to the *terminal application*, not to the `mail` binary, because TCC evaluates the responsible process.

The version directory varies by macOS release (V10 on Sequoia, V11 or V12 on Tahoe), so it must be globbed and the highest version selected.

- [ ] **Step 1: Write the failing test**

Create `internal/mailstore/paths_test.go`:

```go
package mailstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// makeMailDir builds a fake Mail directory containing the given version
// directories, each with an Envelope Index file.
func makeMailDir(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		md := filepath.Join(root, v, "MailData")
		if err := os.MkdirAll(md, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(md, "Envelope Index")
		if err := os.WriteFile(f, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDiscoverPathsPicksHighestVersion(t *testing.T) {
	root := makeMailDir(t, "V10", "V11", "V12")
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 12 {
		t.Errorf("Version = %d, want 12", p.Version)
	}
	if filepath.Base(p.VersionDir) != "V12" {
		t.Errorf("VersionDir = %q, want basename V12", p.VersionDir)
	}
	if filepath.Base(p.IndexPath) != "Envelope Index" {
		t.Errorf("IndexPath = %q, want basename 'Envelope Index'", p.IndexPath)
	}
}

func TestDiscoverPathsSingleVersion(t *testing.T) {
	root := makeMailDir(t, "V10")
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 10 {
		t.Errorf("Version = %d, want 10", p.Version)
	}
}

func TestDiscoverPathsIgnoresVersionsWithoutIndex(t *testing.T) {
	root := makeMailDir(t, "V10")
	// V12 exists but has no Envelope Index — must not be selected.
	if err := os.MkdirAll(filepath.Join(root, "V12", "MailData"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 10 {
		t.Errorf("Version = %d, want 10 (V12 has no index)", p.Version)
	}
}

func TestDiscoverPathsNoMailDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := DiscoverPaths(missing)
	if !errors.Is(err, ErrNoMailDir) {
		t.Errorf("err = %v, want ErrNoMailDir", err)
	}
}

func TestDiscoverPathsNoVersionDir(t *testing.T) {
	root := t.TempDir() // exists but is empty
	_, err := DiscoverPaths(root)
	if !errors.Is(err, ErrNoVersionDir) {
		t.Errorf("err = %v, want ErrNoVersionDir", err)
	}
}

func TestDiscoverPathsNoPermission(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
	_, err := DiscoverPaths(root)
	if !errors.Is(err, ErrNoPermission) {
		t.Errorf("err = %v, want ErrNoPermission", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailstore/ -run TestDiscoverPaths -v`
Expected: FAIL — `undefined: DiscoverPaths`

- [ ] **Step 3: Write minimal implementation**

Create `internal/mailstore/paths.go`:

```go
package mailstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrNoPermission means macOS TCC denied access to the Mail directory.
	// Full Disk Access must be granted to the terminal application.
	ErrNoPermission = errors.New("permission denied reading Mail directory")
	// ErrNoMailDir means the Mail directory does not exist at all.
	ErrNoMailDir = errors.New("Mail directory not found")
	// ErrNoVersionDir means no V<N> directory with an Envelope Index was found.
	ErrNoVersionDir = errors.New("no Mail version directory with an Envelope Index")
)

// Paths holds the resolved locations of Apple Mail's on-disk data.
type Paths struct {
	Root       string // ~/Library/Mail
	VersionDir string // ~/Library/Mail/V12
	IndexPath  string // ~/Library/Mail/V12/MailData/Envelope Index
	Version    int    // 12
}

// DefaultRoot returns the standard Mail directory for the current user.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Mail"), nil
}

// DiscoverPaths locates the Mail data directory and the highest-numbered
// version directory that actually contains an Envelope Index. Passing an
// empty root uses the current user's default Mail directory.
//
// The version directory number varies across macOS releases, so it is
// always discovered rather than assumed.
func DiscoverPaths(root string) (*Paths, error) {
	if root == "" {
		var err error
		if root, err = DefaultRoot(); err != nil {
			return nil, err
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrPermission):
			return nil, fmt.Errorf("%w: %s", ErrNoPermission, root)
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%w: %s", ErrNoMailDir, root)
		default:
			return nil, err
		}
	}

	type candidate struct {
		version int
		dir     string
		index   string
	}
	var found []candidate
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "V") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "V"))
		if err != nil {
			continue
		}
		dir := filepath.Join(root, e.Name())
		index := filepath.Join(dir, "MailData", "Envelope Index")
		if _, err := os.Stat(index); err != nil {
			continue // a version directory without an index is unusable
		}
		found = append(found, candidate{n, dir, index})
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoVersionDir, root)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].version > found[j].version })

	best := found[0]
	return &Paths{
		Root:       root,
		VersionDir: best.dir,
		IndexPath:  best.index,
		Version:    best.version,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailstore/ -v`
Expected: PASS (all discovery tests plus the Task 1 time tests)

- [ ] **Step 5: Commit**

```bash
git add internal/mailstore/paths.go internal/mailstore/paths_test.go
git commit -m "feat: discover Mail version directory and detect permission failures"
```

---

### Task 3: Synthetic fixture builder

**Files:**
- Create: `internal/testdata/gen.go`
- Test: `internal/testdata/gen_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks
- Produces:
  - `testdata.FixtureMessage` struct (fields listed in the code below)
  - `testdata.BuildMailDir(t *testing.T, msgs []FixtureMessage) string` — returns the root of a complete fake Mail directory containing a real SQLite Envelope Index and matching `.emlx` files
  - `testdata.DefaultMessages()` — a standard 5-message set reused across packages

**Context for the implementer:** Real mail cannot be used in tests — it is private and mutable. Every test in this project runs against fixtures built by this task. The Envelope Index schema below is reconstructed from research, not read from a live machine; `internal/mailstore` is the only package that encodes it, so a correction later stays local.

The `.emlx` format is three parts: a decimal byte count terminated by `\n`, then exactly that many bytes of RFC-822 MIME content, then an Apple plist trailer.

- [ ] **Step 1: Add the SQLite driver dependency**

```bash
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the failing test**

Create `internal/testdata/gen_test.go`:

```go
package testdata

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBuildMailDirCreatesReadableIndex(t *testing.T) {
	root := BuildMailDir(t, DefaultMessages())

	index := filepath.Join(root, "V12", "MailData", "Envelope Index")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("Envelope Index not created: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+index+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != len(DefaultMessages()) {
		t.Errorf("message count = %d, want %d", count, len(DefaultMessages()))
	}
}

func TestBuildMailDirCreatesEmlxFiles(t *testing.T) {
	msgs := DefaultMessages()
	root := BuildMailDir(t, msgs)

	var found int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".emlx") {
			found++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(msgs) {
		t.Errorf("found %d .emlx files, want %d", found, len(msgs))
	}
}

func TestEmlxFileHasValidByteCountPrefix(t *testing.T) {
	msgs := DefaultMessages()
	root := BuildMailDir(t, msgs)

	path := EmlxPath(root, msgs[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	nl := strings.IndexByte(string(raw), '\n')
	if nl < 0 {
		t.Fatal("no newline terminating the byte count line")
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw[:nl])))
	if err != nil {
		t.Fatalf("byte count line %q is not a number: %v", raw[:nl], err)
	}
	if n != len(msgs[0].RawMIME) {
		t.Errorf("byte count = %d, want %d", n, len(msgs[0].RawMIME))
	}
	body := string(raw[nl+1 : nl+1+n])
	if body != msgs[0].RawMIME {
		t.Error("MIME content does not match the declared byte count region")
	}
	if !strings.Contains(string(raw[nl+1+n:]), "<?xml") {
		t.Error("plist trailer missing after the MIME region")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/testdata/ -v`
Expected: FAIL — `undefined: BuildMailDir`

- [ ] **Step 4: Write minimal implementation**

Create `internal/testdata/gen.go`:

```go
// Package testdata builds synthetic Apple Mail directories for tests.
//
// Real mail is private and mutable, so every test in this project runs
// against fixtures created here. The Envelope Index schema below mirrors
// the real one closely enough for query testing.
package testdata

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// FixtureMessage describes one synthetic message.
type FixtureMessage struct {
	ROWID       int64
	Subject     string
	SenderName  string
	SenderAddr  string
	ToAddr      string
	DateSent    float64 // Cocoa timestamp
	DateRecv    float64 // Cocoa timestamp
	MailboxURL  string
	MailboxName string
	Read        bool
	Flagged     bool
	Deleted     bool
	Summary     string
	RawMIME     string // the MIME content written into the .emlx
}

// DefaultMessages returns a standard fixture set covering the shapes that
// matter: plain text, HTML-only, multipart with attachment, an
// encoded-word subject, and a bulk message with tracking links.
func DefaultMessages() []FixtureMessage {
	return []FixtureMessage{
		{
			ROWID: 1, Subject: "Plain text hello",
			SenderName: "Alice Smith", SenderAddr: "alice@example.com",
			ToAddr: "user@example.com", DateSent: 807653482, DateRecv: 807653500,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Read: true, Summary: "Just saying hello",
			RawMIME: "From: Alice Smith <alice@example.com>\r\n" +
				"To: user@example.com\r\n" +
				"Subject: Plain text hello\r\n" +
				"Date: Wed, 05 Aug 2026 20:11:22 +0000\r\n" +
				"Message-ID: <plain-1@example.com>\r\n" +
				"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
				"Just saying hello. Visit https://example.com/docs for details.\r\n",
		},
		{
			ROWID: 2, Subject: "HTML only newsletter",
			SenderName: "News", SenderAddr: "news@example.org",
			ToAddr: "user@example.com", DateSent: 807715882, DateRecv: 807715900,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Summary: "This week in news",
			RawMIME: "From: News <news@example.org>\r\n" +
				"To: user@example.com\r\n" +
				"Subject: HTML only newsletter\r\n" +
				"Message-ID: <html-2@example.org>\r\n" +
				"List-Unsubscribe: <https://example.org/unsub?u=42>\r\n" +
				"Precedence: bulk\r\n" +
				"Content-Type: text/html; charset=utf-8\r\n\r\n" +
				"<html><body><p>This week in <b>news</b>.</p>" +
				`<a href="https://example.org/article/9">Read more</a>` +
				`<img src="https://track.example.org/pixel.gif" width="1" height="1">` +
				"</body></html>\r\n",
		},
		{
			ROWID: 3, Subject: "Multipart with attachment",
			SenderName: "Bob Jones", SenderAddr: "bob@example.net",
			ToAddr: "user@example.com", DateSent: 807802282, DateRecv: 807802300,
			MailboxURL: "imap://user@example.com/Archive", MailboxName: "Archive",
			Flagged: true, Summary: "Please see attached",
			RawMIME: "From: Bob Jones <bob@example.net>\r\n" +
				"To: user@example.com\r\n" +
				"Subject: Multipart with attachment\r\n" +
				"Message-ID: <multi-3@example.net>\r\n" +
				"Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n\r\n" +
				"--BOUND\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
				"Please see attached.\r\n" +
				"--BOUND\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\n" +
				"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\nSGVsbG8=\r\n" +
				"--BOUND--\r\n",
		},
		{
			ROWID: 4, Subject: "Perché è importante",
			SenderName: "Carla Rossi", SenderAddr: "carla@example.it",
			ToAddr: "user@example.com", DateSent: 807888682, DateRecv: 807888700,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Summary: "Un messaggio in italiano",
			RawMIME: "From: Carla Rossi <carla@example.it>\r\n" +
				"To: user@example.com\r\n" +
				"Subject: =?UTF-8?B?UGVyY2jDqSDDqCBpbXBvcnRhbnRl?=\r\n" +
				"Message-ID: <encoded-4@example.it>\r\n" +
				"Content-Type: text/plain; charset=utf-8\r\n" +
				"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
				"Un messaggio in italiano con accenti: perch=C3=A9.\r\n",
		},
		{
			ROWID: 5, Subject: "5 nuove posizioni per te",
			SenderName: "LinkedIn Jobs", SenderAddr: "jobs-noreply@linkedin.com",
			ToAddr: "user@example.com", DateSent: 807975082, DateRecv: 807975100,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Summary: "Posizioni consigliate",
			RawMIME: "From: LinkedIn Jobs <jobs-noreply@linkedin.com>\r\n" +
				"To: user@example.com\r\n" +
				"Subject: 5 nuove posizioni per te\r\n" +
				"Message-ID: <li-5@linkedin.com>\r\n" +
				"List-Unsubscribe: <https://www.linkedin.com/e/unsub?t=1>\r\n" +
				"Precedence: bulk\r\n" +
				"Content-Type: text/html; charset=utf-8\r\n\r\n" +
				"<html><body>" +
				`<a href="https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123&eid=abc">Senior Backend Engineer — Milano</a>` +
				`<a href="https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-digest&midToken=AQE999">Senior Backend Engineer</a>` +
				`<a href="https://www.linkedin.com/comm/jobs/view/4055512233?trk=eml-jobs">Platform Engineer — Remote</a>` +
				`<a href="https://track.linkedin.com/px?e=1"><img src="https://track.linkedin.com/px.gif"></a>` +
				"</body></html>\r\n",
		},
	}
}

// EmlxPath returns the .emlx path BuildMailDir writes for a message.
func EmlxPath(root string, m FixtureMessage) string {
	return filepath.Join(root, "V12", "TESTACCOUNT",
		m.MailboxName+".mbox", "Messages", fmt.Sprintf("%d.emlx", m.ROWID))
}

// BuildMailDir creates a complete fake Mail directory: a real SQLite
// Envelope Index plus one .emlx file per message. It returns the root.
func BuildMailDir(t *testing.T, msgs []FixtureMessage) string {
	t.Helper()
	root := t.TempDir()

	mailData := filepath.Join(root, "V12", "MailData")
	if err := os.MkdirAll(mailData, 0o755); err != nil {
		t.Fatal(err)
	}

	writeIndex(t, filepath.Join(mailData, "Envelope Index"), msgs)
	for _, m := range msgs {
		writeEmlx(t, EmlxPath(root, m), m)
	}
	return root
}

// writeEmlx writes the three-part .emlx format: byte count line, MIME
// content of exactly that length, then a plist trailer.
func writeEmlx(t *testing.T, path string, m FixtureMessage) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>flags</key><integer>0</integer></dict></plist>
`
	content := fmt.Sprintf("%d\n%s%s", len(m.RawMIME), m.RawMIME, plist)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeIndex creates a SQLite database mirroring the Envelope Index schema.
func writeIndex(t *testing.T, path string, msgs []FixtureMessage) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := `
CREATE TABLE subjects (ROWID INTEGER PRIMARY KEY, subject TEXT);
CREATE TABLE addresses (ROWID INTEGER PRIMARY KEY, address TEXT, comment TEXT);
CREATE TABLE mailboxes (ROWID INTEGER PRIMARY KEY, url TEXT);
CREATE TABLE summaries (ROWID INTEGER PRIMARY KEY, summary TEXT);
CREATE TABLE messages (
    ROWID INTEGER PRIMARY KEY,
    message_id TEXT,
    subject INTEGER,
    sender INTEGER,
    mailbox INTEGER,
    summary INTEGER,
    conversation_id INTEGER,
    date_sent REAL,
    date_received REAL,
    read INTEGER DEFAULT 0,
    flagged INTEGER DEFAULT 0,
    deleted INTEGER DEFAULT 0,
    flags INTEGER DEFAULT 0
);
CREATE TABLE recipients (
    ROWID INTEGER PRIMARY KEY,
    message_id INTEGER,
    address INTEGER,
    type INTEGER
);
CREATE TABLE attachments (
    ROWID INTEGER PRIMARY KEY,
    message_id INTEGER,
    name TEXT
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	mailboxIDs := map[string]int64{}
	addrIDs := map[string]int64{}
	var nextMailbox, nextAddr int64 = 1, 1

	for _, m := range msgs {
		if _, ok := mailboxIDs[m.MailboxURL]; !ok {
			if _, err := db.Exec("INSERT INTO mailboxes (ROWID, url) VALUES (?, ?)",
				nextMailbox, m.MailboxURL); err != nil {
				t.Fatal(err)
			}
			mailboxIDs[m.MailboxURL] = nextMailbox
			nextMailbox++
		}
		for _, pair := range [][2]string{{m.SenderAddr, m.SenderName}, {m.ToAddr, ""}} {
			if _, ok := addrIDs[pair[0]]; ok {
				continue
			}
			if _, err := db.Exec("INSERT INTO addresses (ROWID, address, comment) VALUES (?, ?, ?)",
				nextAddr, pair[0], pair[1]); err != nil {
				t.Fatal(err)
			}
			addrIDs[pair[0]] = nextAddr
			nextAddr++
		}

		if _, err := db.Exec("INSERT INTO subjects (ROWID, subject) VALUES (?, ?)",
			m.ROWID, m.Subject); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO summaries (ROWID, summary) VALUES (?, ?)",
			m.ROWID, m.Summary); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO messages
            (ROWID, message_id, subject, sender, mailbox, summary, conversation_id,
             date_sent, date_received, read, flagged, deleted)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ROWID, fmt.Sprintf("<fixture-%d@example.com>", m.ROWID),
			m.ROWID, addrIDs[m.SenderAddr], mailboxIDs[m.MailboxURL], m.ROWID,
			m.ROWID, m.DateSent, m.DateRecv,
			boolToInt(m.Read), boolToInt(m.Flagged), boolToInt(m.Deleted)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO recipients (message_id, address, type) VALUES (?, ?, 0)",
			m.ROWID, addrIDs[m.ToAddr]); err != nil {
			t.Fatal(err)
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/testdata/ -v`
Expected: PASS (all three tests)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/testdata/
git commit -m "feat: add synthetic Mail directory fixture builder"
```

---

### Task 4: Shared filter struct

**Files:**
- Create: `internal/mailstore/filter.go`
- Test: `internal/mailstore/filter_test.go`

**Interfaces:**
- Consumes: `mailstore.TimeToCocoa` (Task 1)
- Produces: `mailstore.Filter` struct and `(f Filter) buildWhere() (string, []any)`

**Context for the implementer:** Every command (`search`, `links`, `export`, `stats`) uses this one struct, so a filter added here is available everywhere with identical semantics. `buildWhere` returns a SQL fragment with `?` placeholders and the matching argument slice — never interpolate values into the string, that is a SQL injection path.

`Deleted` messages are excluded unless explicitly included; Mail keeps tombstones in the index.

- [ ] **Step 1: Write the failing test**

Create `internal/mailstore/filter_test.go`:

```go
package mailstore

import (
	"strings"
	"testing"
	"time"
)

func TestBuildWhereEmptyFilterExcludesDeleted(t *testing.T) {
	f := Filter{}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.deleted = 0") {
		t.Errorf("where = %q, want it to exclude deleted messages", where)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildWhereFrom(t *testing.T) {
	// --from matches either the address or the display name, so the
	// value is bound twice.
	f := Filter{From: "alice@example.com"}
	where, args := f.buildWhere()
	if !strings.Contains(where, "addr.address LIKE ?") {
		t.Errorf("where = %q, want a sender address clause", where)
	}
	if !strings.Contains(where, "addr.comment LIKE ?") {
		t.Errorf("where = %q, want a sender display-name clause", where)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want two (address and display name)", args)
	}
	if args[0] != "%alice@example.com%" || args[1] != "%alice@example.com%" {
		t.Errorf("args = %v, want both wildcarded", args)
	}
}

func TestBuildWhereSubjectAndUnread(t *testing.T) {
	f := Filter{Subject: "invoice", Unread: true}
	where, args := f.buildWhere()
	if !strings.Contains(where, "subj.subject LIKE ?") {
		t.Errorf("where = %q, want a subject clause", where)
	}
	if !strings.Contains(where, "m.read = 0") {
		t.Errorf("where = %q, want an unread clause", where)
	}
	if len(args) != 1 || args[0] != "%invoice%" {
		t.Errorf("args = %v, want one wildcarded subject", args)
	}
}

func TestBuildWhereDateRangeUsesCocoaTimestamps(t *testing.T) {
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	f := Filter{Since: &since}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.date_received >= ?") {
		t.Errorf("where = %q, want a since clause", where)
	}
	if len(args) != 1 {
		t.Fatalf("args = %v, want one", args)
	}
	if args[0] != TimeToCocoa(since) {
		t.Errorf("args[0] = %v, want the Cocoa form %v", args[0], TimeToCocoa(since))
	}
}

func TestBuildWhereIncludeDeleted(t *testing.T) {
	f := Filter{IncludeDeleted: true}
	where, _ := f.buildWhere()
	if strings.Contains(where, "m.deleted = 0") {
		t.Errorf("where = %q, want no deleted-exclusion clause", where)
	}
}

func TestBuildWhereROWIDIsAPrimaryKeyLookup(t *testing.T) {
	f := Filter{ROWID: 48213, IncludeDeleted: true}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.ROWID = ?") {
		t.Errorf("where = %q, want a ROWID clause", where)
	}
	if len(args) != 1 || args[0] != int64(48213) {
		t.Errorf("args = %v, want the ROWID bound once", args)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailstore/ -run TestBuildWhere -v`
Expected: FAIL — `undefined: Filter`

- [ ] **Step 3: Write minimal implementation**

Create `internal/mailstore/filter.go`:

```go
package mailstore

import (
	"strings"
	"time"
)

// Filter is the single set of criteria shared by every command. Adding a
// field here makes it available to search, links, export, and stats with
// identical meaning.
type Filter struct {
	// ROWID selects exactly one message by its index ID. Set it and the
	// query is a primary-key lookup, which is how `show` avoids scanning.
	ROWID          int64
	From           string
	To             string
	Subject        string
	Mailbox        string
	Since          *time.Time
	Until          *time.Time
	Unread         bool
	Flagged        bool
	HasAttachment  bool
	IncludeDeleted bool
	Limit          int
}

// buildWhere returns a SQL WHERE fragment with ? placeholders plus the
// arguments to bind. Values are never interpolated into the string.
//
// Table aliases assumed by the caller: m=messages, subj=subjects,
// addr=addresses (sender), mb=mailboxes.
func (f Filter) buildWhere() (string, []any) {
	var clauses []string
	var args []any

	if f.ROWID != 0 {
		clauses = append(clauses, "m.ROWID = ?")
		args = append(args, f.ROWID)
	}
	if !f.IncludeDeleted {
		clauses = append(clauses, "m.deleted = 0")
	}
	if f.From != "" {
		clauses = append(clauses, "(addr.address LIKE ? OR addr.comment LIKE ?)")
		args = append(args, "%"+f.From+"%", "%"+f.From+"%")
	}
	if f.Subject != "" {
		clauses = append(clauses, "subj.subject LIKE ?")
		args = append(args, "%"+f.Subject+"%")
	}
	if f.Mailbox != "" {
		clauses = append(clauses, "mb.url LIKE ?")
		args = append(args, "%"+f.Mailbox+"%")
	}
	if f.Since != nil {
		clauses = append(clauses, "m.date_received >= ?")
		args = append(args, TimeToCocoa(*f.Since))
	}
	if f.Until != nil {
		clauses = append(clauses, "m.date_received <= ?")
		args = append(args, TimeToCocoa(*f.Until))
	}
	if f.Unread {
		clauses = append(clauses, "m.read = 0")
	}
	if f.Flagged {
		clauses = append(clauses, "m.flagged = 1")
	}
	if f.HasAttachment {
		clauses = append(clauses,
			"EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = m.ROWID)")
	}
	if f.To != "" {
		clauses = append(clauses,
			"EXISTS (SELECT 1 FROM recipients r JOIN addresses ra ON r.address = ra.ROWID "+
				"WHERE r.message_id = m.ROWID AND ra.address LIKE ?)")
		args = append(args, "%"+f.To+"%")
	}

	if len(clauses) == 0 {
		return "1=1", nil
	}
	return strings.Join(clauses, " AND "), args
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailstore/ -run TestBuildWhere -v`
Expected: PASS (all six tests)

- [ ] **Step 5: Commit**

```bash
git add internal/mailstore/filter.go internal/mailstore/filter_test.go
git commit -m "feat: add shared filter struct with SQL clause builder"
```

---

### Task 5: Envelope Index store

**Files:**
- Create: `internal/mailstore/store.go`
- Test: `internal/mailstore/store_test.go`

**Interfaces:**
- Consumes: `Paths` (Task 2), `CocoaToTime` (Task 1), `Filter` (Task 4), `testdata.BuildMailDir` (Task 3)
- Produces:
  - `mailstore.Store` with `Open(paths *Paths) (*Store, error)`, `(*Store) Close() error`
  - `(*Store) Query(f Filter) ([]MessageMeta, error)`
  - `(*Store) Counts() (messages, mailboxes int, err error)`
  - `type MessageMeta struct { ROWID int64; MessageID, Subject, SenderName, SenderAddr, MailboxURL, Summary string; DateSent, DateReceived time.Time; ConversationID int64; Read, Flagged bool }`

**Context for the implementer:** The database must be opened read-only and immutable — Mail holds a write lock while running, and `immutable=1` sidesteps it entirely. Never open it any other way.

- [ ] **Step 1: Write the failing test**

Create `internal/mailstore/store_test.go`:

```go
package mailstore

import (
	"testing"
	"time"

	"github.com/harasuke/applemail/internal/testdata"
)

func openFixtureStore(t *testing.T) *Store {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())
	paths, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("DiscoverPaths: %v", err)
	}
	s, err := Open(paths)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestQueryReturnsAllMessages(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d messages, want 5", len(got))
	}
}

func TestQueryJoinsSubjectAndSender(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{Subject: "Plain text hello"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	m := got[0]
	if m.Subject != "Plain text hello" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if m.SenderAddr != "alice@example.com" {
		t.Errorf("SenderAddr = %q", m.SenderAddr)
	}
	if m.SenderName != "Alice Smith" {
		t.Errorf("SenderName = %q", m.SenderName)
	}
	if m.MailboxURL != "imap://user@example.com/INBOX" {
		t.Errorf("MailboxURL = %q", m.MailboxURL)
	}
}

func TestQueryConvertsCocoaDates(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{Subject: "Plain text hello"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := time.Date(2026, 8, 5, 20, 11, 22, 0, time.UTC)
	if !got[0].DateSent.Equal(want) {
		t.Errorf("DateSent = %v, want %v", got[0].DateSent, want)
	}
}

func TestQueryFilterUnread(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{Unread: true})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, m := range got {
		if m.Read {
			t.Errorf("message %d is read but was returned by an unread filter", m.ROWID)
		}
	}
	if len(got) != 4 {
		t.Errorf("got %d unread, want 4", len(got))
	}
}

func TestQueryFilterMailbox(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{Mailbox: "Archive"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d in Archive, want 1", len(got))
	}
}

func TestQueryFilterHasAttachment(t *testing.T) {
	s := openFixtureStore(t)
	// The fixture set records no attachment rows, so this must return none
	// even though message 3 carries an attachment part in its MIME.
	got, err := s.Query(Filter{HasAttachment: true})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d with attachments, want 0", len(got))
	}
}

func TestQueryLimit(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{Limit: 2})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestQueryOrdersByDateReceivedDescending(t *testing.T) {
	s := openFixtureStore(t)
	got, err := s.Query(Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].DateReceived.Before(got[i].DateReceived) {
			t.Errorf("result %d is older than result %d", i-1, i)
		}
	}
}

func TestCounts(t *testing.T) {
	s := openFixtureStore(t)
	msgs, boxes, err := s.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if msgs != 5 {
		t.Errorf("messages = %d, want 5", msgs)
	}
	if boxes != 2 {
		t.Errorf("mailboxes = %d, want 2", boxes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailstore/ -run 'TestQuery|TestCounts' -v`
Expected: FAIL — `undefined: Open`

- [ ] **Step 3: Write minimal implementation**

Create `internal/mailstore/store.go`:

```go
package mailstore

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// MessageMeta is the metadata for one message, read entirely from the
// Envelope Index. It contains no body — bodies live in .emlx files.
type MessageMeta struct {
	ROWID          int64
	MessageID      string
	Subject        string
	SenderName     string
	SenderAddr     string
	MailboxURL     string
	Summary        string
	DateSent       time.Time
	DateReceived   time.Time
	ConversationID int64
	Read           bool
	Flagged        bool
}

// Store is a read-only handle on Mail's Envelope Index.
type Store struct {
	db    *sql.DB
	paths *Paths
}

// Open opens the Envelope Index read-only and immutable. Mail holds a
// write lock while running; immutable=1 avoids contention entirely and
// guarantees this process can never modify the user's mail.
func Open(paths *Paths) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", paths.IndexPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open envelope index: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open envelope index: %w", err)
	}
	return &Store{db: db, paths: paths}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Paths returns the resolved Mail paths this store was opened from.
func (s *Store) Paths() *Paths { return s.paths }

const baseQuery = `
SELECT m.ROWID, COALESCE(m.message_id, ''), COALESCE(subj.subject, ''),
       COALESCE(addr.comment, ''), COALESCE(addr.address, ''),
       COALESCE(mb.url, ''), COALESCE(summ.summary, ''),
       COALESCE(m.date_sent, 0), COALESCE(m.date_received, 0),
       COALESCE(m.conversation_id, 0), COALESCE(m.read, 0), COALESCE(m.flagged, 0)
FROM messages m
LEFT JOIN subjects  subj ON m.subject = subj.ROWID
LEFT JOIN addresses addr ON m.sender  = addr.ROWID
LEFT JOIN mailboxes mb   ON m.mailbox = mb.ROWID
LEFT JOIN summaries summ ON m.summary = summ.ROWID
WHERE %s
ORDER BY m.date_received DESC`

// Query returns message metadata matching the filter, newest first.
func (s *Store) Query(f Filter) ([]MessageMeta, error) {
	where, args := f.buildWhere()
	q := fmt.Sprintf(baseQuery, where)
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []MessageMeta
	for rows.Next() {
		var m MessageMeta
		var sent, recv float64
		var read, flagged int
		if err := rows.Scan(&m.ROWID, &m.MessageID, &m.Subject,
			&m.SenderName, &m.SenderAddr, &m.MailboxURL, &m.Summary,
			&sent, &recv, &m.ConversationID, &read, &flagged); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.DateSent = CocoaToTime(sent)
		m.DateReceived = CocoaToTime(recv)
		m.Read = read != 0
		m.Flagged = flagged != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// Counts returns the number of non-deleted messages and of mailboxes.
func (s *Store) Counts() (messages, mailboxes int, err error) {
	if err = s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE deleted = 0").
		Scan(&messages); err != nil {
		return 0, 0, fmt.Errorf("count messages: %w", err)
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM mailboxes").
		Scan(&mailboxes); err != nil {
		return 0, 0, fmt.Errorf("count mailboxes: %w", err)
	}
	return messages, mailboxes, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailstore/ -v`
Expected: PASS (all store tests plus earlier ones)

- [ ] **Step 5: Commit**

```bash
git add internal/mailstore/store.go internal/mailstore/store_test.go
git commit -m "feat: add read-only Envelope Index store with filtered queries"
```

---

### Task 6: .emlx path resolution

**Files:**
- Create: `internal/mailstore/emlxpath.go`
- Test: `internal/mailstore/emlxpath_test.go`

**Interfaces:**
- Consumes: `Paths` (Task 2), `testdata.BuildMailDir` (Task 3)
- Produces:
  - `mailstore.NewPathIndex(paths *Paths) (*PathIndex, error)`
  - `(*PathIndex) Resolve(rowid int64) (string, bool)`
  - `(*PathIndex) DirCount() int`

**Context for the implementer:** A naive implementation would walk the filesystem searching for `<rowid>.emlx` on every lookup, which is unacceptably slow. Instead, walk once at construction to collect the `Messages` directories (typically 10–50 regardless of mailbox size), then resolve each ROWID with a `stat` against each candidate directory.

Mail also stores messages under `<rowid>.partial.emlx` when only headers were downloaded; both forms must be tried.

- [ ] **Step 1: Write the failing test**

Create `internal/mailstore/emlxpath_test.go`:

```go
package mailstore

import (
	"path/filepath"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

func newFixturePathIndex(t *testing.T) (*PathIndex, string) {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())
	paths, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("DiscoverPaths: %v", err)
	}
	idx, err := NewPathIndex(paths)
	if err != nil {
		t.Fatalf("NewPathIndex: %v", err)
	}
	return idx, root
}

func TestPathIndexResolvesKnownMessage(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	got, ok := idx.Resolve(1)
	if !ok {
		t.Fatal("Resolve(1) = not found, want found")
	}
	if filepath.Base(got) != "1.emlx" {
		t.Errorf("Resolve(1) = %q, want basename 1.emlx", got)
	}
}

func TestPathIndexResolvesAcrossMailboxes(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	// ROWID 3 lives in Archive.mbox, not INBOX.mbox.
	got, ok := idx.Resolve(3)
	if !ok {
		t.Fatal("Resolve(3) = not found, want found")
	}
	if filepath.Base(got) != "3.emlx" {
		t.Errorf("Resolve(3) = %q, want basename 3.emlx", got)
	}
}

func TestPathIndexMissingMessage(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	if _, ok := idx.Resolve(99999); ok {
		t.Error("Resolve(99999) = found, want not found")
	}
}

func TestPathIndexCollectsMessageDirs(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	// The fixture creates INBOX.mbox and Archive.mbox.
	if n := idx.DirCount(); n != 2 {
		t.Errorf("DirCount() = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailstore/ -run TestPathIndex -v`
Expected: FAIL — `undefined: NewPathIndex`

- [ ] **Step 3: Write minimal implementation**

Create `internal/mailstore/emlxpath.go`:

```go
package mailstore

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PathIndex maps message ROWIDs to their .emlx files on disk.
//
// The filesystem is walked once at construction to collect the Messages
// directories — typically 10-50 of them regardless of how many messages
// exist — so that each lookup is a handful of stat calls rather than a
// recursive search.
type PathIndex struct {
	dirs []string
}

// NewPathIndex walks the Mail version directory collecting message dirs.
func NewPathIndex(paths *Paths) (*PathIndex, error) {
	var dirs []string
	err := filepath.WalkDir(paths.VersionDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree must not abort the whole walk.
			if os.IsPermission(err) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() && d.Name() == "Messages" {
			dirs = append(dirs, path)
			return fs.SkipDir // no message dirs nest inside another
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk mail directory: %w", err)
	}
	return &PathIndex{dirs: dirs}, nil
}

// DirCount reports how many Messages directories were found.
func (p *PathIndex) DirCount() int { return len(p.dirs) }

// Resolve returns the .emlx path for a ROWID. Mail writes a .partial.emlx
// when only the headers were downloaded, so both forms are tried.
func (p *PathIndex) Resolve(rowid int64) (string, bool) {
	names := []string{
		fmt.Sprintf("%d.emlx", rowid),
		fmt.Sprintf("%d.partial.emlx", rowid),
	}
	for _, dir := range p.dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, true
			}
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailstore/ -v`
Expected: PASS (all four path-index tests plus earlier ones)

- [ ] **Step 5: Commit**

```bash
git add internal/mailstore/emlxpath.go internal/mailstore/emlxpath_test.go
git commit -m "feat: resolve message ROWIDs to .emlx paths via a cached directory map"
```

---

### Task 7: .emlx parser

**Files:**
- Create: `internal/emlx/parse.go`
- Test: `internal/emlx/parse_test.go`

**Interfaces:**
- Consumes: `testdata.BuildMailDir`, `testdata.EmlxPath`, `testdata.DefaultMessages` (Task 3)
- Produces:
  - `emlx.Parse(raw []byte) (*File, error)`
  - `emlx.ParseFile(path string) (*File, error)`
  - `type File struct { ByteCount int; MIME []byte; Plist []byte }`
  - `emlx.ErrBadByteCount` (sentinel error)

**Context for the implementer:** The `.emlx` format is three parts:

1. A decimal byte count in ASCII, terminated by `0x0a`
2. Exactly that many bytes of RFC-822 MIME content
3. An Apple plist trailer

The byte count is the authority for where MIME ends. Real files occasionally carry a count that overruns the file; clamp rather than panic, and report it — never index past the end of the slice.

- [ ] **Step 1: Write the failing test**

Create `internal/emlx/parse_test.go`:

```go
package emlx

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

func TestParseSplitsThreeParts(t *testing.T) {
	mime := "Subject: Hi\r\n\r\nBody text\r\n"
	plist := "<?xml version=\"1.0\"?><plist></plist>\n"
	// The three parts: count line, then exactly that many bytes of MIME,
	// then the plist trailer.
	raw := []byte(strconv.Itoa(len(mime)) + "\n" + mime + plist)

	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.ByteCount != len(mime) {
		t.Errorf("ByteCount = %d, want %d", f.ByteCount, len(mime))
	}
	if string(f.MIME) != mime {
		t.Errorf("MIME = %q, want %q", f.MIME, mime)
	}
	if !strings.Contains(string(f.Plist), "<plist>") {
		t.Errorf("Plist = %q, want the trailer", f.Plist)
	}
}

func TestParseFileOnFixture(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)

	f, err := ParseFile(testdata.EmlxPath(root, msgs[0]))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if string(f.MIME) != msgs[0].RawMIME {
		t.Error("parsed MIME does not match the fixture")
	}
}

func TestParseRejectsMissingByteCountLine(t *testing.T) {
	_, err := Parse([]byte("no newline here"))
	if !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}

func TestParseRejectsNonNumericByteCount(t *testing.T) {
	_, err := Parse([]byte("abc\r\nSubject: x\r\n"))
	if !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}

func TestParseClampsOverlongByteCount(t *testing.T) {
	mime := "Subject: Hi\r\n\r\nShort\r\n"
	// Declare far more bytes than the file actually contains.
	raw := []byte("99999\n" + mime)

	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse should clamp rather than fail: %v", err)
	}
	if string(f.MIME) != mime {
		t.Errorf("MIME = %q, want the available content %q", f.MIME, mime)
	}
	if len(f.Plist) != 0 {
		t.Errorf("Plist = %q, want empty when the count overruns", f.Plist)
	}
}

func TestParseEmptyInput(t *testing.T) {
	if _, err := Parse(nil); !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}

```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emlx/ -v`
Expected: FAIL — `undefined: Parse`

- [ ] **Step 3: Write minimal implementation**

Create `internal/emlx/parse.go`:

```go
// Package emlx parses Apple Mail's .emlx message files.
//
// An .emlx file has three parts: a decimal byte count terminated by a
// newline, exactly that many bytes of RFC-822 MIME content, and an Apple
// plist trailer holding Mail's own metadata.
package emlx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ErrBadByteCount means the leading byte-count line was missing or unparseable.
var ErrBadByteCount = errors.New("malformed .emlx byte count line")

// File is a parsed .emlx.
type File struct {
	ByteCount int    // as declared on the first line
	MIME      []byte // the RFC-822 message
	Plist     []byte // Apple's metadata trailer
}

// Parse splits raw .emlx bytes into its three parts.
//
// The declared byte count decides where MIME ends. Some real files declare
// more bytes than they contain, so the count is clamped to the available
// data rather than treated as fatal.
func Parse(raw []byte) (*File, error) {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, fmt.Errorf("%w: no newline in %d bytes", ErrBadByteCount, len(raw))
	}

	countStr := string(bytes.TrimSpace(raw[:nl]))
	count, err := strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return nil, fmt.Errorf("%w: %q", ErrBadByteCount, countStr)
	}

	rest := raw[nl+1:]
	end := count
	if end > len(rest) {
		end = len(rest) // clamp: never index past the end
	}

	return &File{
		ByteCount: count,
		MIME:      rest[:end],
		Plist:     rest[end:],
	}, nil
}

// ParseFile reads and parses an .emlx file from disk.
func ParseFile(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emlx/ -v`
Expected: PASS (all six tests)

- [ ] **Step 5: Commit**

```bash
git add internal/emlx/parse.go internal/emlx/parse_test.go
git commit -m "feat: parse the three-part .emlx format"
```

---

### Task 8: Message text extraction

**Files:**
- Create: `internal/emlx/text.go`
- Test: `internal/emlx/text_test.go`

**Interfaces:**
- Consumes: `emlx.File` (Task 7), `testdata` fixtures (Task 3)
- Produces:
  - `emlx.Extract(f *File) (*Message, error)`
  - `type Message struct { Headers mail.Header; Subject, From, To, MessageID string; Text, TextSource string; HTML string; Attachments []Attachment }`
  - `type Attachment struct { Name, MIME string; Size int }`
  - `emlx.HTMLToText(html string) string`

**Context for the implementer:** This is the task with the most edge cases, and Go's stdlib does not cover all of them for free.

- Headers may be RFC 2047 encoded-words (`=?UTF-8?B?...?=`). Use `mime.WordDecoder` with a `CharsetReader` so non-UTF-8 charsets do not fail.
- Bodies may be `quoted-printable` or `base64`. `mime/quotedprintable` handles the former; `encoding/base64` the latter.
- Multipart bodies need `mime/multipart` driven by the boundary from `Content-Type`.
- Prefer `text/plain` over `text/html`. Only convert HTML when no plain part exists, and record which was used in `TextSource`.
- Declared charsets are decoded through the IANA registry
  (`golang.org/x/text/encoding/ianaindex`), covering ISO-8859-*, KOI8-R,
  Shift_JIS, GB2312, EUC-KR, and Windows-125x. Only an unrecognized label
  or a failed decode falls back: UTF-8 if the bytes are already valid, then
  latin-1, which maps every byte and never fails.
- **A fallback must be declared, never silent.** When the chain falls back,
  `TextSource` records it (`text/plain; charset-fallback=latin-1`) so a
  consumer can tell decoded text from best-effort text.

Add both dependencies first:
`go get golang.org/x/net/html golang.org/x/text/encoding/ianaindex`.

- [ ] **Step 1: Write the failing test**

Create `internal/emlx/text_test.go`:

```go
package emlx

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

func extractFixture(t *testing.T, idx int) *Message {
	t.Helper()
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := ParseFile(testdata.EmlxPath(root, msgs[idx]))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	m, err := Extract(f)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return m
}

func TestExtractPlainText(t *testing.T) {
	m := extractFixture(t, 0)
	if !strings.Contains(m.Text, "Just saying hello") {
		t.Errorf("Text = %q, want the plain body", m.Text)
	}
	if m.TextSource != "text/plain" {
		t.Errorf("TextSource = %q, want text/plain", m.TextSource)
	}
	if m.Subject != "Plain text hello" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if m.MessageID != "<plain-1@example.com>" {
		t.Errorf("MessageID = %q", m.MessageID)
	}
}

func TestExtractHTMLOnlyConvertsToText(t *testing.T) {
	m := extractFixture(t, 1)
	if m.TextSource != "text/html" {
		t.Errorf("TextSource = %q, want text/html", m.TextSource)
	}
	if !strings.Contains(m.Text, "This week in news") {
		t.Errorf("Text = %q, want the converted text", m.Text)
	}
	if strings.Contains(m.Text, "<b>") || strings.Contains(m.Text, "<p>") {
		t.Errorf("Text = %q, want no markup", m.Text)
	}
	if m.HTML == "" {
		t.Error("HTML is empty, want the original markup retained")
	}
}

func TestExtractMultipartPrefersPlainAndListsAttachments(t *testing.T) {
	m := extractFixture(t, 2)
	if !strings.Contains(m.Text, "Please see attached") {
		t.Errorf("Text = %q, want the plain part", m.Text)
	}
	if m.TextSource != "text/plain" {
		t.Errorf("TextSource = %q, want text/plain", m.TextSource)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(m.Attachments))
	}
	if m.Attachments[0].Name != "report.pdf" {
		t.Errorf("attachment name = %q, want report.pdf", m.Attachments[0].Name)
	}
	if m.Attachments[0].MIME != "application/pdf" {
		t.Errorf("attachment MIME = %q, want application/pdf", m.Attachments[0].MIME)
	}
}

func TestExtractDecodesEncodedWordSubject(t *testing.T) {
	m := extractFixture(t, 3)
	if m.Subject != "Perché è importante" {
		t.Errorf("Subject = %q, want the decoded form", m.Subject)
	}
}

func TestExtractDecodesQuotedPrintableBody(t *testing.T) {
	m := extractFixture(t, 3)
	if !strings.Contains(m.Text, "perché") {
		t.Errorf("Text = %q, want quoted-printable decoded", m.Text)
	}
	if strings.Contains(m.Text, "=C3=A9") {
		t.Errorf("Text = %q, still contains quoted-printable escapes", m.Text)
	}
}

func TestDecodeCharsetHandlesRegisteredEncodings(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		charset string
		want    string
	}{
		{"utf-8", []byte("perché"), "utf-8", "perché"},
		{"iso-8859-1", []byte{0x70, 0x65, 0x72, 0x63, 0x68, 0xE9}, "iso-8859-1", "perché"},
		{"windows-1252", []byte{0x93, 0x68, 0x69, 0x94}, "windows-1252", "“hi”"},
		{"koi8-r", []byte{0xD0, 0xD2, 0xC9, 0xD7, 0xC5, 0xD4}, "koi8-r", "привет"},
		{"shift_jis", []byte{0x93, 0xFA, 0x96, 0x7B}, "shift_jis", "日本"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, fallback, err := decodeCharset(tt.charset, bytes.NewReader(tt.raw))
			if err != nil {
				t.Fatalf("decodeCharset: %v", err)
			}
			if fallback != "" {
				t.Errorf("fallback = %q, want none for a registered charset", fallback)
			}
			got, _ := io.ReadAll(r)
			if string(got) != tt.want {
				t.Errorf("decoded = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeCharsetDeclaresFallback(t *testing.T) {
	// An unknown label with bytes that are not valid UTF-8 must fall back
	// to latin-1 AND say so, rather than emitting silent mojibake.
	r, fallback, err := decodeCharset("x-unknown-charset", bytes.NewReader([]byte{0xE9}))
	if err != nil {
		t.Fatalf("decodeCharset: %v", err)
	}
	if fallback != "latin-1" {
		t.Errorf("fallback = %q, want latin-1", fallback)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "é" {
		t.Errorf("decoded = %q, want é", got)
	}
}

func TestSourceLabelRecordsFallback(t *testing.T) {
	if got := sourceLabel("text/plain", ""); got != "text/plain" {
		t.Errorf("sourceLabel with no fallback = %q, want text/plain", got)
	}
	want := "text/plain; charset-fallback=latin-1"
	if got := sourceLabel("text/plain", "latin-1"); got != want {
		t.Errorf("sourceLabel = %q, want %q", got, want)
	}
}

func TestHTMLToTextStripsTagsAndDecodesEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips tags", "<p>Hello <b>world</b></p>", "Hello world"},
		{"decodes entities", "<p>caf&eacute; &amp; bar</p>", "café & bar"},
		{"drops script content", "<p>Hi</p><script>alert(1)</script>", "Hi"},
		{"drops style content", "<style>p{color:red}</style><p>Hi</p>", "Hi"},
		// Paragraphs stay separated by exactly one blank line: block tags
		// each contribute a newline, and runs of 3+ collapse to 2.
		{"collapses whitespace", "<p>a</p>\n\n\n<p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.TrimSpace(HTMLToText(tt.in))
			if got != tt.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emlx/ -run 'TestExtract|TestHTMLToText' -v`
Expected: FAIL — `undefined: Extract`

- [ ] **Step 3: Add the HTML and charset dependencies**

```bash
go get golang.org/x/net/html golang.org/x/text/encoding/ianaindex
```

- [ ] **Step 4: Write minimal implementation**

Create `internal/emlx/text.go`:

```go
package emlx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/ianaindex"
)

// Attachment describes one attached part.
type Attachment struct {
	Name string
	MIME string
	Size int
}

// Message is a decoded email: headers, readable text, and attachments.
type Message struct {
	Headers     mail.Header
	Subject     string
	From        string
	To          string
	MessageID   string
	Text        string // always plain text, never markup
	TextSource  string // "text/plain", "text/html", optionally "; charset-fallback=…"
	HTML        string // original markup, when present
	Attachments []Attachment

	// htmlFallback remembers the charset fallback of the HTML part until
	// TextSource is set, in case HTML is the only body present.
	htmlFallback string
}

// decoder handles RFC 2047 encoded-words with a tolerant charset reader.
var decoder = mime.WordDecoder{
	CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		r, _, err := decodeCharset(charset, input)
		return r, err
	},
}

// decodeCharset converts a labelled charset to UTF-8, reporting whether it
// had to fall back.
//
// Declared labels are resolved through the IANA registry, which covers the
// ISO-8859 family, KOI8-R, Shift_JIS, GB2312, EUC-KR, and Windows-125x.
// Only an unknown label or a failed decode falls back: UTF-8 when the bytes
// are already valid, then latin-1, which maps every byte and never fails.
//
// fallback is non-empty when the declared charset was not used, so callers
// can declare it rather than silently emitting mojibake.
func decodeCharset(charset string, r io.Reader) (out io.Reader, fallback string, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}

	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		if utf8.Valid(raw) {
			return bytes.NewReader(raw), "", nil
		}
		return strings.NewReader(latin1ToUTF8(raw)), "latin-1", nil
	}

	// Resolve the declared label through the IANA registry.
	if enc, encErr := ianaindex.MIME.Encoding(charset); encErr == nil && enc != nil {
		decoded, decErr := enc.NewDecoder().Bytes(raw)
		if decErr == nil {
			return bytes.NewReader(decoded), "", nil
		}
	}

	// Unknown label or failed decode: UTF-8 if it already is, else latin-1.
	if utf8.Valid(raw) {
		return bytes.NewReader(raw), "utf-8", nil
	}
	return strings.NewReader(latin1ToUTF8(raw)), "latin-1", nil
}

// latin1ToUTF8 widens every byte to its matching rune. Cannot fail.
func latin1ToUTF8(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// sourceLabel renders a media type plus any charset fallback that fired.
func sourceLabel(mediaType, fallback string) string {
	if fallback == "" {
		return mediaType
	}
	return mediaType + "; charset-fallback=" + fallback
}

func decodeHeader(v string) string {
	if v == "" {
		return ""
	}
	out, err := decoder.DecodeHeader(v)
	if err != nil {
		return v // an undecodable header is still better than nothing
	}
	return out
}

// Extract decodes a parsed .emlx into headers, text, and attachments.
func Extract(f *File) (*Message, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(f.MIME))
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	m := &Message{
		Headers:   msg.Header,
		Subject:   decodeHeader(msg.Header.Get("Subject")),
		From:      decodeHeader(msg.Header.Get("From")),
		To:        decodeHeader(msg.Header.Get("To")),
		MessageID: strings.TrimSpace(msg.Header.Get("Message-ID")),
	}

	ctype := msg.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		if err := extractMultipart(m, msg.Body, params["boundary"]); err != nil {
			return nil, err
		}
	} else {
		body, fallback, err := decodePart(msg.Body,
			msg.Header.Get("Content-Transfer-Encoding"), params["charset"])
		if err != nil {
			return nil, err
		}
		assignBody(m, mediaType, body, fallback)
	}

	if m.Text == "" && m.HTML != "" {
		m.Text = HTMLToText(m.HTML)
		m.TextSource = sourceLabel("text/html", m.htmlFallback)
	}
	return m, nil
}

// extractMultipart walks the parts, preferring text/plain for the body and
// collecting anything with a filename as an attachment.
func extractMultipart(m *Message, body io.Reader, boundary string) error {
	if boundary == "" {
		return fmt.Errorf("multipart message with no boundary")
	}
	mr := multipart.NewReader(body, boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read part: %w", err)
		}

		partType, partParams, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			partType = "application/octet-stream"
			partParams = map[string]string{}
		}

		filename := part.FileName()
		if filename == "" {
			filename = partParams["name"]
		}
		if filename != "" {
			raw, _ := io.ReadAll(part)
			// multipart transparently decodes quoted-printable but NOT
			// base64, so decode it here — otherwise Size reports the
			// encoded length, roughly a third larger than the real file.
			if strings.EqualFold(
				strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")), "base64") {
				if decoded, err := base64.StdEncoding.DecodeString(
					strings.Join(strings.Fields(string(raw)), "")); err == nil {
					raw = decoded
				}
			}
			m.Attachments = append(m.Attachments, Attachment{
				Name: decodeHeader(filename),
				MIME: partType,
				Size: len(raw),
			})
			part.Close()
			continue
		}

		if strings.HasPrefix(partType, "multipart/") {
			if err := extractMultipart(m, part, partParams["boundary"]); err != nil {
				return err
			}
			part.Close()
			continue
		}

		decoded, fallback, err := decodePart(part,
			part.Header.Get("Content-Transfer-Encoding"), partParams["charset"])
		part.Close()
		if err != nil {
			continue // one bad part must not lose the whole message
		}
		assignBody(m, partType, decoded, fallback)
	}
}

// assignBody records a decoded part, preferring plain text for m.Text.
// Any charset fallback is carried into TextSource so it is never silent.
func assignBody(m *Message, mediaType, body, fallback string) {
	switch {
	case strings.HasPrefix(mediaType, "text/plain"):
		if m.Text == "" {
			m.Text = body
			m.TextSource = sourceLabel("text/plain", fallback)
		}
	case strings.HasPrefix(mediaType, "text/html"):
		if m.HTML == "" {
			m.HTML = body
			m.htmlFallback = fallback
		}
	}
}

// decodePart applies the transfer encoding, then the charset. The returned
// fallback is non-empty when the declared charset could not be used.
func decodePart(r io.Reader, encoding, charset string) (text, fallback string, err error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	converted, fallback, err := decodeCharset(charset, r)
	if err != nil {
		return "", "", err
	}
	out, err := io.ReadAll(converted)
	if err != nil {
		return "", "", err
	}
	return string(out), fallback, nil
}

var whitespaceRuns = regexp.MustCompile(`\n{3,}`)

// HTMLToText renders markup as readable plain text: tags removed, entities
// decoded, script and style content dropped, whitespace collapsed.
func HTMLToText(markup string) string {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return markup
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head":
				return
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			}
		}
	}
	walk(doc)

	lines := strings.Split(sb.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	joined := strings.Join(lines, "\n")
	return strings.TrimSpace(whitespaceRuns.ReplaceAllString(joined, "\n\n"))
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/emlx/ -v`
Expected: PASS (all extraction and HTML tests plus the Task 7 parser tests)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/emlx/text.go internal/emlx/text_test.go
git commit -m "feat: decode message headers, bodies, charsets, and attachments"
```

---

### Task 9: Link extraction, normalization, and classification

**Files:**
- Create: `internal/analyze/links.go`
- Test: `internal/analyze/links_test.go`

**Interfaces:**
- Consumes: `emlx.Message` (Task 8)
- Produces:
  - `analyze.ExtractLinks(m *emlx.Message) []Link`
  - `analyze.Normalize(rawURL string) (canonical string, dedupKey string)`
  - `analyze.Classify(u string, anchor string, inImage bool) LinkClass`
  - `type Link struct { URLCanonical, URLOriginal, Domain, AnchorText, DedupKey string; Class LinkClass; AnchorMismatch bool }`
  - `type LinkClass string` with constants `ClassContent`, `ClassTracking`, `ClassAction`

**Context for the implementer:** This is the task that makes the tool useful to an LLM. Bulk senders wrap nearly every URL in tracking redirects, so a naive extractor returns noise. Three jobs:

1. **Normalize** — strip tracking parameters (`trk`, `trkEmail`, `midToken`, `eid`, `lipi`, `refId`, and anything `utm_*`), and unwrap redirect wrappers when the destination sits in the query string (`url=`, `u=`, `redirect=`, `target=`).
2. **Canonicalize** — derive a stable `dedup_key` where the pattern is recognizable. For LinkedIn job URLs, extract the numeric ID from `/jobs/view/<id>` and produce `linkedin:job:<id>`, so the same posting arriving in three emails collapses to one row. Otherwise the key is the canonical URL itself.
3. **Classify** — `ClassTracking` for pixels, beacons, and analytics hosts; `ClassAction` for unsubscribe and confirmation links that *do* something and must never be auto-opened; `ClassContent` for everything else, which is what the LLM should read.

`AnchorMismatch` is true when the visible anchor text names a different domain than the actual href. Report it as a fact; do not label it "suspicious".

**The CLI makes no network requests.** Unwrapping is done offline from query-string parameters only — never by following a redirect.

- [ ] **Step 1: Write the failing test**

Create `internal/analyze/links_test.go`:

```go
package analyze

import (
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/emlx"
	"github.com/harasuke/applemail/internal/testdata"
)

func TestNormalizeStripsTrackingParams(t *testing.T) {
	in := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123&eid=abc"
	canonical, _ := Normalize(in)
	for _, bad := range []string{"trk=", "midToken=", "eid="} {
		if strings.Contains(canonical, bad) {
			t.Errorf("canonical = %q, still contains %q", canonical, bad)
		}
	}
	if !strings.Contains(canonical, "/jobs/view/4021887364") {
		t.Errorf("canonical = %q, want the job path preserved", canonical)
	}
}

func TestNormalizeStripsUTMParams(t *testing.T) {
	in := "https://example.com/page?utm_source=news&utm_campaign=x&id=7"
	canonical, _ := Normalize(in)
	if strings.Contains(canonical, "utm_") {
		t.Errorf("canonical = %q, still contains utm params", canonical)
	}
	if !strings.Contains(canonical, "id=7") {
		t.Errorf("canonical = %q, want the real param kept", canonical)
	}
}

func TestNormalizeUnwrapsRedirect(t *testing.T) {
	in := "https://click.example.com/redirect?url=https%3A%2F%2Freal.example.org%2Farticle%2F9"
	canonical, _ := Normalize(in)
	if canonical != "https://real.example.org/article/9" {
		t.Errorf("canonical = %q, want the unwrapped destination", canonical)
	}
}

func TestNormalizeLinkedInJobsDedupKey(t *testing.T) {
	a := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123"
	b := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-digest&midToken=AQE999"

	_, keyA := Normalize(a)
	_, keyB := Normalize(b)

	if keyA != "linkedin:job:4021887364" {
		t.Errorf("keyA = %q, want linkedin:job:4021887364", keyA)
	}
	if keyA != keyB {
		t.Errorf("keyA = %q, keyB = %q — the same job must share a key", keyA, keyB)
	}
}

func TestNormalizeNonLinkedInKeyIsCanonicalURL(t *testing.T) {
	canonical, key := Normalize("https://example.com/a?utm_source=x")
	if key != canonical {
		t.Errorf("key = %q, want it to equal canonical %q", key, canonical)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		anchor  string
		inImage bool
		want    LinkClass
	}{
		{"image beacon", "https://track.example.org/pixel.gif", "", true, ClassTracking},
		{"tracking host", "https://track.linkedin.com/px?e=1", "", false, ClassTracking},
		{"unsubscribe path", "https://example.org/unsub?u=42", "Unsubscribe", false, ClassAction},
		{"unsubscribe anchor", "https://example.org/x", "unsubscribe", false, ClassAction},
		{"real article", "https://example.org/article/9", "Read more", false, ClassContent},
		{"job posting", "https://www.linkedin.com/jobs/view/4021887364", "Engineer", false, ClassContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.url, tt.anchor, tt.inImage); got != tt.want {
				t.Errorf("Classify(%q, %q, %v) = %q, want %q",
					tt.url, tt.anchor, tt.inImage, got, tt.want)
			}
		})
	}
}

func TestAnchorMismatchDetected(t *testing.T) {
	links := extractFromHTML(t,
		`<a href="https://evil.example.net/login">https://bank.example.com/login</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if !links[0].AnchorMismatch {
		t.Error("AnchorMismatch = false, want true when anchor names another domain")
	}
}

func TestAnchorMismatchNotFlaggedForPlainText(t *testing.T) {
	links := extractFromHTML(t, `<a href="https://example.org/x">Read more</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].AnchorMismatch {
		t.Error("AnchorMismatch = true, want false for non-URL anchor text")
	}
}

func TestExtractLinksFromPlainTextBody(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := emlx.ParseFile(testdata.EmlxPath(root, msgs[0]))
	if err != nil {
		t.Fatal(err)
	}
	m, err := emlx.Extract(f)
	if err != nil {
		t.Fatal(err)
	}

	links := ExtractLinks(m)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].URLCanonical != "https://example.com/docs" {
		t.Errorf("URLCanonical = %q", links[0].URLCanonical)
	}
	if links[0].Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", links[0].Domain)
	}
}

func TestExtractLinksDedupsLinkedInJobs(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := emlx.ParseFile(testdata.EmlxPath(root, msgs[4])) // the LinkedIn fixture
	if err != nil {
		t.Fatal(err)
	}
	m, err := emlx.Extract(f)
	if err != nil {
		t.Fatal(err)
	}

	links := ExtractLinks(m)

	var content []Link
	for _, l := range links {
		if l.Class == ClassContent {
			content = append(content, l)
		}
	}
	// Two distinct jobs: 4021887364 (listed twice) and 4055512233.
	if len(content) != 2 {
		t.Fatalf("got %d content links, want 2 after dedup: %+v", len(content), content)
	}

	keys := map[string]bool{}
	for _, l := range content {
		keys[l.DedupKey] = true
	}
	if !keys["linkedin:job:4021887364"] || !keys["linkedin:job:4055512233"] {
		t.Errorf("dedup keys = %v, want both job keys", keys)
	}
}

func TestExtractLinksKeepsOriginalURL(t *testing.T) {
	links := extractFromHTML(t,
		`<a href="https://www.linkedin.com/comm/jobs/view/1?trk=eml">Job</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if !strings.Contains(links[0].URLOriginal, "trk=eml") {
		t.Errorf("URLOriginal = %q, want the tracked original preserved", links[0].URLOriginal)
	}
}

// extractFromHTML builds a minimal HTML message and extracts its links.
func extractFromHTML(t *testing.T, body string) []Link {
	t.Helper()
	m := &emlx.Message{
		HTML:       "<html><body>" + body + "</body></html>",
		TextSource: "text/html",
	}
	return ExtractLinks(m)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analyze/ -v`
Expected: FAIL — `undefined: Normalize`

- [ ] **Step 3: Write minimal implementation**

Create `internal/analyze/links.go`:

```go
// Package analyze derives structured signals from decoded messages.
package analyze

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/harasuke/applemail/internal/emlx"
)

// LinkClass says whether a link is worth opening.
type LinkClass string

const (
	// ClassContent resolves to a real page — what an LLM should read.
	ClassContent LinkClass = "content"
	// ClassTracking is a pixel, beacon, or analytics redirect: noise.
	ClassTracking LinkClass = "tracking"
	// ClassAction performs something (unsubscribe, confirm, one-click)
	// and must never be opened automatically.
	ClassAction LinkClass = "action"
)

// Link is one extracted URL with everything needed to decide about it.
type Link struct {
	URLCanonical   string
	URLOriginal    string
	Domain         string
	AnchorText     string
	DedupKey       string
	Class          LinkClass
	AnchorMismatch bool
}

// trackingParams are query parameters that identify the recipient or the
// campaign rather than the content. Stripping them lets the same target
// arriving in different emails collapse to one entry.
var trackingParams = map[string]bool{
	"trk": true, "trkemail": true, "midtoken": true, "eid": true,
	"lipi": true, "refid": true, "li_fat_id": true,
	"ecid": true, "mkt_tok": true, "_hsenc": true, "_hsmi": true,
}

// redirectParams hold a wrapped destination URL in the query string.
var redirectParams = []string{"url", "u", "redirect", "target", "dest", "link"}

// trackingHosts serve beacons rather than pages.
var trackingHosts = []string{
	"track.", "click.", "email.", "links.", "beacon.", "px.", "open.",
}

// actionPathHints mark links that perform an action.
var actionPathHints = []string{
	"unsubscribe", "unsub", "optout", "opt-out", "confirm", "verify",
	"one-click", "oneclick", "remove",
}

var linkedInJobRe = regexp.MustCompile(`/jobs/view/(\d+)`)
var plainURLRe = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
var bareDomainRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+\.)+[a-z]{2,}\b`)

// Normalize strips tracking parameters, unwraps offline-recoverable
// redirects, and derives a stable dedup key.
//
// No network request is ever made: unwrapping reads the destination out of
// the query string, it never follows a redirect.
func Normalize(rawURL string) (canonical string, dedupKey string) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return rawURL, rawURL
	}

	// Unwrap a wrapped destination if one is present.
	q := u.Query()
	for _, p := range redirectParams {
		if v := q.Get(p); v != "" {
			if inner, err := url.Parse(v); err == nil && inner.Scheme != "" && inner.Host != "" {
				return Normalize(v)
			}
		}
	}

	for key := range q {
		lower := strings.ToLower(key)
		if trackingParams[lower] || strings.HasPrefix(lower, "utm_") {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	canonical = u.String()

	if strings.Contains(u.Host, "linkedin.com") {
		if m := linkedInJobRe.FindStringSubmatch(u.Path); m != nil {
			return canonical, "linkedin:job:" + m[1]
		}
	}
	return canonical, canonical
}

// Classify decides whether a link is content, tracking, or an action.
func Classify(rawURL, anchor string, inImage bool) LinkClass {
	if inImage {
		return ClassTracking
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ClassContent
	}
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)
	lowerAnchor := strings.ToLower(anchor)

	for _, hint := range actionPathHints {
		if strings.Contains(path, hint) || strings.Contains(lowerAnchor, hint) {
			return ClassAction
		}
	}
	for _, prefix := range trackingHosts {
		if strings.HasPrefix(host, prefix) {
			return ClassTracking
		}
	}
	if strings.HasSuffix(path, ".gif") || strings.HasSuffix(path, ".png") {
		return ClassTracking
	}
	return ClassContent
}

// ExtractLinks pulls every URL from a message, normalized, classified, and
// deduplicated by canonical key. HTML anchors carry their visible text;
// plain-text bodies contribute bare URLs.
func ExtractLinks(m *emlx.Message) []Link {
	seen := map[string]bool{}
	var out []Link

	add := func(rawURL, anchor string, inImage bool) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || !strings.HasPrefix(strings.ToLower(rawURL), "http") {
			return
		}
		canonical, key := Normalize(rawURL)
		class := Classify(canonical, anchor, inImage)

		dedup := string(class) + "|" + key
		if seen[dedup] {
			return
		}
		seen[dedup] = true

		domain := ""
		if u, err := url.Parse(canonical); err == nil {
			domain = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		}

		out = append(out, Link{
			URLCanonical:   canonical,
			URLOriginal:    rawURL,
			Domain:         domain,
			AnchorText:     strings.TrimSpace(anchor),
			DedupKey:       key,
			Class:          class,
			AnchorMismatch: anchorMismatch(canonical, anchor),
		})
	}

	if m.HTML != "" {
		walkHTMLLinks(m.HTML, add)
	}
	for _, u := range plainURLRe.FindAllString(m.Text, -1) {
		add(strings.TrimRight(u, ".,;:"), "", false)
	}
	return out
}

// walkHTMLLinks visits anchors and image sources in a document.
func walkHTMLLinks(markup string, add func(rawURL, anchor string, inImage bool)) {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "a":
				add(attr(n, "href"), nodeText(n), false)
			case "img":
				add(attr(n, "src"), attr(n, "alt"), true)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// anchorMismatch reports whether the visible text names a domain different
// from the link's real destination. This is a fact, not a verdict.
func anchorMismatch(rawURL, anchor string) bool {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	realHost := strings.TrimPrefix(strings.ToLower(u.Host), "www.")

	candidate := bareDomainRe.FindString(anchor)
	if candidate == "" {
		return false // anchor text names no domain at all
	}
	claimed := strings.TrimPrefix(strings.ToLower(candidate), "www.")
	return claimed != realHost &&
		!strings.HasSuffix(realHost, "."+claimed) &&
		!strings.HasSuffix(claimed, "."+realHost)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/analyze/ -v`
Expected: PASS (all link tests)

- [ ] **Step 5: Commit**

```bash
git add internal/analyze/links.go internal/analyze/links_test.go
git commit -m "feat: extract, normalize, and classify links with LinkedIn job dedup"
```

---

### Task 10: Header-derived signals

**Files:**
- Create: `internal/analyze/signals.go`
- Test: `internal/analyze/signals_test.go`

**Interfaces:**
- Consumes: `emlx.Message` (Task 8)
- Produces: `analyze.DeriveSignals(m *emlx.Message) Signals` and `type Signals struct { IsBulk, IsAutomated, HasUnsubscribe, ReplyToDiffers, SPFDKIMPresent bool }`

**Context for the implementer:** Signals are **facts read from headers, never judgments**. `IsBulk` is true because a `List-Unsubscribe` or `Precedence: bulk` header exists, not because the message looks promotional. Do not add `IsSpam`, `IsInteresting`, or any similar field — that assessment belongs to the LLM consuming this output, and encoding it here would assert a certainty the tool does not have.

- [ ] **Step 1: Write the failing test**

Create `internal/analyze/signals_test.go`:

```go
package analyze

import (
	"net/mail"
	"testing"

	"github.com/harasuke/applemail/internal/emlx"
)

func msgWithHeaders(h map[string][]string) *emlx.Message {
	return &emlx.Message{Headers: mail.Header(h)}
}

func TestDeriveSignalsBulkFromListUnsubscribe(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"List-Unsubscribe": {"<https://example.org/unsub>"},
	}))
	if !s.IsBulk {
		t.Error("IsBulk = false, want true when List-Unsubscribe is present")
	}
	if !s.HasUnsubscribe {
		t.Error("HasUnsubscribe = false, want true")
	}
}

func TestDeriveSignalsBulkFromPrecedence(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Precedence": {"bulk"},
	}))
	if !s.IsBulk {
		t.Error("IsBulk = false, want true when Precedence is bulk")
	}
}

func TestDeriveSignalsAutomatedFromAutoSubmitted(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Auto-Submitted": {"auto-generated"},
	}))
	if !s.IsAutomated {
		t.Error("IsAutomated = false, want true for Auto-Submitted")
	}
}

func TestDeriveSignalsAutomatedFromNoReplySender(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{},
		From:    "LinkedIn Jobs <jobs-noreply@linkedin.com>",
	}
	if s := DeriveSignals(m); !s.IsAutomated {
		t.Error("IsAutomated = false, want true for a noreply sender")
	}
}

func TestDeriveSignalsReplyToDiffers(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{"Reply-To": {"other@elsewhere.example"}},
		From:    "Sender <sender@example.com>",
	}
	if s := DeriveSignals(m); !s.ReplyToDiffers {
		t.Error("ReplyToDiffers = false, want true for a different Reply-To domain")
	}
}

func TestDeriveSignalsReplyToSameDomainDoesNotDiffer(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{"Reply-To": {"support@example.com"}},
		From:    "Sender <sender@example.com>",
	}
	if s := DeriveSignals(m); s.ReplyToDiffers {
		t.Error("ReplyToDiffers = true, want false within the same domain")
	}
}

func TestDeriveSignalsAuthPresent(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Authentication-Results": {"spf=pass dkim=pass"},
	}))
	if !s.SPFDKIMPresent {
		t.Error("SPFDKIMPresent = false, want true")
	}
}

func TestDeriveSignalsPlainMessageHasNoSignals(t *testing.T) {
	m := &emlx.Message{Headers: mail.Header{}, From: "Alice <alice@example.com>"}
	s := DeriveSignals(m)
	if s.IsBulk || s.IsAutomated || s.HasUnsubscribe || s.ReplyToDiffers || s.SPFDKIMPresent {
		t.Errorf("signals = %+v, want all false for a plain personal message", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/analyze/ -run TestDeriveSignals -v`
Expected: FAIL — `undefined: DeriveSignals`

- [ ] **Step 3: Write minimal implementation**

Create `internal/analyze/signals.go`:

```go
package analyze

import (
	"net/mail"
	"strings"

	"github.com/harasuke/applemail/internal/emlx"
)

// Signals are facts read from message headers — never judgments.
//
// There is deliberately no IsSpam or IsInteresting field: that assessment
// belongs to whoever consumes this output, and asserting it here would
// claim a certainty the headers do not provide.
type Signals struct {
	IsBulk         bool
	IsAutomated    bool
	HasUnsubscribe bool
	ReplyToDiffers bool
	SPFDKIMPresent bool
}

// DeriveSignals reads header facts from a decoded message.
func DeriveSignals(m *emlx.Message) Signals {
	get := func(name string) string {
		if m.Headers == nil {
			return ""
		}
		return strings.TrimSpace(m.Headers.Get(name))
	}

	var s Signals

	s.HasUnsubscribe = get("List-Unsubscribe") != ""
	precedence := strings.ToLower(get("Precedence"))
	s.IsBulk = s.HasUnsubscribe ||
		precedence == "bulk" || precedence == "list" || precedence == "junk" ||
		get("List-Id") != ""

	autoSubmitted := strings.ToLower(get("Auto-Submitted"))
	s.IsAutomated = (autoSubmitted != "" && autoSubmitted != "no") ||
		get("X-Auto-Response-Suppress") != "" ||
		containsNoReply(m.From)

	if replyTo := get("Reply-To"); replyTo != "" {
		s.ReplyToDiffers = domainOf(replyTo) != "" &&
			domainOf(replyTo) != domainOf(m.From)
	}

	s.SPFDKIMPresent = get("Authentication-Results") != "" ||
		get("DKIM-Signature") != "" ||
		get("Received-SPF") != ""

	return s
}

func containsNoReply(from string) bool {
	lower := strings.ToLower(from)
	for _, hint := range []string{"noreply", "no-reply", "donotreply", "do-not-reply"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// domainOf returns the domain of the first address in a header value.
func domainOf(headerValue string) string {
	addr, err := mail.ParseAddress(strings.TrimSpace(headerValue))
	if err != nil {
		if at := strings.LastIndex(headerValue, "@"); at >= 0 {
			return strings.ToLower(strings.Trim(headerValue[at+1:], "> "))
		}
		return ""
	}
	if at := strings.LastIndex(addr.Address, "@"); at >= 0 {
		return strings.ToLower(addr.Address[at+1:])
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/analyze/ -v`
Expected: PASS (signal tests plus the Task 9 link tests)

- [ ] **Step 5: Commit**

```bash
git add internal/analyze/signals.go internal/analyze/signals_test.go
git commit -m "feat: derive factual header signals"
```

---

### Task 11: Output types and renderers

**Files:**
- Create: `internal/output/types.go`
- Create: `internal/output/render.go`
- Test: `internal/output/render_test.go`

**Interfaces:**
- Consumes: nothing (pure wire format)
- Produces:
  - `output.Message`, `output.Address`, `output.Body`, `output.Flags`, `output.Link`, `output.Attachment`, `output.Signals`, `output.Summary` structs
  - `output.Renderer` interface with `Write(v any) error` and `Close() error`
  - `output.NewRenderer(format string, w io.Writer) (Renderer, error)`
  - Supported formats: `jsonl`, `json`, `table`, `text`

**Context for the implementer:** These structs are the tool's public contract — an LLM or a script parses exactly these field names, so the JSON tags matter more than the Go names.

JSONL is the default because it streams: one object per line, written and flushed as each message finishes, so memory stays flat and the first result appears immediately. `json` buffers everything into one array for `jq` convenience. The summary object carries `"type": "summary"` so a streaming consumer can identify the final line unambiguously.

- [ ] **Step 1: Write the failing test**

Create `internal/output/render_test.go`:

```go
package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func sampleMessage() Message {
	return Message{
		ID:      48213,
		Subject: "Test subject",
		From:    Address{Name: "Alice", Address: "alice@example.com"},
		Mailbox: "INBOX",
		Date:    "2026-08-14T09:31:22Z",
		Body:    Body{Text: "Hello there", Source: "text/plain"},
		Flags:   Flags{Read: true},
	}
}

func TestJSONLWritesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("jsonl", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write(sampleMessage()); err != nil {
		t.Fatal(err)
	}
	if err := r.Write(sampleMessage()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestJSONLUsesContractFieldNames(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(sampleMessage())
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "subject", "from", "mailbox", "date", "body", "flags"} {
		if _, ok := m[key]; !ok {
			t.Errorf("output is missing the %q field", key)
		}
	}
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatal("body is not an object")
	}
	if _, ok := body["text"]; !ok {
		t.Error("body is missing the \"text\" field")
	}
	if _, ok := body["source"]; !ok {
		t.Error("body is missing the \"source\" field")
	}
}

func TestJSONFormatProducesOneArray(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	r.Write(sampleMessage())
	r.Write(sampleMessage())
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("got %d elements, want 2", len(arr))
	}
}

func TestJSONFormatEmptyProducesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("json", &buf)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("got %d elements, want 0", len(arr))
	}
}

func TestSummaryCarriesTypeField(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(Summary{Total: 3, Unread: 1})
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\"", m["type"])
	}
}

func TestTableFormatShowsKeyColumns(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("table", &buf)
	if err != nil {
		t.Fatal(err)
	}
	r.Write(sampleMessage())
	r.Close()

	out := buf.String()
	for _, want := range []string{"48213", "Test subject", "alice@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestUnknownFormatIsRejected(t *testing.T) {
	if _, err := NewRenderer("yaml", &bytes.Buffer{}); err == nil {
		t.Error("NewRenderer(\"yaml\") = nil error, want a rejection")
	}
}

func TestSummaryPointerIsAlsoTagged(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(&Summary{Total: 1})
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\" for a *Summary too", m["type"])
	}
}

func TestTableRendersSummary(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("table", &buf)
	r.Write(Summary{Total: 42, Unread: 7})
	r.Close()

	out := buf.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("table output is empty for a summary — stats --format table would print nothing")
	}
	if !strings.Contains(out, "42") {
		t.Errorf("summary table missing the total:\n%s", out)
	}
}

func TestTableRendersCounts(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("table", &buf)
	r.Write(Count{Key: "linkedin.com", Count: 9})
	r.Close()

	out := buf.String()
	if !strings.Contains(out, "linkedin.com") || !strings.Contains(out, "9") {
		t.Errorf("count table missing data:\n%s", out)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	// Accented text must never be cut mid-character.
	got := truncate("perché è importante davvero", 10)
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if n := len([]rune(got)); n != 10 {
		t.Errorf("truncate returned %d runes, want 10", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output/ -v`
Expected: FAIL — `undefined: NewRenderer`

- [ ] **Step 3: Write the wire-format types**

Create `internal/output/types.go`:

```go
// Package output defines the tool's wire format and renderers.
//
// These structs are the public contract: an LLM or a script parses exactly
// these field names, so the JSON tags matter more than the Go names.
package output

// Address is one mail participant.
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Body is the decoded message text. It is always plain text, never markup.
type Body struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Source    string `json:"source"` // "text/plain" or "text/html"
}

// Flags are Mail's own per-message flags.
type Flags struct {
	Read          bool `json:"read"`
	Flagged       bool `json:"flagged"`
	HasAttachment bool `json:"has_attachment"`
}

// Signals are header-derived facts, never judgments.
type Signals struct {
	IsBulk         bool `json:"is_bulk"`
	IsAutomated    bool `json:"is_automated"`
	HasUnsubscribe bool `json:"has_unsubscribe"`
	ReplyToDiffers bool `json:"reply_to_differs"`
	SPFDKIMPresent bool `json:"spf_dkim_present"`
}

// Attachment describes an attached part (metadata only, no content).
type Attachment struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int    `json:"size"`
}

// Link is one extracted URL.
type Link struct {
	URLCanonical   string `json:"url_canonical"`
	URLOriginal    string `json:"url_original"`
	Domain         string `json:"domain"`
	AnchorText     string `json:"anchor_text"`
	Class          string `json:"class"` // content | tracking | action
	DedupKey       string `json:"dedup_key"`
	MessageID      int64  `json:"message_id"`
	AnchorMismatch bool   `json:"anchor_mismatch"`
}

// Message is one complete message record.
type Message struct {
	ID          int64        `json:"id"`
	MessageID   string       `json:"message_id"`
	ThreadID    int64        `json:"thread_id"`
	Mailbox     string       `json:"mailbox"`
	Account     string       `json:"account"`
	Date        string       `json:"date"` // RFC 3339
	From        Address      `json:"from"`
	To          []Address    `json:"to"`
	Subject     string       `json:"subject"`
	Flags       Flags        `json:"flags"`
	Body        Body         `json:"body"`
	Links       []Link       `json:"links"`
	Attachments []Attachment `json:"attachments"`
	Signals     Signals      `json:"signals"`
	// Error is set when this message could not be read. The scan continues;
	// one unreadable message never fails the run.
	Error string `json:"error,omitempty"`
}

// Count pairs a label with a frequency, used throughout the summary.
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Summary is the corpus rollup, emitted as the final line of a run.
type Summary struct {
	Type            string   `json:"type"` // always "summary"
	Total           int      `json:"total"`
	DateRange       []string `json:"date_range"`
	TopSenders      []Count  `json:"top_senders"`
	TopDomains      []Count  `json:"top_domains"`
	VolumeByWeek    []Count  `json:"volume_by_week"`
	Unread          int      `json:"unread"`
	Flagged         int      `json:"flagged"`
	WithAttachments int      `json:"with_attachments"`
	Threads         int      `json:"threads"`
	Skipped         int      `json:"skipped"`
}
```

- [ ] **Step 4: Write the renderers**

Create `internal/output/render.go`:

```go
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Renderer writes result records in a chosen format.
type Renderer interface {
	Write(v any) error
	Close() error
}

// NewRenderer returns a renderer for the named format.
//
// jsonl is the default because it streams: each record is written and
// flushed as it completes, so memory stays flat and the first result
// appears immediately. json buffers everything into a single array.
func NewRenderer(format string, w io.Writer) (Renderer, error) {
	switch strings.ToLower(format) {
	case "", "jsonl":
		return &jsonlRenderer{enc: json.NewEncoder(w)}, nil
	case "json":
		return &jsonRenderer{w: w, items: []any{}}, nil
	case "table":
		return newTableRenderer(w), nil
	case "text":
		return &textRenderer{w: w}, nil
	default:
		return nil, fmt.Errorf("unknown format %q (want jsonl, json, table, or text)", format)
	}
}

type jsonlRenderer struct{ enc *json.Encoder }

func (r *jsonlRenderer) Write(v any) error { return r.enc.Encode(tagSummary(v)) }
func (r *jsonlRenderer) Close() error      { return nil }

type jsonRenderer struct {
	w     io.Writer
	items []any
}

func (r *jsonRenderer) Write(v any) error {
	r.items = append(r.items, tagSummary(v))
	return nil
}

func (r *jsonRenderer) Close() error {
	enc := json.NewEncoder(r.w)
	enc.SetIndent("", "  ")
	return enc.Encode(r.items)
}

// tagSummary stamps the discriminator on summary records so a streaming
// consumer can recognize the final line without ambiguity. Both the value
// and pointer forms are handled, so a caller passing *Summary does not
// silently emit an untagged record.
func tagSummary(v any) any {
	switch s := v.(type) {
	case Summary:
		s.Type = "summary"
		return s
	case *Summary:
		if s == nil {
			return v
		}
		copied := *s
		copied.Type = "summary"
		return copied
	}
	return v
}

type tableRenderer struct {
	tw     *tabwriter.Writer
	header bool
}

func newTableRenderer(w io.Writer) *tableRenderer {
	return &tableRenderer{tw: tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)}
}

func (r *tableRenderer) Write(v any) error {
	switch rec := v.(type) {
	case Message:
		if !r.header {
			fmt.Fprintln(r.tw, "ID\tDATE\tFROM\tSUBJECT")
			r.header = true
		}
		_, err := fmt.Fprintf(r.tw, "%d\t%s\t%s\t%s\n",
			rec.ID, truncate(rec.Date, 19),
			truncate(rec.From.Address, 32), truncate(rec.Subject, 60))
		return err

	case Link:
		if !r.header {
			fmt.Fprintln(r.tw, "MSG\tCLASS\tDOMAIN\tURL")
			r.header = true
		}
		_, err := fmt.Fprintf(r.tw, "%d\t%s\t%s\t%s\n",
			rec.MessageID, rec.Class, rec.Domain, rec.URLCanonical)
		return err

	case Count:
		if !r.header {
			fmt.Fprintln(r.tw, "KEY\tCOUNT")
			r.header = true
		}
		_, err := fmt.Fprintf(r.tw, "%s\t%d\n", rec.Key, rec.Count)
		return err

	case Summary:
		// Rendered as key/value rows rather than dropped — otherwise
		// "mail stats --format table" prints nothing and exits 0.
		return writeSummaryText(r.tw, rec)
	}
	return nil
}

func (r *tableRenderer) Close() error { return r.tw.Flush() }

type textRenderer struct{ w io.Writer }

func (r *textRenderer) Write(v any) error {
	switch rec := v.(type) {
	case Message:
		_, err := fmt.Fprintf(r.w,
			"From: %s <%s>\nDate: %s\nSubject: %s\nMailbox: %s\n\n%s\n\n---\n\n",
			rec.From.Name, rec.From.Address, rec.Date, rec.Subject, rec.Mailbox, rec.Body.Text)
		return err
	case Link:
		_, err := fmt.Fprintf(r.w, "%s\t%s\n", rec.Class, rec.URLCanonical)
		return err
	case Count:
		_, err := fmt.Fprintf(r.w, "%s\t%d\n", rec.Key, rec.Count)
		return err
	case Summary:
		return writeSummaryText(r.w, rec)
	}
	return nil
}

func (r *textRenderer) Close() error { return nil }

// writeSummaryText renders a corpus summary for human formats.
func writeSummaryText(w io.Writer, s Summary) error {
	if _, err := fmt.Fprintf(w,
		"Total\t%d\nUnread\t%d\nFlagged\t%d\nWith attachments\t%d\nThreads\t%d\n",
		s.Total, s.Unread, s.Flagged, s.WithAttachments, s.Threads); err != nil {
		return err
	}
	if len(s.DateRange) == 2 {
		if _, err := fmt.Fprintf(w, "Date range\t%s to %s\n",
			s.DateRange[0], s.DateRange[1]); err != nil {
			return err
		}
	}
	for _, c := range s.TopSenders {
		if _, err := fmt.Fprintf(w, "Sender\t%s (%d)\n", c.Key, c.Count); err != nil {
			return err
		}
	}
	for _, c := range s.TopDomains {
		if _, err := fmt.Fprintf(w, "Domain\t%s (%d)\n", c.Key, c.Count); err != nil {
			return err
		}
	}
	if s.Skipped > 0 {
		if _, err := fmt.Fprintf(w, "Skipped\t%d\n", s.Skipped); err != nil {
			return err
		}
	}
	return nil
}

// truncate shortens a string to n runes. It counts runes, not bytes, so
// accented subjects are never cut mid-character.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/output/ -v`
Expected: PASS (all seven renderer tests)

- [ ] **Step 6: Commit**

```bash
git add internal/output/
git commit -m "feat: add wire-format types and JSONL/JSON/table/text renderers"
```

---

### Task 12: Parallel streaming scanner

**Files:**
- Create: `internal/scan/scan.go`
- Test: `internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `mailstore.Store`, `mailstore.PathIndex`, `mailstore.Filter`, `mailstore.MessageMeta` (Tasks 4-6), `emlx.ParseFile`, `emlx.Extract` (Tasks 7-8), `analyze.ExtractLinks`, `analyze.DeriveSignals` (Tasks 9-10), `output.Message` (Task 11)
- Produces:
  - `scan.New(store *mailstore.Store, paths *mailstore.PathIndex) *Scanner`
  - `(*Scanner) Run(ctx context.Context, opts Options, emit func(output.Message) error) (Stats, error)`
  - `(*Scanner) One(rowid int64, opts Options) (output.Message, bool, error)` — single-message lookup by ROWID, used by `show`
  - `type Options struct { Filter mailstore.Filter; BodyQuery string; MaxBodyChars int; Workers int }`
  - `type Stats struct { Emitted, Skipped, MissingFiles, ParseErrors int }`

**Context for the implementer:** This task joins everything. Three requirements that are easy to get wrong:

1. **Results must stay in the store's order** (newest first) even though workers finish out of order. Dispatch indexed jobs and reassemble by index — do not emit straight from the worker goroutines.
2. **One unreadable message must never fail the scan.** A missing `.emlx` or a parse error produces an `output.Message` with `Error` set, counted in `Stats`, and the run continues.
3. **`BodyQuery` filters after parsing** — it is the only filter that cannot be pushed into SQL, because Mail keeps no body index. A message whose body does not match is dropped, not emitted with an error.
4. **`Limit` must not reach SQL when a body query is active.** Pushing it down would fetch only the newest N messages and then filter *those* bodies, silently reporting "no matches" for anything older — which breaks body search on any mailbox larger than the limit. With a body query, `Run` strips the SQL limit and instead stops once N messages have actually matched.
5. **Never leak goroutines.** `Run` owns a derived cancellable context and drains `results` on every early return. Without this, `mail search | head -1` (a broken pipe on `emit`) leaves the workers, the dispatcher, and the closer blocked forever — harmless in a one-shot CLI, a per-request leak under the planned MCP server.
6. **Error kinds are carried, not string-matched.** `result` holds an `errKind` enum so `Stats` classification never depends on the wording of an error message.

- [ ] **Step 1: Write the failing test**

Create `internal/scan/scan_test.go`:

```go
package scan

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/harasuke/applemail/internal/mailstore"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/testdata"
)

func newFixtureScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())
	paths, err := mailstore.DiscoverPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := mailstore.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err := mailstore.NewPathIndex(paths)
	if err != nil {
		t.Fatal(err)
	}
	return New(store, idx), root
}

func collect(t *testing.T, s *Scanner, opts Options) ([]output.Message, Stats) {
	t.Helper()
	var got []output.Message
	stats, err := s.Run(context.Background(), opts, func(m output.Message) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return got, stats
}

func TestRunEmitsAllMessages(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, stats := collect(t, s, Options{})
	if len(got) != 5 {
		t.Fatalf("got %d messages, want 5", len(got))
	}
	if stats.Emitted != 5 {
		t.Errorf("Emitted = %d, want 5", stats.Emitted)
	}
	if stats.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", stats.Skipped)
	}
}

func TestRunPreservesStoreOrder(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{})
	// The store returns newest first; the fixture ROWIDs ascend with date.
	want := []int64{5, 4, 3, 2, 1}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d has ID %d, want %d (order not preserved)", i, got[i].ID, id)
		}
	}
}

func TestRunPopulatesBodyAndLinks(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{Filter: mailstore.Filter{Subject: "Plain text hello"}})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	m := got[0]
	if !strings.Contains(m.Body.Text, "Just saying hello") {
		t.Errorf("Body.Text = %q", m.Body.Text)
	}
	if len(m.Links) != 1 {
		t.Errorf("got %d links, want 1", len(m.Links))
	}
	if m.From.Address != "alice@example.com" {
		t.Errorf("From.Address = %q", m.From.Address)
	}
}

func TestRunBodyQueryFiltersAfterParsing(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{BodyQuery: "Just saying hello"})
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1 matching the body query", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("matched message ID = %d, want 1", got[0].ID)
	}
}

func TestRunBodyQueryIsCaseInsensitive(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{BodyQuery: "JUST SAYING HELLO"})
	if len(got) != 1 {
		t.Errorf("got %d messages, want 1 (query should be case-insensitive)", len(got))
	}
}

func TestRunMissingEmlxYieldsErrorRecordAndContinues(t *testing.T) {
	s, root := newFixtureScanner(t)
	// Delete one message body; the scan must still emit all five records.
	victim := testdata.EmlxPath(root, testdata.DefaultMessages()[0])
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	got, stats := collect(t, s, Options{})
	if len(got) != 5 {
		t.Fatalf("got %d messages, want 5 (a missing file must not drop a record)", len(got))
	}
	if stats.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", stats.MissingFiles)
	}

	var errored int
	for _, m := range got {
		if m.Error != "" {
			errored++
		}
	}
	if errored != 1 {
		t.Errorf("got %d error records, want 1", errored)
	}
}

func TestRunTruncatesBodyWhenAsked(t *testing.T) {
	s, _ := newFixtureScanner(t)
	got, _ := collect(t, s, Options{
		Filter:       mailstore.Filter{Subject: "Plain text hello"},
		MaxBodyChars: 10,
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if len([]rune(got[0].Body.Text)) > 10 {
		t.Errorf("Body.Text is %d runes, want at most 10", len([]rune(got[0].Body.Text)))
	}
	if !got[0].Body.Truncated {
		t.Error("Body.Truncated = false, want true")
	}
}

func TestRunBodyQueryLimitCountsMatchesNotCandidates(t *testing.T) {
	s, _ := newFixtureScanner(t)
	// Message 1 is the OLDEST of the five and the only body match. With a
	// limit of 2 the SQL would return messages 5 and 4 only — so if the
	// limit reached SQL, this search finds nothing.
	got, _ := collect(t, s, Options{
		BodyQuery: "Just saying hello",
		Filter:    mailstore.Filter{Limit: 2},
	})
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1 — the limit must count matches, not candidates", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("matched ID = %d, want 1 (the oldest message)", got[0].ID)
	}
}

func TestRunBodyQueryStopsAtMatchLimit(t *testing.T) {
	s, _ := newFixtureScanner(t)
	// Every fixture body contains a lowercase "e"; stop after two matches.
	got, _ := collect(t, s, Options{
		BodyQuery: "e",
		Filter:    mailstore.Filter{Limit: 2},
	})
	if len(got) != 2 {
		t.Errorf("got %d matches, want exactly the 2 requested", len(got))
	}
}

func TestRunDoesNotLeakGoroutinesWhenEmitFails(t *testing.T) {
	s, _ := newFixtureScanner(t)

	before := runtime.NumGoroutine()
	_, err := s.Run(context.Background(), Options{}, func(output.Message) error {
		return errors.New("consumer closed the pipe")
	})
	if err == nil {
		t.Fatal("Run returned nil error, want the emit error propagated")
	}

	// Workers, the dispatcher, and the closer must all have exited.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines leaked: %d before, %d after", before, runtime.NumGoroutine())
}

func TestRunRespectsContextCancellation(t *testing.T) {
	s, _ := newFixtureScanner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the run starts

	_, err := s.Run(ctx, Options{}, func(output.Message) error { return nil })
	if err == nil {
		t.Error("Run returned nil error, want a cancellation error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scan/ -v`
Expected: FAIL — `undefined: New`

- [ ] **Step 3: Write minimal implementation**

Create `internal/scan/scan.go`:

```go
// Package scan joins metadata queries to body parsing and streams results.
package scan

import (
	"context"
	"fmt"
	"net/mail"
	"runtime"
	"strings"
	"sync"

	"github.com/harasuke/applemail/internal/analyze"
	"github.com/harasuke/applemail/internal/emlx"
	"github.com/harasuke/applemail/internal/mailstore"
	"github.com/harasuke/applemail/internal/output"
)

// Options control one scan.
type Options struct {
	Filter mailstore.Filter
	// BodyQuery filters on message text. It is applied after parsing
	// because Mail keeps no body index to push it into.
	BodyQuery    string
	MaxBodyChars int // 0 means unlimited
	Workers      int // 0 means GOMAXPROCS
}

// Stats report what a scan did, including what it could not read.
type Stats struct {
	Emitted      int
	Skipped      int
	MissingFiles int
	ParseErrors  int
}

// Scanner reads metadata from the store and bodies from disk.
type Scanner struct {
	store *mailstore.Store
	paths *mailstore.PathIndex
}

// New builds a Scanner over an open store and a path index.
func New(store *mailstore.Store, paths *mailstore.PathIndex) *Scanner {
	return &Scanner{store: store, paths: paths}
}

// errKind classifies why a message could not be read, so Stats never
// depends on the wording of an error message.
type errKind int

const (
	errNone errKind = iota
	errMissingFile
	errParse
)

// result carries a worker's output back with its dispatch index, so
// output order matches the store's order regardless of completion order.
type result struct {
	index int
	msg   output.Message
	kind  errKind
	drop  bool // body query did not match
}

// Run queries metadata, parses bodies in parallel, and emits records in
// the store's order (newest first).
//
// A message that cannot be read yields a record with Error set and is
// counted in Stats — one unreadable message never fails the scan.
func (s *Scanner) Run(ctx context.Context, opts Options, emit func(output.Message) error) (Stats, error) {
	var stats Stats

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	// Own a cancellable context so an early return can stop the workers
	// rather than leaving them blocked on a send.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// A body query cannot be pushed into SQL, so neither can the limit:
	// LIMIT 50 would scan only the 50 newest candidates and report "no
	// matches" for everything older. Strip it here and count matches
	// during emission instead.
	query := opts.Filter
	matchLimit := 0
	if opts.BodyQuery != "" && query.Limit > 0 {
		matchLimit = query.Limit
		query.Limit = 0
	}

	metas, err := s.store.Query(query)
	if err != nil {
		return stats, err
	}
	if len(metas) == 0 {
		return stats, nil
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(metas) {
		workers = len(metas)
	}

	jobs := make(chan int)
	results := make(chan result, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				select {
				case <-ctx.Done():
					return
				case results <- s.buildOne(idx, metas[idx], opts):
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range metas {
			select {
			case <-ctx.Done():
				return
			case jobs <- i:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	// drain stops the workers and consumes anything still in flight, so no
	// goroutine is left blocked on a send when Run returns early.
	drain := func() {
		cancel()
		for range results {
		}
	}

	// Reassemble in dispatch order: hold out-of-order results until their
	// turn arrives, so emission matches the store's ordering.
	pending := make(map[int]result, workers*2)
	next := 0
	for r := range results {
		pending[r.index] = r
		for {
			ready, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++

			if ready.drop {
				continue
			}
			switch ready.kind {
			case errMissingFile:
				stats.Skipped++
				stats.MissingFiles++
			case errParse:
				stats.Skipped++
				stats.ParseErrors++
			}
			if err := emit(ready.msg); err != nil {
				drain()
				return stats, err
			}
			stats.Emitted++

			// With a body query the limit counts actual matches, so it can
			// only be applied here — after parsing decided the outcome.
			if matchLimit > 0 && stats.Emitted >= matchLimit {
				drain()
				return stats, nil
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

// One resolves a single message by ROWID through an indexed lookup.
//
// This exists so `show` never scans the mailbox to find one message: an
// agent calls show per message in a loop, and a full scan per call would
// cost thousands of file reads for a primary-key lookup.
func (s *Scanner) One(rowid int64, opts Options) (output.Message, bool, error) {
	f := opts.Filter
	f.ROWID = rowid
	f.IncludeDeleted = true // an id must still resolve after deletion
	f.Limit = 0

	metas, err := s.store.Query(f)
	if err != nil {
		return output.Message{}, false, err
	}
	if len(metas) == 0 {
		return output.Message{}, false, nil
	}
	return s.buildOne(0, metas[0], opts).msg, true, nil
}

// buildOne turns one metadata row into a full output record.
func (s *Scanner) buildOne(index int, meta mailstore.MessageMeta, opts Options) result {
	msg := output.Message{
		ID:        meta.ROWID,
		MessageID: meta.MessageID,
		ThreadID:  meta.ConversationID,
		Mailbox:   mailboxName(meta.MailboxURL),
		Account:   accountName(meta.MailboxURL),
		Date:      meta.DateReceived.Format("2006-01-02T15:04:05Z07:00"),
		From:      output.Address{Name: meta.SenderName, Address: meta.SenderAddr},
		Subject:   meta.Subject,
		Flags:     output.Flags{Read: meta.Read, Flagged: meta.Flagged},
		To:        []output.Address{},
		Links:     []output.Link{},
	}

	path, ok := s.paths.Resolve(meta.ROWID)
	if !ok {
		msg.Error = fmt.Sprintf("message body not found on disk (ROWID %d)", meta.ROWID)
		return result{index: index, msg: msg, kind: errMissingFile}
	}

	file, err := emlx.ParseFile(path)
	if err != nil {
		msg.Error = fmt.Sprintf("parse .emlx: %v", err)
		return result{index: index, msg: msg, kind: errParse}
	}

	decoded, err := emlx.Extract(file)
	if err != nil {
		msg.Error = fmt.Sprintf("extract message: %v", err)
		return result{index: index, msg: msg, kind: errParse}
	}

	if opts.BodyQuery != "" &&
		!strings.Contains(strings.ToLower(decoded.Text), strings.ToLower(opts.BodyQuery)) {
		return result{index: index, drop: true}
	}

	text := decoded.Text
	truncated := false
	if opts.MaxBodyChars > 0 {
		if runes := []rune(text); len(runes) > opts.MaxBodyChars {
			text = string(runes[:opts.MaxBodyChars])
			truncated = true
		}
	}
	msg.Body = output.Body{Text: text, Truncated: truncated, Source: decoded.TextSource}

	if decoded.Subject != "" {
		msg.Subject = decoded.Subject
	}
	msg.To = parseAddressList(decoded.To)

	for _, a := range decoded.Attachments {
		msg.Attachments = append(msg.Attachments,
			output.Attachment{Name: a.Name, MIME: a.MIME, Size: a.Size})
	}
	msg.Flags.HasAttachment = len(msg.Attachments) > 0

	for _, l := range analyze.ExtractLinks(decoded) {
		msg.Links = append(msg.Links, output.Link{
			URLCanonical:   l.URLCanonical,
			URLOriginal:    l.URLOriginal,
			Domain:         l.Domain,
			AnchorText:     l.AnchorText,
			Class:          string(l.Class),
			DedupKey:       l.DedupKey,
			MessageID:      meta.ROWID,
			AnchorMismatch: l.AnchorMismatch,
		})
	}

	sig := analyze.DeriveSignals(decoded)
	msg.Signals = output.Signals{
		IsBulk:         sig.IsBulk,
		IsAutomated:    sig.IsAutomated,
		HasUnsubscribe: sig.HasUnsubscribe,
		ReplyToDiffers: sig.ReplyToDiffers,
		SPFDKIMPresent: sig.SPFDKIMPresent,
	}

	return result{index: index, msg: msg}
}

func parseAddressList(header string) []output.Address {
	out := []output.Address{}
	if strings.TrimSpace(header) == "" {
		return out
	}
	addrs, err := mail.ParseAddressList(header)
	if err != nil {
		return []output.Address{{Address: strings.TrimSpace(header)}}
	}
	for _, a := range addrs {
		out = append(out, output.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

// mailboxName takes the last path element of a mailbox URL.
func mailboxName(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(rawURL, "/"), "/")
	return parts[len(parts)-1]
}

// accountName takes the user portion of a mailbox URL, when present.
func accountName(rawURL string) string {
	at := strings.Index(rawURL, "://")
	if at < 0 {
		return ""
	}
	rest := rawURL[at+3:]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	return rest
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scan/ -v`
Expected: PASS (all eight scanner tests)

- [ ] **Step 5: Verify no data race**

Run: `go test ./internal/scan/ -race -count=3`
Expected: PASS with no race warnings

- [ ] **Step 6: Commit**

```bash
git add internal/scan/
git commit -m "feat: add parallel scanner with order-preserving streaming emission"
```

---

### Task 13: Corpus summary accumulator

**Files:**
- Create: `internal/corpus/summary.go`
- Test: `internal/corpus/summary_test.go`

**Interfaces:**
- Consumes: `output.Message`, `output.Summary`, `output.Count` (Task 11)
- Produces: `corpus.NewAccumulator() *Accumulator`, `(*Accumulator) Add(m output.Message)`, `(*Accumulator) Summary(skipped int) output.Summary`

**Context for the implementer:** The accumulator must work in a streaming pass — `Add` is called once per emitted message, and nothing is retained beyond running counts. Never buffer the messages themselves; that would defeat the streaming design.

Ties in the top-N lists are broken by key so output is deterministic and testable.

- [ ] **Step 1: Write the failing test**

Create `internal/corpus/summary_test.go`:

```go
package corpus

import (
	"testing"

	"github.com/harasuke/applemail/internal/output"
)

func msg(id int64, sender, date string, read bool) output.Message {
	return output.Message{
		ID:       id,
		From:     output.Address{Address: sender},
		Date:     date,
		ThreadID: id,
		Flags:    output.Flags{Read: read},
	}
}

func TestSummaryCountsTotalAndUnread(t *testing.T) {
	a := NewAccumulator()
	a.Add(msg(1, "alice@example.com", "2026-08-01T10:00:00Z", true))
	a.Add(msg(2, "bob@example.com", "2026-08-02T10:00:00Z", false))
	a.Add(msg(3, "bob@example.com", "2026-08-03T10:00:00Z", false))

	s := a.Summary(0)
	if s.Total != 3 {
		t.Errorf("Total = %d, want 3", s.Total)
	}
	if s.Unread != 2 {
		t.Errorf("Unread = %d, want 2", s.Unread)
	}
}

func TestSummaryTopSendersOrderedByCount(t *testing.T) {
	a := NewAccumulator()
	a.Add(msg(1, "alice@example.com", "2026-08-01T10:00:00Z", true))
	a.Add(msg(2, "bob@example.com", "2026-08-02T10:00:00Z", true))
	a.Add(msg(3, "bob@example.com", "2026-08-03T10:00:00Z", true))

	s := a.Summary(0)
	if len(s.TopSenders) < 1 {
		t.Fatal("TopSenders is empty")
	}
	if s.TopSenders[0].Key != "bob@example.com" || s.TopSenders[0].Count != 2 {
		t.Errorf("TopSenders[0] = %+v, want bob@example.com with 2", s.TopSenders[0])
	}
}

func TestSummaryDateRange(t *testing.T) {
	a := NewAccumulator()
	a.Add(msg(1, "a@example.com", "2026-08-05T10:00:00Z", true))
	a.Add(msg(2, "a@example.com", "2026-08-01T10:00:00Z", true))
	a.Add(msg(3, "a@example.com", "2026-08-09T10:00:00Z", true))

	s := a.Summary(0)
	if len(s.DateRange) != 2 {
		t.Fatalf("DateRange = %v, want two entries", s.DateRange)
	}
	if s.DateRange[0] != "2026-08-01T10:00:00Z" {
		t.Errorf("DateRange[0] = %q, want the oldest date", s.DateRange[0])
	}
	if s.DateRange[1] != "2026-08-09T10:00:00Z" {
		t.Errorf("DateRange[1] = %q, want the newest date", s.DateRange[1])
	}
}

func TestSummaryCountsDomainsFromLinks(t *testing.T) {
	a := NewAccumulator()
	m := msg(1, "a@example.com", "2026-08-01T10:00:00Z", true)
	m.Links = []output.Link{
		{Domain: "linkedin.com", Class: "content"},
		{Domain: "linkedin.com", Class: "content"},
		{Domain: "example.org", Class: "content"},
	}
	a.Add(m)

	s := a.Summary(0)
	if len(s.TopDomains) < 1 {
		t.Fatal("TopDomains is empty")
	}
	if s.TopDomains[0].Key != "linkedin.com" || s.TopDomains[0].Count != 2 {
		t.Errorf("TopDomains[0] = %+v, want linkedin.com with 2", s.TopDomains[0])
	}
}

func TestSummaryCountsDistinctThreads(t *testing.T) {
	a := NewAccumulator()
	m1 := msg(1, "a@example.com", "2026-08-01T10:00:00Z", true)
	m1.ThreadID = 100
	m2 := msg(2, "a@example.com", "2026-08-02T10:00:00Z", true)
	m2.ThreadID = 100
	m3 := msg(3, "a@example.com", "2026-08-03T10:00:00Z", true)
	m3.ThreadID = 200
	a.Add(m1)
	a.Add(m2)
	a.Add(m3)

	if s := a.Summary(0); s.Threads != 2 {
		t.Errorf("Threads = %d, want 2", s.Threads)
	}
}

func TestSummaryVolumeByWeek(t *testing.T) {
	a := NewAccumulator()
	a.Add(msg(1, "a@example.com", "2026-08-03T10:00:00Z", true)) // 2026-W32
	a.Add(msg(2, "a@example.com", "2026-08-04T10:00:00Z", true)) // 2026-W32
	a.Add(msg(3, "a@example.com", "2026-08-12T10:00:00Z", true)) // 2026-W33

	s := a.Summary(0)
	if len(s.VolumeByWeek) != 2 {
		t.Fatalf("VolumeByWeek = %v, want two weeks", s.VolumeByWeek)
	}
	// Weeks are ordered chronologically.
	if s.VolumeByWeek[0].Key != "2026-W32" || s.VolumeByWeek[0].Count != 2 {
		t.Errorf("VolumeByWeek[0] = %+v, want 2026-W32 with 2", s.VolumeByWeek[0])
	}
}

func TestSummaryReportsSkipped(t *testing.T) {
	a := NewAccumulator()
	a.Add(msg(1, "a@example.com", "2026-08-01T10:00:00Z", true))
	if s := a.Summary(4); s.Skipped != 4 {
		t.Errorf("Skipped = %d, want 4", s.Skipped)
	}
}

func TestSummaryEmptyAccumulator(t *testing.T) {
	s := NewAccumulator().Summary(0)
	if s.Total != 0 {
		t.Errorf("Total = %d, want 0", s.Total)
	}
	if len(s.DateRange) != 0 {
		t.Errorf("DateRange = %v, want empty", s.DateRange)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/corpus/ -v`
Expected: FAIL — `undefined: NewAccumulator`

- [ ] **Step 3: Write minimal implementation**

Create `internal/corpus/summary.go`:

```go
// Package corpus accumulates aggregate statistics over a result set.
package corpus

import (
	"fmt"
	"sort"
	"time"

	"github.com/harasuke/applemail/internal/output"
)

const topN = 10

// Accumulator builds a corpus summary in a single streaming pass.
//
// Only running counts are retained — never the messages themselves, which
// would defeat the streaming design.
type Accumulator struct {
	total           int
	unread          int
	flagged         int
	withAttachments int

	senders map[string]int
	domains map[string]int
	weeks   map[string]int
	threads map[int64]bool

	oldest string
	newest string
}

// NewAccumulator returns an empty accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{
		senders: map[string]int{},
		domains: map[string]int{},
		weeks:   map[string]int{},
		threads: map[int64]bool{},
	}
}

// Add folds one message into the running totals.
func (a *Accumulator) Add(m output.Message) {
	a.total++
	if !m.Flags.Read {
		a.unread++
	}
	if m.Flags.Flagged {
		a.flagged++
	}
	if len(m.Attachments) > 0 {
		a.withAttachments++
	}
	if m.From.Address != "" {
		a.senders[m.From.Address]++
	}
	if m.ThreadID != 0 {
		a.threads[m.ThreadID] = true
	}
	for _, l := range m.Links {
		if l.Domain != "" {
			a.domains[l.Domain]++
		}
	}
	if m.Date != "" {
		// String comparison is valid only because every Date is formatted
		// as fixed-width UTC RFC 3339 by the scanner. If dates ever carry
		// a non-Z offset, compare parsed times instead.
		if a.oldest == "" || m.Date < a.oldest {
			a.oldest = m.Date
		}
		if a.newest == "" || m.Date > a.newest {
			a.newest = m.Date
		}
		if wk := isoWeek(m.Date); wk != "" {
			a.weeks[wk]++
		}
	}
}

// Summary renders the accumulated totals. skipped comes from the scanner.
func (a *Accumulator) Summary(skipped int) output.Summary {
	s := output.Summary{
		Type:            "summary",
		Total:           a.total,
		Unread:          a.unread,
		Flagged:         a.flagged,
		WithAttachments: a.withAttachments,
		Threads:         len(a.threads),
		Skipped:         skipped,
		TopSenders:      topCounts(a.senders, topN),
		TopDomains:      topCounts(a.domains, topN),
		VolumeByWeek:    chronological(a.weeks),
		DateRange:       []string{},
	}
	if a.oldest != "" {
		s.DateRange = []string{a.oldest, a.newest}
	}
	return s
}

// topCounts returns the n most frequent keys, ties broken by key so the
// output is deterministic.
func topCounts(m map[string]int, n int) []output.Count {
	out := make([]output.Count, 0, len(m))
	for k, v := range m {
		out = append(out, output.Count{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// chronological returns week buckets in ascending order.
func chronological(m map[string]int) []output.Count {
	out := make([]output.Count, 0, len(m))
	for k, v := range m {
		out = append(out, output.Count{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// isoWeek renders an RFC 3339 date as an ISO year-week bucket.
func isoWeek(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/corpus/ -v`
Expected: PASS (all eight summary tests)

- [ ] **Step 5: Commit**

```bash
git add internal/corpus/
git commit -m "feat: accumulate corpus summary in a streaming pass"
```

---

### Task 14: CLI skeleton, exit codes, and permission messaging

**Files:**
- Create: `cmd/mail/main.go`
- Create: `cmd/mail/root.go`
- Create: `cmd/mail/context.go`
- Test: `cmd/mail/context_test.go`

**Interfaces:**
- Consumes: `mailstore.DiscoverPaths`, sentinel errors (Task 2), `mailstore.Open` (Task 5), `mailstore.NewPathIndex` (Task 6), `output.NewRenderer` (Task 11)
- Produces:
  - `rootCmd` with persistent flags `--format`, `--mail-dir`, `--limit`, and the shared filter flags
  - `openMail() (*mailstore.Store, *mailstore.PathIndex, error)`
  - `permissionMessage(root string) string`
  - `exitCodeFor(err error) int`
  - `buildFilter() (mailstore.Filter, error)`

**Context for the implementer:** The permission message is the single most important piece of user-facing text in this tool. On the target machine `~/Library/Mail` currently returns `Operation not permitted`, so this message is the first thing the user will see.

It must say three things: that **the terminal application** needs the grant (not the `mail` binary — macOS TCC evaluates the responsible process, and on macOS 26.1+ granting a bare CLI binary reportedly does not work), the exact System Settings path, and that the terminal must be restarted afterward.

Add Cobra first: `go get github.com/spf13/cobra`.

- [ ] **Step 1: Write the failing test**

Create `cmd/mail/context_test.go`:

```go
package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/mailstore"
)

func TestExitCodeForPermissionError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", mailstore.ErrNoPermission)
	if got := exitCodeFor(err); got != 2 {
		t.Errorf("exitCodeFor(ErrNoPermission) = %d, want 2", got)
	}
}

func TestExitCodeForGeneralError(t *testing.T) {
	if got := exitCodeFor(errors.New("something broke")); got != 1 {
		t.Errorf("exitCodeFor(generic) = %d, want 1", got)
	}
}

func TestExitCodeForNil(t *testing.T) {
	if got := exitCodeFor(nil); got != 0 {
		t.Errorf("exitCodeFor(nil) = %d, want 0", got)
	}
}

func TestPermissionMessageNamesTerminalNotBinary(t *testing.T) {
	msg := permissionMessage("/Users/someone/Library/Mail")

	if !strings.Contains(msg, "Full Disk Access") {
		t.Error("message does not mention Full Disk Access")
	}
	if !strings.Contains(strings.ToLower(msg), "terminal") {
		t.Error("message does not tell the user to grant access to their terminal")
	}
	if !strings.Contains(msg, "Privacy & Security") {
		t.Error("message does not give the System Settings path")
	}
	if !strings.Contains(strings.ToLower(msg), "restart") {
		t.Error("message does not say the terminal must be restarted")
	}
}

func TestParseSinceRelativeDays(t *testing.T) {
	got, err := parseSince("30d")
	if err != nil {
		t.Fatalf("parseSince(30d): %v", err)
	}
	if got.IsZero() {
		t.Error("parseSince(30d) returned the zero time")
	}
}

func TestParseSinceAbsoluteDate(t *testing.T) {
	got, err := parseSince("2026-08-01")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 8 || got.Day() != 1 {
		t.Errorf("parseSince(2026-08-01) = %v, want that date", got)
	}
}

func TestParseSinceRejectsGarbage(t *testing.T) {
	if _, err := parseSince("last tuesday"); err == nil {
		t.Error("parseSince(\"last tuesday\") = nil error, want a rejection")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/ -v`
Expected: FAIL — `undefined: exitCodeFor`

- [ ] **Step 3: Add Cobra**

```bash
go get github.com/spf13/cobra
```

- [ ] **Step 4: Write the shared command context**

Create `cmd/mail/context.go`:

```go
package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/harasuke/applemail/internal/mailstore"
)

// Exit codes. A distinct code for permissions lets a script tell
// "no results" from "no access" without parsing text.
const (
	exitOK         = 0
	exitError      = 1
	exitPermission = 2
)

func exitCodeFor(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, mailstore.ErrNoPermission):
		return exitPermission
	default:
		return exitError
	}
}

// permissionMessage explains how to grant Full Disk Access.
//
// macOS TCC evaluates the *responsible process*, which for a CLI is the
// terminal application, not this binary. On macOS 26.1+ adding a bare
// (non-.app) binary to the Full Disk Access list is reported not to work,
// so the terminal is the supported path.
func permissionMessage(root string) string {
	return fmt.Sprintf(`Cannot read %s — macOS denied access.

Apple Mail's data is protected by macOS privacy controls. Grant Full Disk
Access to your TERMINAL application (Terminal, iTerm, or whichever you are
using) — not to the "mail" binary itself, which macOS does not accept.

  1. Open System Settings → Privacy & Security → Full Disk Access
  2. Enable your terminal application (add it with + if it is not listed)
  3. Quit and restart the terminal completely
  4. Run "mail doctor" to confirm

Nothing is written to your mail: this tool opens the database read-only.`, root)
}

// openMail resolves paths and opens the store and the .emlx path index.
func openMail() (*mailstore.Store, *mailstore.PathIndex, error) {
	paths, err := mailstore.DiscoverPaths(flagMailDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := mailstore.Open(paths)
	if err != nil {
		return nil, nil, err
	}
	idx, err := mailstore.NewPathIndex(paths)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return store, idx, nil
}

var relativeDateRe = regexp.MustCompile(`^(\d+)([dwmy])$`)

// parseSince accepts either a relative span (30d, 2w, 6m, 1y) or an
// absolute YYYY-MM-DD date.
func parseSince(s string) (time.Time, error) {
	if m := relativeDateRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration %q", s)
		}
		now := time.Now()
		switch m[2] {
		case "d":
			return now.AddDate(0, 0, -n), nil
		case "w":
			return now.AddDate(0, 0, -n*7), nil
		case "m":
			return now.AddDate(0, -n, 0), nil
		case "y":
			return now.AddDate(-n, 0, 0), nil
		}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid date %q (want YYYY-MM-DD or a span like 30d, 2w, 6m, 1y)", s)
	}
	return t, nil
}

// buildFilter assembles the shared filter from the persistent flags.
func buildFilter() (mailstore.Filter, error) {
	f := mailstore.Filter{
		From:          flagFrom,
		To:            flagTo,
		Subject:       flagSubject,
		Mailbox:       flagMailbox,
		Unread:        flagUnread,
		Flagged:       flagFlagged,
		HasAttachment: flagHasAttachment,
		Limit:         flagLimit,
	}
	if flagSince != "" {
		t, err := parseSince(flagSince)
		if err != nil {
			return f, err
		}
		f.Since = &t
	}
	if flagUntil != "" {
		t, err := parseSince(flagUntil)
		if err != nil {
			return f, err
		}
		f.Until = &t
	}
	return f, nil
}
```

- [ ] **Step 5: Write the root command**

Create `cmd/mail/root.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

var (
	flagFormat        string
	flagMailDir       string
	flagLimit         int
	flagFrom          string
	flagTo            string
	flagSubject       string
	flagMailbox       string
	flagSince         string
	flagUntil         string
	flagUnread        bool
	flagFlagged       bool
	flagHasAttachment bool
	flagMaxBodyChars  int

	// Declared here rather than beside their commands so tests can reset
	// every flag from one place — Cobra binds these to package globals
	// that persist across Execute calls within a test binary.
	flagStats    bool
	flagRaw      bool
	flagAllLinks bool
	flagGroupBy  string
	flagOut      string
	flagExportAs string
)

var rootCmd = &cobra.Command{
	Use:   "mail",
	Short: "Read and search Apple Mail",
	Long: `Read, search, and analyze Apple Mail messages.

Reads Mail's Envelope Index and .emlx files directly, read-only. Output is
JSONL by default so it streams and pipes cleanly.

Requires Full Disk Access for your terminal application. Run "mail doctor"
to check.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// addFilterFlags attaches the shared filter flags to a command. Every
// command that selects messages uses the same set, with the same meaning.
//
// defaultLimit differs by command on purpose: search is interactive and
// caps at 50, while links, export, and stats default to 0 (unlimited)
// because a silent cap would corrupt an archive or an aggregate while
// still looking like it succeeded.
func addFilterFlags(cmd *cobra.Command, defaultLimit int) {
	cmd.Flags().StringVar(&flagFrom, "from", "", "filter by sender address or display name")
	cmd.Flags().StringVar(&flagTo, "to", "", "filter by recipient address")
	cmd.Flags().StringVar(&flagSubject, "subject", "", "filter by subject substring")
	cmd.Flags().StringVar(&flagMailbox, "mailbox", "", "filter by mailbox name or URL")
	cmd.Flags().StringVar(&flagSince, "since", "", "only messages on or after this date (YYYY-MM-DD or 30d, 2w, 6m, 1y)")
	cmd.Flags().StringVar(&flagUntil, "until", "", "only messages on or before this date")
	cmd.Flags().BoolVar(&flagUnread, "unread", false, "only unread messages")
	cmd.Flags().BoolVar(&flagFlagged, "flagged", false, "only flagged messages")
	cmd.Flags().BoolVar(&flagHasAttachment, "has-attachment", false, "only messages with attachments")
	cmd.Flags().IntVar(&flagLimit, "limit", defaultLimit,
		"maximum messages to return (0 for no limit)")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFormat, "format", "jsonl",
		"output format: jsonl, json, table, or text")
	rootCmd.PersistentFlags().StringVar(&flagMailDir, "mail-dir", "",
		"override the Mail directory (default ~/Library/Mail)")
}
```

- [ ] **Step 6: Write the entry point**

Create `cmd/mail/main.go`:

```go
// Command mail reads and searches Apple Mail.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/harasuke/applemail/internal/mailstore"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		// A permission failure gets the full explanation; everything else
		// gets the plain error. Both go to stderr so stdout stays pure JSONL.
		if errors.Is(err, mailstore.ErrNoPermission) {
			root, _ := mailstore.DefaultRoot()
			if flagMailDir != "" {
				root = flagMailDir
			}
			fmt.Fprintln(os.Stderr, permissionMessage(root))
		} else {
			fmt.Fprintf(os.Stderr, "mail: %v\n", err)
		}
		os.Exit(exitCodeFor(err))
	}
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./cmd/mail/ -v`
Expected: PASS (all seven context tests)

- [ ] **Step 8: Verify it builds and shows help**

Run:
```bash
go build -o mail ./cmd/mail && ./mail --help
```
Expected: help text listing `--format` and `--mail-dir`

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd/mail/
git commit -m "feat: add CLI skeleton with exit codes and permission guidance"
```

---

### Task 15: doctor and search commands

**Files:**
- Create: `cmd/mail/doctor.go`
- Create: `cmd/mail/search.go`
- Test: `cmd/mail/commands_test.go`

**Interfaces:**
- Consumes: everything from Tasks 12-14
- Produces: `doctorCmd`, `searchCmd`, and `runScan(cmd *cobra.Command, bodyQuery string, withStats bool) error`

**Context for the implementer:** `runScan` is shared by `search`, `links`, `export`, and `stats` — write it once here. It opens the store, builds the filter, runs the scanner, renders each message, and writes the skip count to stderr.

`doctor` must work even when everything else fails: it reports the permission problem rather than propagating it as a bare error.

- [ ] **Step 1: Write the failing test**

Create `cmd/mail/commands_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

// runCommand executes the CLI against a fixture Mail directory and
// returns stdout.
func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))

	t.Cleanup(resetFlags)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return stdout.String()
}

// resetFlags restores flag defaults between tests, since Cobra binds them
// to package-level variables that persist across Execute calls.
//
// This covers flags introduced in later tasks too. A flag left set by one
// test silently changes what the next test exercises — for example a
// leaked --raw makes a "message not found" test pass through the wrong
// code path.
func resetFlags() {
	flagFormat = "jsonl"
	flagMailDir = ""
	flagLimit = 50
	flagFrom, flagTo, flagSubject, flagMailbox = "", "", "", ""
	flagSince, flagUntil = "", ""
	flagUnread, flagFlagged, flagHasAttachment = false, false, false
	flagMaxBodyChars = 0
	flagStats = false
	flagRaw = false
	flagAllLinks = false
	flagGroupBy = ""
	flagOut = ""
	flagExportAs = "eml"
}

func TestDoctorReportsCounts(t *testing.T) {
	out := runCommand(t, "doctor")
	if !strings.Contains(out, "5") {
		t.Errorf("doctor output does not report the message count:\n%s", out)
	}
	if !strings.Contains(out, "V12") {
		t.Errorf("doctor output does not report the version directory:\n%s", out)
	}
}

func TestSearchEmitsJSONLPerMessage(t *testing.T) {
	out := runCommand(t, "search")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestSearchAppliesSubjectFilter(t *testing.T) {
	out := runCommand(t, "search", "--subject", "Plain text hello")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), out)
	}
}

func TestSearchBodyQueryMatchesContent(t *testing.T) {
	out := runCommand(t, "search", "Just saying hello")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), out)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	if m["subject"] != "Plain text hello" {
		t.Errorf("subject = %v, want the matching message", m["subject"])
	}
}

func TestSearchLimitCapsResults(t *testing.T) {
	out := runCommand(t, "search", "--limit", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}
}

func TestSearchTableFormat(t *testing.T) {
	out := runCommand(t, "search", "--format", "table", "--limit", "1")
	if !strings.Contains(out, "SUBJECT") {
		t.Errorf("table output has no header:\n%s", out)
	}
}

func TestSearchNoMatchesEmitsNothing(t *testing.T) {
	out := runCommand(t, "search", "--subject", "no-such-subject-anywhere")
	if strings.TrimSpace(out) != "" {
		t.Errorf("output = %q, want empty for no matches", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/ -run 'TestDoctor|TestSearch' -v`
Expected: FAIL — unknown command "doctor"

- [ ] **Step 3: Write the doctor command**

Create `cmd/mail/doctor.go`:

```go
package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/mailstore"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that Mail's data is reachable",
	Long: `Report whether this tool can read Apple Mail, and what it found.

Run this first. If Full Disk Access is missing, this command explains how
to grant it instead of failing with a database error.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()

		paths, err := mailstore.DiscoverPaths(flagMailDir)
		if err != nil {
			// A permission failure is explained once, by main. Returning
			// it here without printing avoids the message appearing twice.
			// Other failures get a diagnosis main would not provide.
			errOut := cmd.ErrOrStderr()
			switch {
			case errors.Is(err, mailstore.ErrNoMailDir):
				fmt.Fprintln(errOut, "No Mail directory found. Is Apple Mail set up on this machine?")
			case errors.Is(err, mailstore.ErrNoVersionDir):
				fmt.Fprintln(errOut, "Mail directory found, but no version directory contains an Envelope Index.")
				fmt.Fprintln(errOut, "This macOS layout is unexpected — please report it.")
			}
			return err
		}

		fmt.Fprintf(out, "Mail directory:     %s\n", paths.Root)
		fmt.Fprintf(out, "Version directory:  %s (V%d)\n", paths.VersionDir, paths.Version)
		fmt.Fprintf(out, "Envelope Index:     %s\n", paths.IndexPath)

		store, err := mailstore.Open(paths)
		if err != nil {
			return err
		}
		defer store.Close()

		messages, mailboxes, err := store.Counts()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Messages:           %d\n", messages)
		fmt.Fprintf(out, "Mailboxes:          %d\n", mailboxes)

		idx, err := mailstore.NewPathIndex(paths)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Message directories: %d\n", idx.DirCount())
		fmt.Fprintln(out, "\nAll checks passed. Mail data is readable.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
```

- [ ] **Step 4: Write the search command and the shared scan runner**

Create `cmd/mail/search.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/corpus"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search messages by metadata and body text",
	Long: `Search Apple Mail.

Metadata filters resolve against Mail's index and are instant. A bare
[query] argument searches message bodies, which reads .emlx files from
disk — combine it with filters to narrow the scan.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = args[0]
		}
		return runScan(cmd, query, flagStats)
	},
}

// runScan is the shared pipeline behind search, links, export, and stats:
// open the store, build the filter, scan, render, report skips.
func runScan(cmd *cobra.Command, bodyQuery string, withStats bool) error {
	filter, err := buildFilter()
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

	acc := corpus.NewAccumulator()
	scanner := scan.New(store, paths)

	stats, err := scanner.Run(cmd.Context(), scan.Options{
		Filter:       filter,
		BodyQuery:    bodyQuery,
		MaxBodyChars: flagMaxBodyChars,
	}, func(m output.Message) error {
		if withStats {
			acc.Add(m)
			return nil // stats mode emits only the summary
		}
		acc.Add(m)
		return renderer.Write(m)
	})
	if err != nil {
		return err
	}

	if withStats {
		if err := renderer.Write(acc.Summary(stats.Skipped)); err != nil {
			return err
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
	return nil
}

func init() {
	addFilterFlags(searchCmd, 50)
	searchCmd.Flags().IntVar(&flagMaxBodyChars, "max-body-chars", 0,
		"truncate message bodies to this many characters (0 for no limit)")
	searchCmd.Flags().BoolVar(&flagStats, "stats", false,
		"emit only the corpus summary instead of individual messages")
	rootCmd.AddCommand(searchCmd)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/mail/ -v`
Expected: PASS (all doctor and search tests)

- [ ] **Step 6: Commit**

```bash
git add cmd/mail/doctor.go cmd/mail/search.go cmd/mail/commands_test.go
git commit -m "feat: add doctor and search commands"
```

---

### Task 16: show and links commands

**Files:**
- Create: `cmd/mail/show.go`
- Create: `cmd/mail/links.go`
- Test: `cmd/mail/showlinks_test.go`

**Interfaces:**
- Consumes: `runScan` (Task 15), `scan.Scanner`, `emlx.ParseFile`, `mailstore` types
- Produces: `showCmd`, `linksCmd`

**Context for the implementer:** `show` takes one ROWID and always reads that message regardless of filters. `--raw` writes the untouched `.emlx` bytes, which is the escape hatch when the parser gets something wrong.

`links` reuses the scan pipeline but emits link records instead of message records. Default output is `content` links only — that is what an LLM should fetch. `--all` includes tracking and action links. `--group-by domain` rolls up into counts.

- [ ] **Step 1: Write the failing test**

Create `cmd/mail/showlinks_test.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShowEmitsOneMessage(t *testing.T) {
	out := runCommand(t, "show", "1")
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("show output is not valid JSON: %v\n%s", err, out)
	}
	if int64(m["id"].(float64)) != 1 {
		t.Errorf("id = %v, want 1", m["id"])
	}
	body := m["body"].(map[string]any)
	if !strings.Contains(body["text"].(string), "Just saying hello") {
		t.Errorf("body.text = %v", body["text"])
	}
}

func TestShowRawEmitsOriginalEmlx(t *testing.T) {
	out := runCommand(t, "show", "1", "--raw")
	if !strings.Contains(out, "Subject: Plain text hello") {
		t.Errorf("raw output does not contain the original headers:\n%s", out)
	}
	if !strings.Contains(out, "<?xml") {
		t.Errorf("raw output does not contain the plist trailer:\n%s", out)
	}
}

func TestShowUnknownIDFails(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "show", "99999")
	if err == nil {
		t.Error("show 99999 returned nil error, want a not-found error")
	}
}

func TestLinksEmitsContentLinksOnly(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com")
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("links emitted nothing")
	}
	for _, line := range lines {
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if l["class"] != "content" {
			t.Errorf("class = %v, want only content links by default", l["class"])
		}
	}
}

func TestLinksDedupsLinkedInJobs(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com")
	lines := nonEmptyLines(out)
	// Two distinct jobs despite three job anchors in the fixture.
	if len(lines) != 2 {
		t.Fatalf("got %d content links, want 2 after dedup:\n%s", len(lines), out)
	}
}

func TestLinksAllIncludesTracking(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com", "--all")

	var sawTracking bool
	for _, line := range nonEmptyLines(out) {
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		if l["class"] == "tracking" {
			sawTracking = true
		}
	}
	if !sawTracking {
		t.Errorf("--all did not include tracking links:\n%s", out)
	}
}

func TestLinksGroupByDomain(t *testing.T) {
	out := runCommand(t, "links", "--group-by", "domain")
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("group-by domain emitted nothing")
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &c); err != nil {
		t.Fatal(err)
	}
	if _, ok := c["key"]; !ok {
		t.Errorf("grouped output has no \"key\" field: %s", lines[0])
	}
	if _, ok := c["count"]; !ok {
		t.Errorf("grouped output has no \"count\" field: %s", lines[0])
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
```

Add these two helpers to `cmd/mail/commands_test.go`:

```go
// setupFixtureRoot builds a fixture Mail directory and returns its root.
func setupFixtureRoot(t *testing.T) string {
	t.Helper()
	return testdata.BuildMailDir(t, testdata.DefaultMessages())
}

// executeExpectingError runs the CLI and returns the error rather than
// failing the test, for cases where a failure is the expected outcome.
func executeExpectingError(t *testing.T, root string, args ...string) error {
	t.Helper()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))
	t.Cleanup(resetFlags)
	return rootCmd.Execute()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/ -run 'TestShow|TestLinks' -v`
Expected: FAIL — unknown command "show"

- [ ] **Step 3: Write the show command**

Create `cmd/mail/show.go`:

```go
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show one message in full",
	Long: `Print one message: headers, decoded text, links, and attachments.

The body and the link list are separate blocks, so a message can be read
independently of the links it contains.

--raw writes the original .emlx bytes untouched.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid message id %q: %w", args[0], err)
		}

		store, paths, err := openMail()
		if err != nil {
			return err
		}
		defer store.Close()

		if flagRaw {
			path, ok := paths.Resolve(id)
			if !ok {
				return fmt.Errorf("message %d has no .emlx file on disk", id)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		}

		renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
		if err != nil {
			return err
		}

		// One is an indexed lookup, not a scan: show is called per message
		// in a loop by agents, so it must not read the whole mailbox.
		scanner := scan.New(store, paths)
		msg, found, err := scanner.One(id, scan.Options{MaxBodyChars: flagMaxBodyChars})
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("message %d not found", id)
		}
		if err := renderer.Write(msg); err != nil {
			return err
		}
		return renderer.Close()
	},
}

func init() {
	showCmd.Flags().BoolVar(&flagRaw, "raw", false, "write the original .emlx bytes")
	showCmd.Flags().IntVar(&flagMaxBodyChars, "max-body-chars", 0,
		"truncate the message body to this many characters (0 for no limit)")
	rootCmd.AddCommand(showCmd)
}
```

- [ ] **Step 4: Write the links command**

Create `cmd/mail/links.go`:

```go
package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/analyze"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var linksCmd = &cobra.Command{
	Use:   "links",
	Short: "Extract links from messages",
	Long: `Extract every URL from matching messages, normalized and deduplicated.

Tracking parameters are stripped and redirect wrappers are unwrapped
offline, so the emitted URL is the one worth opening. Recognized patterns
(LinkedIn job postings) collapse to a stable dedup key, so the same target
arriving in several emails appears once.

By default only "content" links are emitted — the ones that resolve to a
real page. Use --all to also see tracking beacons and action links
(unsubscribe, confirmation) which should never be opened automatically.

This command makes no network requests.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, err := buildFilter()
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

		domains := map[string]int{}
		scanner := scan.New(store, paths)

		stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
			func(m output.Message) error {
				for _, l := range m.Links {
					if !flagAllLinks && l.Class != string(analyze.ClassContent) {
						continue
					}
					if flagGroupBy == "domain" {
						domains[l.Domain]++
						continue
					}
					if err := renderer.Write(l); err != nil {
						return err
					}
				}
				return nil
			})
		if err != nil {
			return err
		}

		if flagGroupBy == "domain" {
			counts := make([]output.Count, 0, len(domains))
			for k, v := range domains {
				counts = append(counts, output.Count{Key: k, Count: v})
			}
			sort.Slice(counts, func(i, j int) bool {
				if counts[i].Count != counts[j].Count {
					return counts[i].Count > counts[j].Count
				}
				return counts[i].Key < counts[j].Key
			})
			for _, c := range counts {
				if err := renderer.Write(c); err != nil {
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
		return nil
	},
}

func init() {
	addFilterFlags(linksCmd, 0)
	linksCmd.Flags().BoolVar(&flagAllLinks, "all", false,
		"include tracking and action links, not just content links")
	linksCmd.Flags().StringVar(&flagGroupBy, "group-by", "",
		"roll up results: \"domain\"")
	rootCmd.AddCommand(linksCmd)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/mail/ -v`
Expected: PASS (all show and links tests plus earlier ones)

- [ ] **Step 6: Commit**

```bash
git add cmd/mail/show.go cmd/mail/links.go cmd/mail/showlinks_test.go cmd/mail/commands_test.go
git commit -m "feat: add show and links commands"
```

---

### Task 17: export and stats commands

**Files:**
- Create: `cmd/mail/export.go`
- Create: `cmd/mail/stats.go`
- Test: `cmd/mail/exportstats_test.go`

**Interfaces:**
- Consumes: `runScan` (Task 15), `scan.Scanner`, `corpus.Accumulator`
- Produces: `exportCmd`, `statsCmd`

**Context for the implementer:** `export` is the only command that writes files, and only into the directory the user names. It must refuse to write anywhere under `~/Library/Mail` — that guard is the difference between a read-only tool and one that can damage a mailbox.

Three formats via `--as`:

- `eml` (default) — one file per message
- `json` — one file per message, the full analyzed record
- `mbox` — a single `archive.mbox`, the interchange format Apple Mail and Thunderbird import

**mbox needs the mboxrd variant, not naive concatenation.** In mbox, any line beginning `From ` starts a new message, so a body line beginning `From ` would split one message into two on import. mboxrd escapes it by prefixing `>`, and escapes already-escaped forms (`>From ` → `>>From `) so the transformation stays reversible. Getting this wrong silently corrupts the archive at import time, which is exactly when it is too late to notice.

`stats` is `search --stats`: it runs the same pipeline but emits only the summary line.

- [ ] **Step 1: Write the failing test**

Create `cmd/mail/exportstats_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExportWritesEmlFiles(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest)

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("wrote %d files, want 5", len(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".eml") {
			t.Errorf("wrote %q, want a .eml file", e.Name())
		}
	}
}

func TestExportEmlContainsOriginalHeaders(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--subject", "Plain text hello")

	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dest, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Subject: Plain text hello") {
		t.Error("exported .eml does not contain the original headers")
	}
	if strings.Contains(string(raw), "<?xml") {
		t.Error("exported .eml still contains the .emlx plist trailer")
	}
}

func TestExportJSONFormat(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--as", "json", "--limit", "1")

	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(dest, entries[0].Name()))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("exported file is not valid JSON: %v", err)
	}
}

func TestExportMboxWritesSingleArchive(t *testing.T) {
	dest := t.TempDir()
	runCommand(t, "export", "--out", dest, "--as", "mbox")

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "archive.mbox" {
		t.Fatalf("wrote %v, want a single archive.mbox", entries)
	}

	raw, err := os.ReadFile(filepath.Join(dest, "archive.mbox"))
	if err != nil {
		t.Fatal(err)
	}
	// One "From " separator line per message, at the start of a line.
	var separators int
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "From ") {
			separators++
		}
	}
	if separators != 5 {
		t.Errorf("got %d separator lines, want 5", separators)
	}
	if !strings.Contains(string(raw), "Subject: Plain text hello") {
		t.Error("archive does not contain the message headers")
	}
}

func TestIsFromLineEscapesMboxrdForms(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"From someone@example.com Mon Aug  5 20:11:22 2026", true},
		{">From already escaped", true},
		{">>From twice escaped", true},
		{"From: Alice <alice@example.com>", false}, // a header, not a separator
		{"Fromage is not a separator", false},
		{"  From indented", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isFromLine(tt.line); got != tt.want {
			t.Errorf("isFromLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestExportDefaultsToUnlimited(t *testing.T) {
	// export must not inherit search's limit of 50: a silent cap would
	// produce a partial archive that still looks successful.
	if exportCmd.Flags().Lookup("limit").DefValue != "0" {
		t.Errorf("export --limit default = %q, want 0 (unlimited)",
			exportCmd.Flags().Lookup("limit").DefValue)
	}
	for _, c := range []struct {
		name string
		cmd  *cobra.Command
	}{{"stats", statsCmd}, {"links", linksCmd}} {
		if c.cmd.Flags().Lookup("limit").DefValue != "0" {
			t.Errorf("%s --limit default = %q, want 0 (unlimited)",
				c.name, c.cmd.Flags().Lookup("limit").DefValue)
		}
	}
	if searchCmd.Flags().Lookup("limit").DefValue != "50" {
		t.Errorf("search --limit default = %q, want 50",
			searchCmd.Flags().Lookup("limit").DefValue)
	}
}

func TestExportRefusesToWriteIntoMailDirectory(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "export", "--out", root)
	if err == nil {
		t.Fatal("export into the Mail directory returned nil error, want a refusal")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "mail directory") {
		t.Errorf("error = %v, want it to explain the refusal", err)
	}
}

func TestExportRequiresOutFlag(t *testing.T) {
	root := setupFixtureRoot(t)
	if err := executeExpectingError(t, root, "export"); err == nil {
		t.Error("export without --out returned nil error, want a requirement error")
	}
}

func TestStatsEmitsOnlySummary(t *testing.T) {
	out := runCommand(t, "stats")
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want exactly the summary:\n%s", len(lines), out)
	}

	var s map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &s); err != nil {
		t.Fatal(err)
	}
	if s["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\"", s["type"])
	}
	if int(s["total"].(float64)) != 5 {
		t.Errorf("total = %v, want 5", s["total"])
	}
}

func TestStatsCountsTopSenders(t *testing.T) {
	out := runCommand(t, "stats")
	var s map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil {
		t.Fatal(err)
	}
	senders, ok := s["top_senders"].([]any)
	if !ok || len(senders) == 0 {
		t.Fatalf("top_senders = %v, want entries", s["top_senders"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mail/ -run 'TestExport|TestStats' -v`
Expected: FAIL — unknown command "export"

- [ ] **Step 3: Write the export command**

Create `cmd/mail/export.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/emlx"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export matching messages to files",
	Long: `Write matching messages into a directory you name.

This is the only command that creates files, and it never writes inside
Mail's own data. Formats: eml (default) or json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagOut == "" {
			return fmt.Errorf("--out is required: name a directory to write into")
		}

		filter, err := buildFilter()
		if err != nil {
			return err
		}

		store, paths, err := openMail()
		if err != nil {
			return err
		}
		defer store.Close()

		// Refuse to write into Mail's own data. This guard is what keeps
		// the tool read-only with respect to the user's mailbox.
		absOut, err := filepath.Abs(flagOut)
		if err != nil {
			return err
		}
		absMail, err := filepath.Abs(store.Paths().Root)
		if err != nil {
			return err
		}
		if absOut == absMail || strings.HasPrefix(absOut+string(filepath.Separator),
			absMail+string(filepath.Separator)) {
			return fmt.Errorf(
				"refusing to export into the Mail directory (%s): choose a different --out", absMail)
		}

		if err := os.MkdirAll(absOut, 0o755); err != nil {
			return err
		}

		// mbox writes every message into one file, so the handle is opened
		// once here rather than per message.
		var mboxFile *os.File
		if flagExportAs == "mbox" {
			mboxFile, err = os.Create(filepath.Join(absOut, "archive.mbox"))
			if err != nil {
				return err
			}
			defer mboxFile.Close()
		}

		var written int
		scanner := scan.New(store, paths)
		stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
			func(m output.Message) error {
				if m.Error != "" {
					return nil // nothing to export for an unreadable message
				}
				switch flagExportAs {
				case "mbox":
					path, ok := paths.Resolve(m.ID)
					if !ok {
						return nil
					}
					file, err := emlx.ParseFile(path)
					if err != nil {
						return nil
					}
					written++
					return writeMboxEntry(mboxFile, m, file.MIME)
				case "json":
					data, err := json.MarshalIndent(m, "", "  ")
					if err != nil {
						return err
					}
					written++
					return os.WriteFile(
						filepath.Join(absOut, fmt.Sprintf("%d.json", m.ID)), data, 0o644)
				default:
					// .eml is the .emlx MIME section with the plist trailer removed.
					path, ok := paths.Resolve(m.ID)
					if !ok {
						return nil
					}
					file, err := emlx.ParseFile(path)
					if err != nil {
						return nil // skip; the scan already reported it
					}
					written++
					return os.WriteFile(
						filepath.Join(absOut, fmt.Sprintf("%d.eml", m.ID)), file.MIME, 0o644)
				}
			})
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "exported %d messages to %s\n", written, absOut)
		if stats.Skipped > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%d messages skipped (%d missing files, %d parse errors)\n",
				stats.Skipped, stats.MissingFiles, stats.ParseErrors)
		}
		return nil
	},
}

// writeMboxEntry appends one message in mboxrd form.
//
// A line beginning "From " starts a new message in mbox, so any such line
// inside the body must be escaped or the archive splits one message into
// two on import. mboxrd prefixes ">" and escapes already-escaped forms
// (">From " becomes ">>From "), keeping the transformation reversible.
func writeMboxEntry(w io.Writer, m output.Message, mime []byte) error {
	from := m.From.Address
	if from == "" {
		from = "unknown@localhost"
	}
	date := m.Date
	if t, err := time.Parse(time.RFC3339, m.Date); err == nil {
		date = t.Format("Mon Jan _2 15:04:05 2006")
	}
	if _, err := fmt.Fprintf(w, "From %s %s\n", from, date); err != nil {
		return err
	}

	for _, line := range strings.Split(string(mime), "\n") {
		trimmed := strings.TrimSuffix(line, "\r")
		if isFromLine(trimmed) {
			if _, err := fmt.Fprint(w, ">"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// isFromLine reports whether a line needs mboxrd escaping: "From " itself,
// or an already-escaped ">From ", ">>From ", and so on.
func isFromLine(line string) bool {
	i := 0
	for i < len(line) && line[i] == '>' {
		i++
	}
	return strings.HasPrefix(line[i:], "From ")
}

func init() {
	addFilterFlags(exportCmd, 0)
	exportCmd.Flags().StringVar(&flagOut, "out", "", "directory to write into (required)")
	exportCmd.Flags().StringVar(&flagExportAs, "as", "eml",
		"export format: eml (one file per message), json, or mbox (single archive)")
	rootCmd.AddCommand(exportCmd)
}
```

- [ ] **Step 4: Write the stats command**

Create `cmd/mail/stats.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Summarize a set of messages",
	Long: `Emit one aggregate record describing the matching messages:
top senders, link domains, volume by week, read and flag counts, and
thread count.

This is the view for reasoning about many messages together rather than
one at a time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScan(cmd, "", true)
	},
}

func init() {
	addFilterFlags(statsCmd, 0)
	rootCmd.AddCommand(statsCmd)
}
```

- [ ] **Step 5: Run the full test suite**

Run: `go test ./... -v`
Expected: PASS across every package

- [ ] **Step 6: Commit**

```bash
git add cmd/mail/export.go cmd/mail/stats.go cmd/mail/exportstats_test.go
git commit -m "feat: add export and stats commands"
```

---

### Task 18: Build verification and real-data smoke test

**Files:**
- Create: `README.md`
- Create: `Makefile`

**Interfaces:**
- Consumes: the complete CLI
- Produces: a built `mail` binary and a verified real-data run

**Context for the implementer:** Everything up to here ran against synthetic fixtures. This task is the first contact with real mail, and it is the moment the reconstructed schema is validated.

The Envelope Index schema in `internal/testdata/gen.go` was rebuilt from published research, not read off this machine — Full Disk Access was unavailable when the plan was written. **If `mail doctor` fails with a SQL error naming a missing column or table, that is the expected discovery, not a bug in this task.** Fix `internal/mailstore/store.go` and `internal/testdata/gen.go` to match reality, then re-run. Schema knowledge lives only in those two files, so a correction stays local.

- [ ] **Step 1: Write the Makefile**

Create `Makefile`:

```makefile
.PHONY: build test race clean install

build:
	go build -o mail ./cmd/mail

test:
	go test ./...

race:
	go test ./... -race

clean:
	rm -f mail

install: build
	install -m 0755 mail /usr/local/bin/mail
```

- [ ] **Step 2: Verify the full suite and a clean build**

Run:
```bash
make test && make race && make build && ./mail --help
```
Expected: all tests pass, the binary builds, help lists doctor, search, show, links, export, and stats.

- [ ] **Step 3: Confirm the binary is statically linked**

Run: `otool -L ./mail`
Expected: only system libraries (`libSystem`, `libresolv`). No SQLite dependency — the `modernc.org/sqlite` driver is pure Go, which is what keeps this a single portable binary.

- [ ] **Step 4: Write the README**

Create `README.md`:

```markdown
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

## Install

```bash
make build            # produces ./mail
make install          # copies it to /usr/local/bin
```

## Commands

```bash
mail doctor                                  # verify access; run this first
mail search --from linkedin --since 30d      # metadata filters (instant)
mail search "invoice" --since 90d            # body text search
mail show 48213                              # one message in full
mail show 48213 --raw                        # the original .emlx
mail links --from linkedin.com               # clean, fetchable URLs
mail links --group-by domain                 # who links you where
mail export --out ./archive --since 1y       # write .eml files
mail export --out ./bk --as mbox             # single re-importable archive
mail stats --since 6m                        # aggregate view
```

All commands share the same filters: `--from`, `--to`, `--subject`,
`--mailbox`, `--since`, `--until`, `--unread`, `--flagged`,
`--has-attachment`, `--limit`.

## Output

JSONL by default — one object per line, streamed as results are parsed.
`--format json` buffers a single array; `table` and `text` are for reading.

Errors go to stderr, so stdout stays pipeable:

```bash
mail search --since 7d | jq -r '.subject'
mail links --from linkedin | jq -r '.url_canonical'
```

Exit codes: `0` success, `1` error, `2` Full Disk Access missing.

## Design notes

**Read-only.** Mail's database is opened `mode=ro&immutable=1`. Nothing
under `~/Library/Mail` is ever written. `export` is the only command that
creates files, and it refuses to write inside the Mail directory.

**No network requests.** Tracking parameters are stripped and redirect
wrappers unwrapped offline, from the URL itself. This tool never fetches a
link — following a tracking URL would signal mail opens and leak activity.
Fetching is left to whoever consumes the output, on an explicit request.

**Signals are facts.** `is_bulk` means a `List-Unsubscribe` or
`Precedence: bulk` header exists. There is no `is_spam` or `is_interesting`
field: that judgment belongs to the reader.

## Not supported

Sending, moving messages, creating mailboxes, flagging, and deleting are
deliberately out of scope. This tool reads.
```

- [ ] **Step 5: Real-data smoke test**

This is the only step touching real mail. Run it yourself and read the output.

```bash
./mail doctor
```

Expected: the discovered version directory, index path, message and mailbox
counts, and `All checks passed`.

If it reports a permission problem, follow the instructions it prints, restart the terminal, and run it again.

If it fails with a SQL error naming an unknown column or table, the reconstructed schema does not match this machine. Correct `internal/mailstore/store.go` and `internal/testdata/gen.go`, re-run `make test`, and repeat.

- [ ] **Step 6: Verify search against real mail**

```bash
./mail search --limit 5 --format table
./mail search --limit 3 | jq -r '.subject'
./mail links --limit 20 --group-by domain
```

Expected: real subjects in the table, valid JSON through `jq`, and a domain rollup.

- [ ] **Step 7: Commit**

```bash
git add README.md Makefile
git commit -m "docs: add README and Makefile"
```

---

## Self-Review

**Spec coverage — every spec section maps to a task:**

| Spec requirement | Task |
|---|---|
| Envelope Index read-only access | 5 |
| Runtime `V*` discovery | 2 |
| Cocoa epoch conversion | 1 |
| `.emlx` three-part parsing | 7 |
| Body text, charsets, HTML→text | 8 |
| Link normalization + canonicalization | 9 |
| Link classification (content/tracking/action) | 9 |
| `anchor_mismatch` as a fact | 9 |
| No network requests | 9 (offline unwrap only) |
| Header signals, no judgments | 10 |
| JSONL contract + all four formats | 11 |
| `body.truncated` / `--max-body-chars` | 11, 12 |
| Summary as final tagged line | 11, 13 |
| Streaming + parallel reads | 12 |
| Order preservation | 12 |
| Per-message error isolation | 12 |
| Corpus rollup | 13 |
| Exit codes 0/1/2 | 14 |
| Permission preflight + terminal guidance | 14, 15 |
| `doctor` | 15 |
| `search` (metadata + body) | 15 |
| `show` + `--raw` | 16 |
| `links` + `--all` + `--group-by` | 16 |
| `export` as `eml` | 17 |
| `export` as `json` | 17 |
| `export` as `mbox` (mboxrd escaping) | 17 |
| `export` refuses to write into Mail dir | 17 |
| Per-command `--limit` defaults | 14, 17 |
| Charset decoding via IANA registry | 8 |
| Charset fallback declared in `body.source` | 8 |
| `show` is an indexed lookup, not a scan | 4, 12, 16 |
| `stats` | 17 |
| Fixture-based testing | 3, used throughout |
| Real-data smoke test | 18 |
| Deferred: send/move/flag/delete/cache/MCP | none — correctly absent |

**Type consistency:** `Filter` (Task 4) is consumed unchanged by `store.Query` (5), `scan.Options` (12), and every command (15–17). `output.Message` (11) is produced by `scan` (12) and consumed by `corpus` (13) and all renderers. `analyze.Link` (9) maps to `output.Link` (11) in `scan.buildOne`. `emlx.Message` (8) is consumed by `analyze.ExtractLinks` and `DeriveSignals` (9, 10). No signature drift found.

**Known plan-level gaps, stated deliberately:**

1. The Envelope Index schema is reconstructed, not verified against this machine. Task 18 Step 5 is where it gets validated; the fix is scoped to two files (`internal/mailstore/store.go` and `internal/testdata/gen.go`).

**Corrections applied after external review (2026-08-16):**

An independent review compiled and ran the plan's code and found 16 defects. All are fixed above:

| # | Severity | Defect | Resolution |
|---|---|---|---|
| 1 | Blocker | Cocoa constant `807629482` transposed; correct value is `807653482`. Following TDD would have "fixed" the implementation and corrupted epoch conversion at the root. | Corrected in Tasks 1 and 3; value re-verified independently |
| 2 | Blocker | `HTMLToText` "collapses whitespace" test unsatisfiable — impl yields `a\n\nb`, test wanted `a\nb` | Test expectation corrected to `a\n\nb`; blank-line paragraph separation is the better LLM output |
| 3 | Major | `--limit` pushed into SQL before the body pass, so body search silently scanned only the newest N | `Run` strips the SQL limit when a body query is active and counts matches instead; two tests added |
| 4 | Major | Scanner leaked goroutines when `emit` errored (e.g. `mail search \| head -1`) | `Run` owns a cancellable context and drains `results` on every early return; leak test added |
| 5 | Major | `--limit` default 50 leaked into `export`, `stats`, `links` — silent truncation of archives and aggregates | `addFilterFlags` takes a per-command default: 50 for `search`, 0 elsewhere; test added |
| 6 | Major | `show <id>` scanned and parsed the entire mailbox to find one message | `Filter.ROWID` + `Scanner.One` make it a primary-key lookup |
| 7 | Major | Spec promised `.mbox` export; no task implemented it | Implemented as mboxrd (reversible `>From ` escaping), verified; tests added |
| 8 | Minor | `doctor` printed the permission message twice, once to stdout | Doctor returns the error unprinted; `main` explains it once, on stderr |
| 9 | Minor | Charset fallback never declared; non-latin1 charsets became silent mojibake | IANA registry decoding via `x/text`; fallback recorded in `body.source`; verified across KOI8-R, Shift_JIS, Windows-1252 |
| 10 | Minor | Attachment `Size` reported base64-encoded length | Base64 parts decoded before measuring |
| 11 | Minor | `links` and `export` discarded `Stats`, so skip counts were never reported | Both now print the skip line |
| 12 | Minor | `resetFlags` extended only at Task 17, so Task 16 tests passed through wrong code paths | All flags declared in `root.go`; full `resetFlags` present from Task 15 |
| 13 | Minor | Hand-rolled `itoa`/`formatWeek` — 30 lines of avoidable defect surface | Replaced with `fmt.Sprintf("%d-W%02d")`; string date comparison documented |
| 14 | Minor | `tagSummary` ignored `*Summary`; `table`/`text` dropped summaries entirely; `truncate` could split a rune | All three fixed; tests added |
| 15 | Minor | `isValidUTF8` allocated two copies per part; `Stats` classified errors by string matching | `utf8.Valid`; `errKind` enum carried in `result` |
| 16 | Minor | Doc drift: task-order paragraph, `buildFilter` signature, Mail.app version, dead `var _ = context.Background` | All corrected; spec notes why Mail.app version is not reported |

Confirmed sound by the same review and left unchanged: `Normalize` recursion termination, the LinkedIn dedup trace, order preservation on the happy path, and `modernc.org/sqlite` accepting both the spaced path and a column named `read`.
```
