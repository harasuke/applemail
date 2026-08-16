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
	Account        string
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
	if f.Account != "" {
		// The account is the authority portion of the mailbox URL
		// (scheme://<account>/<mailbox>), so match it exactly rather than
		// as a bare substring.
		clauses = append(clauses, "mb.url LIKE '%://' || ? || '/%'")
		args = append(args, f.Account)
	}
	if f.Since != nil {
		clauses = append(clauses, "m.date_received >= ?")
		args = append(args, TimeToEpoch(*f.Since))
	}
	if f.Until != nil {
		clauses = append(clauses, "m.date_received <= ?")
		args = append(args, TimeToEpoch(*f.Until))
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
