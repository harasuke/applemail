package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func sampleMessage() Message {
	return Message{
		ID:      48213,
		Subject: "Test subject",
		From:    Address{Name: "Alice", Address: "alice@example.com"},
		Mailbox: "INBOX",
		Date:    "2026-08-14T09:31:22Z",
		Body:    Body{Text: "Hello there", Source: "text/plain"},
		Flags:   Flags{Read: true},
	}
}

func TestJSONLWritesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("jsonl", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write(sampleMessage()); err != nil {
		t.Fatal(err)
	}
	if err := r.Write(sampleMessage()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestJSONLUsesContractFieldNames(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(sampleMessage())
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "subject", "from", "mailbox", "date", "body", "flags"} {
		if _, ok := m[key]; !ok {
			t.Errorf("output is missing the %q field", key)
		}
	}
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatal("body is not an object")
	}
	if _, ok := body["text"]; !ok {
		t.Error("body is missing the \"text\" field")
	}
	if _, ok := body["source"]; !ok {
		t.Error("body is missing the \"source\" field")
	}
}

func TestJSONFormatProducesOneArray(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	r.Write(sampleMessage())
	r.Write(sampleMessage())
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("got %d elements, want 2", len(arr))
	}
}

func TestJSONFormatEmptyProducesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("json", &buf)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("got %d elements, want 0", len(arr))
	}
}

func TestSummaryCarriesTypeField(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(Summary{Total: 3, Unread: 1})
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\"", m["type"])
	}
}

func TestTableFormatShowsKeyColumns(t *testing.T) {
	var buf bytes.Buffer
	r, err := NewRenderer("table", &buf)
	if err != nil {
		t.Fatal(err)
	}
	r.Write(sampleMessage())
	r.Close()

	out := buf.String()
	for _, want := range []string{"48213", "Test subject", "alice@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestTableDateNotTruncated(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("table", &buf)
	r.Write(sampleMessage()) // Date: "2026-08-14T09:31:22Z"
	r.Close()

	out := buf.String()
	if !strings.Contains(out, "2026-08-14T09:31:22Z") {
		t.Errorf("table output truncated the RFC3339 date:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("table output contains an ellipsis in the date:\n%s", out)
	}
}

func TestUnknownFormatIsRejected(t *testing.T) {
	if _, err := NewRenderer("yaml", &bytes.Buffer{}); err == nil {
		t.Error("NewRenderer(\"yaml\") = nil error, want a rejection")
	}
}

func TestSummaryPointerIsAlsoTagged(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("jsonl", &buf)
	r.Write(&Summary{Total: 1})
	r.Close()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "summary" {
		t.Errorf("type = %v, want \"summary\" for a *Summary too", m["type"])
	}
}

func TestTableRendersSummary(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("table", &buf)
	r.Write(Summary{Total: 42, Unread: 7})
	r.Close()

	out := buf.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("table output is empty for a summary — stats --format table would print nothing")
	}
	if !strings.Contains(out, "42") {
		t.Errorf("summary table missing the total:\n%s", out)
	}
}

func TestTableRendersCounts(t *testing.T) {
	var buf bytes.Buffer
	r, _ := NewRenderer("table", &buf)
	r.Write(Count{Key: "linkedin.com", Count: 9})
	r.Close()

	out := buf.String()
	if !strings.Contains(out, "linkedin.com") || !strings.Contains(out, "9") {
		t.Errorf("count table missing data:\n%s", out)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	// Accented text must never be cut mid-character.
	got := truncate("perché è importante davvero", 10)
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if n := len([]rune(got)); n != 10 {
		t.Errorf("truncate returned %d runes, want 10", n)
	}
}
