package emlx

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

func extractFixture(t *testing.T, idx int) *Message {
	t.Helper()
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := ParseFile(testdata.EmlxPath(root, msgs[idx]))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	m, err := Extract(f)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return m
}

func TestExtractPlainText(t *testing.T) {
	m := extractFixture(t, 0)
	if !strings.Contains(m.Text, "Just saying hello") {
		t.Errorf("Text = %q, want the plain body", m.Text)
	}
	if m.TextSource != "text/plain" {
		t.Errorf("TextSource = %q, want text/plain", m.TextSource)
	}
	if m.Subject != "Plain text hello" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if m.MessageID != "<plain-1@example.com>" {
		t.Errorf("MessageID = %q", m.MessageID)
	}
}

func TestExtractHTMLOnlyConvertsToText(t *testing.T) {
	m := extractFixture(t, 1)
	if m.TextSource != "text/html" {
		t.Errorf("TextSource = %q, want text/html", m.TextSource)
	}
	if !strings.Contains(m.Text, "This week in news") {
		t.Errorf("Text = %q, want the converted text", m.Text)
	}
	if strings.Contains(m.Text, "<b>") || strings.Contains(m.Text, "<p>") {
		t.Errorf("Text = %q, want no markup", m.Text)
	}
	if m.HTML == "" {
		t.Error("HTML is empty, want the original markup retained")
	}
}

func TestExtractMultipartPrefersPlainAndListsAttachments(t *testing.T) {
	m := extractFixture(t, 2)
	if !strings.Contains(m.Text, "Please see attached") {
		t.Errorf("Text = %q, want the plain part", m.Text)
	}
	if m.TextSource != "text/plain" {
		t.Errorf("TextSource = %q, want text/plain", m.TextSource)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(m.Attachments))
	}
	if m.Attachments[0].Name != "report.pdf" {
		t.Errorf("attachment name = %q, want report.pdf", m.Attachments[0].Name)
	}
	if m.Attachments[0].MIME != "application/pdf" {
		t.Errorf("attachment MIME = %q, want application/pdf", m.Attachments[0].MIME)
	}
}

func TestExtractDecodesEncodedWordSubject(t *testing.T) {
	m := extractFixture(t, 3)
	if m.Subject != "Perché è importante" {
		t.Errorf("Subject = %q, want the decoded form", m.Subject)
	}
}

func TestExtractDecodesQuotedPrintableBody(t *testing.T) {
	m := extractFixture(t, 3)
	if !strings.Contains(m.Text, "perché") {
		t.Errorf("Text = %q, want quoted-printable decoded", m.Text)
	}
	if strings.Contains(m.Text, "=C3=A9") {
		t.Errorf("Text = %q, still contains quoted-printable escapes", m.Text)
	}
}

func TestDecodeCharsetHandlesRegisteredEncodings(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		charset string
		want    string
	}{
		{"utf-8", []byte("perché"), "utf-8", "perché"},
		{"iso-8859-1", []byte{0x70, 0x65, 0x72, 0x63, 0x68, 0xE9}, "iso-8859-1", "perché"},
		{"windows-1252", []byte{0x93, 0x68, 0x69, 0x94}, "windows-1252", "“hi”"},
		{"koi8-r", []byte{0xD0, 0xD2, 0xC9, 0xD7, 0xC5, 0xD4}, "koi8-r", "привет"},
		{"shift_jis", []byte{0x93, 0xFA, 0x96, 0x7B}, "shift_jis", "日本"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, fallback, err := decodeCharset(tt.charset, bytes.NewReader(tt.raw))
			if err != nil {
				t.Fatalf("decodeCharset: %v", err)
			}
			if fallback != "" {
				t.Errorf("fallback = %q, want none for a registered charset", fallback)
			}
			got, _ := io.ReadAll(r)
			if string(got) != tt.want {
				t.Errorf("decoded = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeCharsetDeclaresFallback(t *testing.T) {
	// An unknown label with bytes that are not valid UTF-8 must fall back
	// to latin-1 AND say so, rather than emitting silent mojibake.
	r, fallback, err := decodeCharset("x-unknown-charset", bytes.NewReader([]byte{0xE9}))
	if err != nil {
		t.Fatalf("decodeCharset: %v", err)
	}
	if fallback != "latin-1" {
		t.Errorf("fallback = %q, want latin-1", fallback)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "é" {
		t.Errorf("decoded = %q, want é", got)
	}
}

func TestSourceLabelRecordsFallback(t *testing.T) {
	if got := sourceLabel("text/plain", ""); got != "text/plain" {
		t.Errorf("sourceLabel with no fallback = %q, want text/plain", got)
	}
	want := "text/plain; charset-fallback=latin-1"
	if got := sourceLabel("text/plain", "latin-1"); got != want {
		t.Errorf("sourceLabel = %q, want %q", got, want)
	}
}

func TestHTMLToTextStripsTagsAndDecodesEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips tags", "<p>Hello <b>world</b></p>", "Hello world"},
		{"decodes entities", "<p>caf&eacute; &amp; bar</p>", "café & bar"},
		{"drops script content", "<p>Hi</p><script>alert(1)</script>", "Hi"},
		{"drops style content", "<style>p{color:red}</style><p>Hi</p>", "Hi"},
		// Paragraphs stay separated by exactly one blank line: block tags
		// each contribute a newline, and runs of 3+ collapse to 2.
		{"collapses whitespace", "<p>a</p>\n\n\n<p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.TrimSpace(HTMLToText(tt.in))
			if got != tt.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
