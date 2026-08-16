// Package corpus accumulates aggregate statistics over a result set.
package corpus

import (
	"fmt"
	"sort"
	"time"

	"github.com/mirko/applemail/internal/output"
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
