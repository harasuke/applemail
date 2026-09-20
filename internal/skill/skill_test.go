package skill

import (
	"strings"
	"testing"
)

// TestMarkdownHasFrontmatter guards the skill against drift: every harness
// requires YAML frontmatter with name and description, or the skill is
// silently ignored.
func TestMarkdownHasFrontmatter(t *testing.T) {
	doc := Markdown()
	for _, want := range []string{"name: mail", "description:"} {
		if !strings.Contains(doc, want) {
			t.Errorf("skill is missing frontmatter field %q", want)
		}
	}
}

// TestMarkdownHasSafetyRules pins the red lines that make the skill safe to
// hand to an agent. Removing any of these is a bug, not a wording choice.
func TestMarkdownHasSafetyRules(t *testing.T) {
	doc := Markdown()
	for _, want := range []string{
		"mail trash --dry-run",
		"Never open `action` links",
		"no network requests",
		"mail doctor",
		"mail reference",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("skill is missing safety/usage text %q", want)
		}
	}
}
