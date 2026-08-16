package analyze

import (
	"net/mail"
	"testing"

	"github.com/mirko/applemail/internal/emlx"
)

func msgWithHeaders(h map[string][]string) *emlx.Message {
	return &emlx.Message{Headers: mail.Header(h)}
}

func TestDeriveSignalsBulkFromListUnsubscribe(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"List-Unsubscribe": {"<https://example.org/unsub>"},
	}))
	if !s.IsBulk {
		t.Error("IsBulk = false, want true when List-Unsubscribe is present")
	}
	if !s.HasUnsubscribe {
		t.Error("HasUnsubscribe = false, want true")
	}
}

func TestDeriveSignalsBulkFromPrecedence(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Precedence": {"bulk"},
	}))
	if !s.IsBulk {
		t.Error("IsBulk = false, want true when Precedence is bulk")
	}
}

func TestDeriveSignalsAutomatedFromAutoSubmitted(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Auto-Submitted": {"auto-generated"},
	}))
	if !s.IsAutomated {
		t.Error("IsAutomated = false, want true for Auto-Submitted")
	}
}

func TestDeriveSignalsAutomatedFromNoReplySender(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{},
		From:    "LinkedIn Jobs <jobs-noreply@linkedin.com>",
	}
	if s := DeriveSignals(m); !s.IsAutomated {
		t.Error("IsAutomated = false, want true for a noreply sender")
	}
}

func TestDeriveSignalsReplyToDiffers(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{"Reply-To": {"other@elsewhere.example"}},
		From:    "Sender <sender@example.com>",
	}
	if s := DeriveSignals(m); !s.ReplyToDiffers {
		t.Error("ReplyToDiffers = false, want true for a different Reply-To domain")
	}
}

func TestDeriveSignalsReplyToSameDomainDoesNotDiffer(t *testing.T) {
	m := &emlx.Message{
		Headers: mail.Header{"Reply-To": {"support@example.com"}},
		From:    "Sender <sender@example.com>",
	}
	if s := DeriveSignals(m); s.ReplyToDiffers {
		t.Error("ReplyToDiffers = true, want false within the same domain")
	}
}

func TestDeriveSignalsAuthPresent(t *testing.T) {
	s := DeriveSignals(msgWithHeaders(map[string][]string{
		"Authentication-Results": {"spf=pass dkim=pass"},
	}))
	if !s.SPFDKIMPresent {
		t.Error("SPFDKIMPresent = false, want true")
	}
}

func TestDeriveSignalsPlainMessageHasNoSignals(t *testing.T) {
	m := &emlx.Message{Headers: mail.Header{}, From: "Alice <alice@example.com>"}
	s := DeriveSignals(m)
	if s.IsBulk || s.IsAutomated || s.HasUnsubscribe || s.ReplyToDiffers || s.SPFDKIMPresent {
		t.Errorf("signals = %+v, want all false for a plain personal message", s)
	}
}
