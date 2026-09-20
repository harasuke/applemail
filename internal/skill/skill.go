// Package skill embeds the mail agent skill so the binary can install it
// into every agent harness (Claude Code, opencode, Codex, Gemini CLI)
// without needing the source repo.
package skill

import _ "embed"

//go:embed SKILL.md
var doc string

// Markdown returns the full skill document.
func Markdown() string { return doc }
