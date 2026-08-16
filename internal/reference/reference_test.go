package reference

import (
	"strings"
	"testing"
)

// TestMarkdownContainsSchemaFields guards the reference doc against drift:
// if a field is renamed in the output package, the doc should be updated to
// match. The doc is the contract an agent reads, so it must name the real
// JSON keys.
func TestMarkdownContainsMessageSchemaFields(t *testing.T) {
	doc := Markdown()
	for _, field := range []string{
		`"id"`, `"message_id"`, `"thread_id"`, `"mailbox"`, `"account"`,
		`"date"`, `"from"`, `"to"`, `"subject"`, `"flags"`, `"body"`,
		`"links"`, `"attachments"`, `"signals"`, `"error"`,
		`"read"`, `"flagged"`, `"has_attachment"`,
		`"text"`, `"truncated"`, `"source"`,
		`"is_bulk"`, `"is_automated"`, `"has_unsubscribe"`, `"reply_to_differs"`, `"spf_dkim_present"`,
	} {
		if !strings.Contains(doc, field) {
			t.Errorf("reference doc is missing field %s", field)
		}
	}
}

func TestMarkdownContainsLinkSchemaFields(t *testing.T) {
	doc := Markdown()
	for _, field := range []string{
		`"url_canonical"`, `"url_original"`, `"domain"`, `"anchor_text"`,
		`"class"`, `"dedup_key"`, `"message_id"`, `"anchor_mismatch"`,
	} {
		if !strings.Contains(doc, field) {
			t.Errorf("reference doc is missing link field %s", field)
		}
	}
}

func TestMarkdownContainsSummaryAndExitCodes(t *testing.T) {
	doc := Markdown()
	for _, want := range []string{
		`"type": "summary"`, `"top_senders"`, `"top_domains"`, `"volume_by_week"`,
		"| 3 | Missing Automation permission", "| 2 | Missing Full Disk Access",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("reference doc is missing %q", want)
		}
	}
}
