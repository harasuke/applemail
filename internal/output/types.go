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
	ID        int64  `json:"id"`
	MessageID string `json:"message_id"`
	// MessageIDHeader is the RFC 822 Message-ID header. It is the identifier
	// AppleScript uses to address a message, and is consumed by the trash
	// command. Deliberately not serialized: the JSON contract's "message_id"
	// is Mail's internal index ID, a different value.
	MessageIDHeader string       `json:"-"`
	ThreadID        int64        `json:"thread_id"`
	Mailbox         string       `json:"mailbox"`
	Account         string       `json:"account"`
	Date            string       `json:"date"` // RFC 3339
	From            Address      `json:"from"`
	To              []Address    `json:"to"`
	Subject         string       `json:"subject"`
	Flags           Flags        `json:"flags"`
	Body            Body         `json:"body"`
	Links           []Link       `json:"links"`
	Attachments     []Attachment `json:"attachments"`
	Signals         Signals      `json:"signals"`
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

// Trash is one message selected for moving to the Trash.
type Trash struct {
	ID        int64   `json:"id"`
	MessageID string  `json:"message_id"` // RFC 822 Message-ID header
	Subject   string  `json:"subject"`
	From      Address `json:"from"`
	Action    string  `json:"action"` // always "trash"
	DryRun    bool    `json:"dry_run"`
}

// Account is one configured Mail account, as listed by `mail accounts`.
type Account struct {
	Name   string   `json:"name"`
	ID     string   `json:"id"`
	Emails []string `json:"emails"`
}

// EffectiveAction reports the action for display, substituting "dry-run"
// when the run was a preview.
func (t Trash) EffectiveAction() string {
	if t.DryRun {
		return "dry-run"
	}
	return t.Action
}
