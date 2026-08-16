package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirko/applemail/internal/testdata"
)

func TestShowEmitsOneMessage(t *testing.T) {
	out := runCommand(t, "show", "1")
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("show output is not valid JSON: %v\n%s", err, out)
	}
	if int64(m["id"].(float64)) != 1 {
		t.Errorf("id = %v, want 1", m["id"])
	}
	body := m["body"].(map[string]any)
	if !strings.Contains(body["text"].(string), "Just saying hello") {
		t.Errorf("body.text = %v", body["text"])
	}
}

func TestShowRawEmitsOriginalEmlx(t *testing.T) {
	out := runCommand(t, "show", "1", "--raw")
	if !strings.Contains(out, "Subject: Plain text hello") {
		t.Errorf("raw output does not contain the original headers:\n%s", out)
	}
	if !strings.Contains(out, "<?xml") {
		t.Errorf("raw output does not contain the plist trailer:\n%s", out)
	}
}

func TestShowUnknownIDFails(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "show", "99999")
	if err == nil {
		t.Error("show 99999 returned nil error, want a not-found error")
	}
}

func TestLinksEmitsContentLinksOnly(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com")
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("links emitted nothing")
	}
	for _, line := range lines {
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if l["class"] != "content" {
			t.Errorf("class = %v, want only content links by default", l["class"])
		}
	}
}

func TestLinksDedupsLinkedInJobs(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com")
	lines := nonEmptyLines(out)
	// Two distinct jobs despite three job anchors in the fixture.
	if len(lines) != 2 {
		t.Fatalf("got %d content links, want 2 after dedup:\n%s", len(lines), out)
	}
}

func TestLinksDedupsAcrossMessages(t *testing.T) {
	jobA := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123"
	jobB := "https://www.linkedin.com/comm/jobs/view/4055512233?trk=eml-digest"

	mime := func(subject, anchors string) string {
		return "From: LinkedIn Jobs <jobs-noreply@linkedin.com>\r\n" +
			"To: user@example.com\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n\r\n" +
			"<html><body>" + anchors + "</body></html>\r\n"
	}

	msgs := []testdata.FixtureMessage{
		{
			ROWID: 1, Subject: "Digest 1",
			SenderName: "LinkedIn Jobs", SenderAddr: "jobs-noreply@linkedin.com",
			ToAddr: "user@example.com", DateSent: 1785960682, DateRecv: 1785960700,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Summary: "Digest 1",
			RawMIME: mime("Digest 1",
				`<a href="`+jobA+`">Senior Backend Engineer</a>`+
					`<a href="`+jobB+`">Platform Engineer</a>`),
		},
		{
			ROWID: 2, Subject: "Digest 2",
			SenderName: "LinkedIn Jobs", SenderAddr: "jobs-noreply@linkedin.com",
			ToAddr: "user@example.com", DateSent: 1786023082, DateRecv: 1786023100,
			MailboxURL: "imap://user@example.com/INBOX", MailboxName: "INBOX",
			Summary: "Digest 2",
			RawMIME: mime("Digest 2",
				`<a href="`+jobA+`">Senior Backend Engineer again</a>`),
		},
	}

	out := runCommandOnFixture(t, msgs, "links")
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("got %d content links, want 2 after cross-message dedup:\n%s", len(lines), out)
	}

	var countA, countB int
	for _, line := range lines {
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		canonical := l["url_canonical"].(string)
		if strings.Contains(canonical, "4021887364") {
			countA++
		}
		if strings.Contains(canonical, "4055512233") {
			countB++
		}
	}
	if countA != 1 {
		t.Errorf("job 4021887364 appears %d times, want exactly 1:\n%s", countA, out)
	}
	if countB != 1 {
		t.Errorf("job 4055512233 appears %d times, want exactly 1:\n%s", countB, out)
	}
}

func runCommandOnFixture(t *testing.T, msgs []testdata.FixtureMessage, args ...string) string {
	t.Helper()
	root := testdata.BuildMailDir(t, msgs)

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"--mail-dir", root}, args...))
	t.Cleanup(resetFlags)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return stdout.String()
}

func TestLinksAllIncludesTracking(t *testing.T) {
	out := runCommand(t, "links", "--from", "jobs-noreply@linkedin.com", "--all")

	var sawTracking bool
	for _, line := range nonEmptyLines(out) {
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		if l["class"] == "tracking" {
			sawTracking = true
		}
	}
	if !sawTracking {
		t.Errorf("--all did not include tracking links:\n%s", out)
	}
}

func TestLinksGroupByDomain(t *testing.T) {
	out := runCommand(t, "links", "--group-by", "domain")
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		t.Fatal("group-by domain emitted nothing")
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &c); err != nil {
		t.Fatal(err)
	}
	if _, ok := c["key"]; !ok {
		t.Errorf("grouped output has no \"key\" field: %s", lines[0])
	}
	if _, ok := c["count"]; !ok {
		t.Errorf("grouped output has no \"count\" field: %s", lines[0])
	}
}

func TestLinksRejectsUnknownGroupBy(t *testing.T) {
	root := setupFixtureRoot(t)
	err := executeExpectingError(t, root, "links", "--group-by", "bogus")
	if err == nil {
		t.Fatal("links --group-by bogus returned nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %v, want it to name the bad value", err)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
