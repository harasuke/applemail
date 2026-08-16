package analyze

import (
	"net/mail"
	"strings"

	"github.com/mirko/applemail/internal/emlx"
)

// Signals are facts read from message headers — never judgments.
//
// There is deliberately no IsSpam or IsInteresting field: that assessment
// belongs to whoever consumes this output, and asserting it here would
// claim a certainty the headers do not provide.
type Signals struct {
	IsBulk         bool
	IsAutomated    bool
	HasUnsubscribe bool
	ReplyToDiffers bool
	SPFDKIMPresent bool
}

// DeriveSignals reads header facts from a decoded message.
func DeriveSignals(m *emlx.Message) Signals {
	get := func(name string) string {
		if m.Headers == nil {
			return ""
		}
		return strings.TrimSpace(m.Headers.Get(name))
	}

	var s Signals

	s.HasUnsubscribe = get("List-Unsubscribe") != ""
	precedence := strings.ToLower(get("Precedence"))
	s.IsBulk = s.HasUnsubscribe ||
		precedence == "bulk" || precedence == "list" || precedence == "junk" ||
		get("List-Id") != ""

	autoSubmitted := strings.ToLower(get("Auto-Submitted"))
	s.IsAutomated = (autoSubmitted != "" && autoSubmitted != "no") ||
		get("X-Auto-Response-Suppress") != "" ||
		containsNoReply(m.From)

	if replyTo := get("Reply-To"); replyTo != "" {
		s.ReplyToDiffers = domainOf(replyTo) != "" &&
			domainOf(replyTo) != domainOf(m.From)
	}

	s.SPFDKIMPresent = get("Authentication-Results") != "" ||
		get("DKIM-Signature") != "" ||
		get("Received-SPF") != ""

	return s
}

func containsNoReply(from string) bool {
	lower := strings.ToLower(from)
	for _, hint := range []string{"noreply", "no-reply", "donotreply", "do-not-reply"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// domainOf returns the domain of the first address in a header value.
func domainOf(headerValue string) string {
	addr, err := mail.ParseAddress(strings.TrimSpace(headerValue))
	if err != nil {
		if at := strings.LastIndex(headerValue, "@"); at >= 0 {
			return strings.ToLower(strings.Trim(headerValue[at+1:], "> "))
		}
		return ""
	}
	if at := strings.LastIndex(addr.Address, "@"); at >= 0 {
		return strings.ToLower(addr.Address[at+1:])
	}
	return ""
}
