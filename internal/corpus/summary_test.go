package corpus

import (
	"testing"

	"github.com/mirko/applemail/internal/output"
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
