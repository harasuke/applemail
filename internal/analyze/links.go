// Package analyze derives structured signals from decoded messages.
package analyze

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/mirko/applemail/internal/emlx"
)

// LinkClass says whether a link is worth opening.
type LinkClass string

const (
	// ClassContent resolves to a real page — what an LLM should read.
	ClassContent LinkClass = "content"
	// ClassTracking is a pixel, beacon, or analytics redirect: noise.
	ClassTracking LinkClass = "tracking"
	// ClassAction performs something (unsubscribe, confirm, one-click)
	// and must never be opened automatically.
	ClassAction LinkClass = "action"
)

// Link is one extracted URL with everything needed to decide about it.
type Link struct {
	URLCanonical   string
	URLOriginal    string
	Domain         string
	AnchorText     string
	DedupKey       string
	Class          LinkClass
	AnchorMismatch bool
}

// trackingParams are query parameters that identify the recipient or the
// campaign rather than the content. Stripping them lets the same target
// arriving in different emails collapse to one entry.
var trackingParams = map[string]bool{
	"trk": true, "trkemail": true, "midtoken": true, "eid": true,
	"lipi": true, "refid": true, "li_fat_id": true,
	"ecid": true, "mkt_tok": true, "_hsenc": true, "_hsmi": true,
}

// redirectParams hold a wrapped destination URL in the query string.
var redirectParams = []string{"url", "u", "redirect", "target", "dest", "link"}

// trackingHosts serve beacons rather than pages.
var trackingHosts = []string{
	"track.", "click.", "email.", "links.", "beacon.", "px.", "open.",
}

// actionPathHints mark links that perform an action.
var actionPathHints = []string{
	"unsubscribe", "unsub", "optout", "opt-out", "confirm", "verify",
	"one-click", "oneclick", "remove",
}

var linkedInJobRe = regexp.MustCompile(`/jobs/view/(\d+)`)
var plainURLRe = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
var bareDomainRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+\.)+[a-z]{2,}\b`)

// Normalize strips tracking parameters, unwraps offline-recoverable
// redirects, and derives a stable dedup key.
//
// No network request is ever made: unwrapping reads the destination out of
// the query string, it never follows a redirect.
func Normalize(rawURL string) (canonical string, dedupKey string) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return rawURL, rawURL
	}

	// Unwrap a wrapped destination if one is present.
	q := u.Query()
	for _, p := range redirectParams {
		if v := q.Get(p); v != "" {
			if inner, err := url.Parse(v); err == nil && inner.Scheme != "" && inner.Host != "" {
				return Normalize(v)
			}
		}
	}

	for key := range q {
		lower := strings.ToLower(key)
		if trackingParams[lower] || strings.HasPrefix(lower, "utm_") {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	canonical = u.String()

	if strings.Contains(strings.ToLower(u.Host), "linkedin.com") {
		if m := linkedInJobRe.FindStringSubmatch(u.Path); m != nil {
			return canonical, "linkedin:job:" + m[1]
		}
	}
	return canonical, canonical
}

// Classify decides whether a link is content, tracking, or an action.
func Classify(rawURL, anchor string, inImage bool) LinkClass {
	if inImage {
		return ClassTracking
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ClassContent
	}
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)
	lowerAnchor := strings.ToLower(anchor)

	for _, hint := range actionPathHints {
		if strings.Contains(path, hint) || strings.Contains(lowerAnchor, hint) {
			return ClassAction
		}
	}
	for _, prefix := range trackingHosts {
		if strings.HasPrefix(host, prefix) {
			return ClassTracking
		}
	}
	if strings.HasSuffix(path, ".gif") || strings.HasSuffix(path, ".png") {
		return ClassTracking
	}
	return ClassContent
}

// ExtractLinks pulls every URL from a message, normalized, classified, and
// deduplicated by canonical key. HTML anchors carry their visible text;
// plain-text bodies contribute bare URLs.
func ExtractLinks(m *emlx.Message) []Link {
	seen := map[string]bool{}
	var out []Link

	add := func(rawURL, anchor string, inImage bool) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || !strings.HasPrefix(strings.ToLower(rawURL), "http") {
			return
		}
		canonical, key := Normalize(rawURL)
		class := Classify(canonical, anchor, inImage)

		dedup := string(class) + "|" + key
		if seen[dedup] {
			return
		}
		seen[dedup] = true

		domain := ""
		if u, err := url.Parse(canonical); err == nil {
			domain = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		}

		out = append(out, Link{
			URLCanonical:   canonical,
			URLOriginal:    rawURL,
			Domain:         domain,
			AnchorText:     strings.TrimSpace(anchor),
			DedupKey:       key,
			Class:          class,
			AnchorMismatch: anchorMismatch(canonical, anchor),
		})
	}

	if m.HTML != "" {
		walkHTMLLinks(m.HTML, add)
	}
	for _, u := range plainURLRe.FindAllString(m.Text, -1) {
		add(strings.TrimRight(u, ".,;:"), "", false)
	}
	return out
}

// walkHTMLLinks visits anchors and image sources in a document.
func walkHTMLLinks(markup string, add func(rawURL, anchor string, inImage bool)) {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "a":
				add(attr(n, "href"), nodeText(n), false)
			case "img":
				add(attr(n, "src"), attr(n, "alt"), true)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// anchorMismatch reports whether the visible text names a domain different
// from the link's real destination. This is a fact, not a verdict.
func anchorMismatch(rawURL, anchor string) bool {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	realHost := strings.TrimPrefix(strings.ToLower(u.Host), "www.")

	candidate := bareDomainRe.FindString(anchor)
	if candidate == "" {
		return false // anchor text names no domain at all
	}
	claimed := strings.TrimPrefix(strings.ToLower(candidate), "www.")
	return claimed != realHost &&
		!strings.HasSuffix(realHost, "."+claimed) &&
		!strings.HasSuffix(claimed, "."+realHost)
}
