package analyze

import (
	"strings"
	"testing"

	"github.com/mirko/applemail/internal/emlx"
	"github.com/mirko/applemail/internal/testdata"
)

func TestNormalizeStripsTrackingParams(t *testing.T) {
	in := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123&eid=abc"
	canonical, _ := Normalize(in)
	for _, bad := range []string{"trk=", "midToken=", "eid="} {
		if strings.Contains(canonical, bad) {
			t.Errorf("canonical = %q, still contains %q", canonical, bad)
		}
	}
	if !strings.Contains(canonical, "/jobs/view/4021887364") {
		t.Errorf("canonical = %q, want the job path preserved", canonical)
	}
}

func TestNormalizeStripsUTMParams(t *testing.T) {
	in := "https://example.com/page?utm_source=news&utm_campaign=x&id=7"
	canonical, _ := Normalize(in)
	if strings.Contains(canonical, "utm_") {
		t.Errorf("canonical = %q, still contains utm params", canonical)
	}
	if !strings.Contains(canonical, "id=7") {
		t.Errorf("canonical = %q, want the real param kept", canonical)
	}
}

func TestNormalizeUnwrapsRedirect(t *testing.T) {
	in := "https://click.example.com/redirect?url=https%3A%2F%2Freal.example.org%2Farticle%2F9"
	canonical, _ := Normalize(in)
	if canonical != "https://real.example.org/article/9" {
		t.Errorf("canonical = %q, want the unwrapped destination", canonical)
	}
}

func TestNormalizeLinkedInJobsDedupKey(t *testing.T) {
	a := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-jobs&midToken=AQE123"
	b := "https://www.linkedin.com/comm/jobs/view/4021887364?trk=eml-digest&midToken=AQE999"

	_, keyA := Normalize(a)
	_, keyB := Normalize(b)

	if keyA != "linkedin:job:4021887364" {
		t.Errorf("keyA = %q, want linkedin:job:4021887364", keyA)
	}
	if keyA != keyB {
		t.Errorf("keyA = %q, keyB = %q — the same job must share a key", keyA, keyB)
	}
}

func TestNormalizeNonLinkedInKeyIsCanonicalURL(t *testing.T) {
	canonical, key := Normalize("https://example.com/a?utm_source=x")
	if key != canonical {
		t.Errorf("key = %q, want it to equal canonical %q", key, canonical)
	}
}

func TestNormalizeLinkedInHostCaseInsensitive(t *testing.T) {
	_, key := Normalize("https://WWW.LinkedIn.com/jobs/view/4021887364")
	if key != "linkedin:job:4021887364" {
		t.Errorf("key = %q, want linkedin:job:4021887364", key)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		anchor  string
		inImage bool
		want    LinkClass
	}{
		{"image beacon", "https://track.example.org/pixel.gif", "", true, ClassTracking},
		{"tracking host", "https://track.linkedin.com/px?e=1", "", false, ClassTracking},
		{"unsubscribe path", "https://example.org/unsub?u=42", "Unsubscribe", false, ClassAction},
		{"unsubscribe anchor", "https://example.org/x", "unsubscribe", false, ClassAction},
		{"real article", "https://example.org/article/9", "Read more", false, ClassContent},
		{"job posting", "https://www.linkedin.com/jobs/view/4021887364", "Engineer", false, ClassContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.url, tt.anchor, tt.inImage); got != tt.want {
				t.Errorf("Classify(%q, %q, %v) = %q, want %q",
					tt.url, tt.anchor, tt.inImage, got, tt.want)
			}
		})
	}
}

func TestAnchorMismatchDetected(t *testing.T) {
	links := extractFromHTML(t,
		`<a href="https://evil.example.net/login">https://bank.example.com/login</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if !links[0].AnchorMismatch {
		t.Error("AnchorMismatch = false, want true when anchor names another domain")
	}
}

func TestAnchorMismatchNotFlaggedForPlainText(t *testing.T) {
	links := extractFromHTML(t, `<a href="https://example.org/x">Read more</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].AnchorMismatch {
		t.Error("AnchorMismatch = true, want false for non-URL anchor text")
	}
}

func TestExtractLinksFromPlainTextBody(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := emlx.ParseFile(testdata.EmlxPath(root, msgs[0]))
	if err != nil {
		t.Fatal(err)
	}
	m, err := emlx.Extract(f)
	if err != nil {
		t.Fatal(err)
	}

	links := ExtractLinks(m)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].URLCanonical != "https://example.com/docs" {
		t.Errorf("URLCanonical = %q", links[0].URLCanonical)
	}
	if links[0].Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", links[0].Domain)
	}
}

func TestExtractLinksDedupsLinkedInJobs(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)
	f, err := emlx.ParseFile(testdata.EmlxPath(root, msgs[4])) // the LinkedIn fixture
	if err != nil {
		t.Fatal(err)
	}
	m, err := emlx.Extract(f)
	if err != nil {
		t.Fatal(err)
	}

	links := ExtractLinks(m)

	var content []Link
	for _, l := range links {
		if l.Class == ClassContent {
			content = append(content, l)
		}
	}
	// Two distinct jobs: 4021887364 (listed twice) and 4055512233.
	if len(content) != 2 {
		t.Fatalf("got %d content links, want 2 after dedup: %+v", len(content), content)
	}

	keys := map[string]bool{}
	for _, l := range content {
		keys[l.DedupKey] = true
	}
	if !keys["linkedin:job:4021887364"] || !keys["linkedin:job:4055512233"] {
		t.Errorf("dedup keys = %v, want both job keys", keys)
	}
}

func TestExtractLinksKeepsOriginalURL(t *testing.T) {
	links := extractFromHTML(t,
		`<a href="https://www.linkedin.com/comm/jobs/view/1?trk=eml">Job</a>`)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if !strings.Contains(links[0].URLOriginal, "trk=eml") {
		t.Errorf("URLOriginal = %q, want the tracked original preserved", links[0].URLOriginal)
	}
}

// extractFromHTML builds a minimal HTML message and extracts its links.
func extractFromHTML(t *testing.T, body string) []Link {
	t.Helper()
	m := &emlx.Message{
		HTML:       "<html><body>" + body + "</body></html>",
		TextSource: "text/html",
	}
	return ExtractLinks(m)
}
