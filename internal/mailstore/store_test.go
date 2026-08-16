package mailstore

import (
	"testing"
	"time"

	"github.com/mirko/applemail/internal/testdata"
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

func TestQueryConvertsEpochDates(t *testing.T) {
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
