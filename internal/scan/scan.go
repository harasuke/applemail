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
	matches := 0
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
			// Error records consume no match budget.
			if ready.kind == errNone {
				matches++
			}
			if matchLimit > 0 && matches >= matchLimit {
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
	res := s.buildOne(0, metas[0], opts)
	if res.drop {
		return output.Message{}, false, nil
	}
	return res.msg, true, nil
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
	if file.Truncated {
		msg.Error = "message body truncated: declared byte count exceeds file size"
		return result{index: index, msg: msg, kind: errParse}
	}

	decoded, err := emlx.Extract(file)
	if err != nil {
		msg.Error = fmt.Sprintf("extract message: %v", err)
		return result{index: index, msg: msg, kind: errParse}
	}
	msg.MessageIDHeader = decoded.MessageID

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
