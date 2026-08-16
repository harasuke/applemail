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
			rec.ID, truncate(rec.Date, 20),
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

	case Trash:
		if !r.header {
			fmt.Fprintln(r.tw, "ID\tFROM\tSUBJECT\tACTION")
			r.header = true
		}
		_, err := fmt.Fprintf(r.tw, "%d\t%s\t%s\t%s\n",
			rec.ID, truncate(rec.From.Address, 32), truncate(rec.Subject, 60), rec.EffectiveAction())
		return err

	case Account:
		if !r.header {
			fmt.Fprintln(r.tw, "NAME\tEMAIL\tID")
			r.header = true
		}
		_, err := fmt.Fprintf(r.tw, "%s\t%s\t%s\n",
			truncate(rec.Name, 24), truncate(strings.Join(rec.Emails, ","), 40), rec.ID)
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
	case Trash:
		_, err := fmt.Fprintf(r.w, "%s: %s <%s> — %s\n",
			rec.EffectiveAction(), rec.From.Name, rec.From.Address, rec.Subject)
		return err
	case Account:
		_, err := fmt.Fprintf(r.w, "%s\t%s\t%s\n",
			rec.Name, strings.Join(rec.Emails, ","), rec.ID)
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
