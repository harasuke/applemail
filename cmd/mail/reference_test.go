package main

import (
	"strings"
	"testing"
)

func TestReferenceCommandPrintsEmbeddedDoc(t *testing.T) {
	out := runCommand(t, "reference")
	for _, want := range []string{
		"# mail — Reference",
		"mail trash",
		"Exit codes",
		"Gotchas",
		"Recipes for LLM agents",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reference output is missing %q", want)
		}
	}
}
