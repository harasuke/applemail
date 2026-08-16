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

// Open opens the Envelope Index read-only.
//
// mode=ro guarantees this process can never modify the user's mail. It is
// deliberately NOT opened with immutable=1: Mail writes the index in WAL
// mode, and immutable=1 makes SQLite ignore the -wal file, so recent changes
// (moves, flags) would be invisible until Mail checkpoints. mode=ro still
// reads the WAL, which is safe because WAL is designed for concurrent
// readers alongside a single writer.
func Open(paths *Paths) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro", paths.IndexPath)
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
		m.DateSent = EpochToTime(sent)
		m.DateReceived = EpochToTime(recv)
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
