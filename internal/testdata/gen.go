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
	DateSent    float64 // Unix epoch seconds
	DateRecv    float64 // Unix epoch seconds
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
			ToAddr: "user@example.com", DateSent: 1785960682, DateRecv: 1785960700,
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
			ToAddr: "user@example.com", DateSent: 1786023082, DateRecv: 1786023100,
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
			ToAddr: "user@example.com", DateSent: 1786109482, DateRecv: 1786109500,
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
			ToAddr: "user@example.com", DateSent: 1786195882, DateRecv: 1786195900,
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
			ToAddr: "user@example.com", DateSent: 1786282282, DateRecv: 1786282300,
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
