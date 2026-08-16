package scan

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mirko/applemail/internal/mailstore"
	"github.com/mirko/applemail/internal/output"
	"github.com/mirko/applemail/internal/testdata"
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

func TestRunReportsTruncatedEmlxBody(t *testing.T) {
	s, root := newFixtureScanner(t)
	// Overwrite one message with a declared byte count far exceeding its
	// real size; the scan must report it as a parse error, not emit a body.
	victim := testdata.EmlxPath(root, testdata.DefaultMessages()[0])
	if err := os.WriteFile(victim, []byte("99999\nSubject: x\r\n\r\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, stats := collect(t, s, Options{})
	if stats.ParseErrors != 1 {
		t.Errorf("ParseErrors = %d, want 1", stats.ParseErrors)
	}

	var errored []output.Message
	for _, m := range got {
		if m.Error != "" {
			errored = append(errored, m)
		}
	}
	if len(errored) != 1 {
		t.Fatalf("got %d error records, want 1", len(errored))
	}
	if errored[0].ID != 1 {
		t.Errorf("errored record ID = %d, want 1", errored[0].ID)
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

func TestRunBodyQueryLimitIgnoresErrorRecords(t *testing.T) {
	s, root := newFixtureScanner(t)
	// The store returns newest first, so ROWID 5 leads the scan. Deleting
	// its body makes it an error record that must not consume the match
	// budget; the limit still has to yield two real body matches.
	victim := testdata.EmlxPath(root, testdata.DefaultMessages()[4])
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	got, stats := collect(t, s, Options{
		BodyQuery: "e",
		Filter:    mailstore.Filter{Limit: 2},
	})
	// The missing ROWID 5 leads the newest-first stream as an error record.
	// It is emitted for reporting but must not consume the match budget, so
	// the two newest real body matches (4 and 3) must still be returned.
	if len(got) != 3 {
		t.Fatalf("got %d emitted records, want 3 (one error record + two matches)", len(got))
	}
	if got[0].ID != 5 || got[0].Error == "" {
		t.Errorf("got[0] = {ID:%d Error:%q}, want the ROWID 5 error record", got[0].ID, got[0].Error)
	}
	if got[1].ID != 4 || got[2].ID != 3 {
		t.Errorf("matched IDs = [%d %d], want [4 3]", got[1].ID, got[2].ID)
	}
	if stats.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", stats.MissingFiles)
	}
	if stats.Emitted != 3 {
		t.Errorf("Emitted = %d, want 3 (one error record + two matches)", stats.Emitted)
	}
}

func TestOneNonMatchingBodyQueryReturnsNotFound(t *testing.T) {
	s, _ := newFixtureScanner(t)
	msg, found, err := s.One(1, Options{BodyQuery: "no message contains this"})
	if err != nil {
		t.Fatalf("One: %v", err)
	}
	if found {
		t.Error("found = true, want false for a non-matching body query")
	}
	if msg.ID != 0 || msg.Error != "" {
		t.Errorf("msg = %+v, want the zero value", msg)
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

func TestRunRespectsContextCancellation(t *testing.T) {
	s, _ := newFixtureScanner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the run starts

	_, err := s.Run(ctx, Options{}, func(output.Message) error { return nil })
	if err == nil {
		t.Error("Run returned nil error, want a cancellation error")
	}
}
