package emlx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/ianaindex"
)

// Attachment describes one attached part.
type Attachment struct {
	Name string
	MIME string
	Size int
}

// Message is a decoded email: headers, readable text, and attachments.
type Message struct {
	Headers     mail.Header
	Subject     string
	From        string
	To          string
	MessageID   string
	Text        string // always plain text, never markup
	TextSource  string // "text/plain", "text/html", optionally "; charset-fallback=…"
	HTML        string // original markup, when present
	Attachments []Attachment

	// htmlFallback remembers the charset fallback of the HTML part until
	// TextSource is set, in case HTML is the only body present.
	htmlFallback string
}

// decoder handles RFC 2047 encoded-words with a tolerant charset reader.
var decoder = mime.WordDecoder{
	CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		r, _, err := decodeCharset(charset, input)
		return r, err
	},
}

// decodeCharset converts a labelled charset to UTF-8, reporting whether it
// had to fall back.
//
// Declared labels are resolved through the IANA registry, which covers the
// ISO-8859 family, KOI8-R, Shift_JIS, GB2312, EUC-KR, and Windows-125x.
// Only an unknown label or a failed decode falls back: UTF-8 when the bytes
// are already valid, then latin-1, which maps every byte and never fails.
//
// fallback is non-empty when the declared charset was not used, so callers
// can declare it rather than silently emitting mojibake.
func decodeCharset(charset string, r io.Reader) (out io.Reader, fallback string, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}

	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		if utf8.Valid(raw) {
			return bytes.NewReader(raw), "", nil
		}
		return strings.NewReader(latin1ToUTF8(raw)), "latin-1", nil
	}

	// Resolve the declared label through the IANA registry.
	if enc, encErr := ianaindex.MIME.Encoding(charset); encErr == nil && enc != nil {
		decoded, decErr := enc.NewDecoder().Bytes(raw)
		if decErr == nil {
			return bytes.NewReader(decoded), "", nil
		}
	}

	// Unknown label or failed decode: UTF-8 if it already is, else latin-1.
	if utf8.Valid(raw) {
		return bytes.NewReader(raw), "utf-8", nil
	}
	return strings.NewReader(latin1ToUTF8(raw)), "latin-1", nil
}

// latin1ToUTF8 widens every byte to its matching rune. Cannot fail.
func latin1ToUTF8(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// sourceLabel renders a media type plus any charset fallback that fired.
func sourceLabel(mediaType, fallback string) string {
	if fallback == "" {
		return mediaType
	}
	return mediaType + "; charset-fallback=" + fallback
}

func decodeHeader(v string) string {
	if v == "" {
		return ""
	}
	out, err := decoder.DecodeHeader(v)
	if err != nil {
		return v // an undecodable header is still better than nothing
	}
	return out
}

// Extract decodes a parsed .emlx into headers, text, and attachments.
func Extract(f *File) (*Message, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(f.MIME))
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	m := &Message{
		Headers:   msg.Header,
		Subject:   decodeHeader(msg.Header.Get("Subject")),
		From:      decodeHeader(msg.Header.Get("From")),
		To:        decodeHeader(msg.Header.Get("To")),
		MessageID: strings.TrimSpace(msg.Header.Get("Message-ID")),
	}

	ctype := msg.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		if err := extractMultipart(m, msg.Body, params["boundary"]); err != nil {
			return nil, err
		}
	} else {
		body, fallback, err := decodePart(msg.Body,
			msg.Header.Get("Content-Transfer-Encoding"), params["charset"])
		if err != nil {
			return nil, err
		}
		assignBody(m, mediaType, body, fallback)
	}

	if m.Text == "" && m.HTML != "" {
		m.Text = HTMLToText(m.HTML)
		m.TextSource = sourceLabel("text/html", m.htmlFallback)
	}
	return m, nil
}

// extractMultipart walks the parts, preferring text/plain for the body and
// collecting anything with a filename as an attachment.
func extractMultipart(m *Message, body io.Reader, boundary string) error {
	if boundary == "" {
		return fmt.Errorf("multipart message with no boundary")
	}
	mr := multipart.NewReader(body, boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read part: %w", err)
		}

		partType, partParams, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			partType = "application/octet-stream"
			partParams = map[string]string{}
		}

		filename := part.FileName()
		if filename == "" {
			filename = partParams["name"]
		}
		if filename != "" {
			raw, _ := io.ReadAll(part)
			// multipart transparently decodes quoted-printable but NOT
			// base64, so decode it here — otherwise Size reports the
			// encoded length, roughly a third larger than the real file.
			if strings.EqualFold(
				strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")), "base64") {
				if decoded, err := base64.StdEncoding.DecodeString(
					strings.Join(strings.Fields(string(raw)), "")); err == nil {
					raw = decoded
				}
			}
			m.Attachments = append(m.Attachments, Attachment{
				Name: decodeHeader(filename),
				MIME: partType,
				Size: len(raw),
			})
			part.Close()
			continue
		}

		if strings.HasPrefix(partType, "multipart/") {
			if err := extractMultipart(m, part, partParams["boundary"]); err != nil {
				return err
			}
			part.Close()
			continue
		}

		decoded, fallback, err := decodePart(part,
			part.Header.Get("Content-Transfer-Encoding"), partParams["charset"])
		part.Close()
		if err != nil {
			continue // one bad part must not lose the whole message
		}
		assignBody(m, partType, decoded, fallback)
	}
}

// assignBody records a decoded part, preferring plain text for m.Text.
// Any charset fallback is carried into TextSource so it is never silent.
func assignBody(m *Message, mediaType, body, fallback string) {
	switch {
	case strings.HasPrefix(mediaType, "text/plain"):
		if m.Text == "" {
			m.Text = body
			m.TextSource = sourceLabel("text/plain", fallback)
		}
	case strings.HasPrefix(mediaType, "text/html"):
		if m.HTML == "" {
			m.HTML = body
			m.htmlFallback = fallback
		}
	}
}

// decodePart applies the transfer encoding, then the charset. The returned
// fallback is non-empty when the declared charset could not be used.
func decodePart(r io.Reader, encoding, charset string) (text, fallback string, err error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	converted, fallback, err := decodeCharset(charset, r)
	if err != nil {
		return "", "", err
	}
	out, err := io.ReadAll(converted)
	if err != nil {
		return "", "", err
	}
	return string(out), fallback, nil
}

var whitespaceRuns = regexp.MustCompile(`\n{3,}`)

// HTMLToText renders markup as readable plain text: tags removed, entities
// decoded, script and style content dropped, whitespace collapsed.
func HTMLToText(markup string) string {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return markup
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head":
				return
			case "br", "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			}
		}
	}
	walk(doc)

	lines := strings.Split(sb.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	joined := strings.Join(lines, "\n")
	return strings.TrimSpace(whitespaceRuns.ReplaceAllString(joined, "\n\n"))
}
