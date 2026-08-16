// Package reference embeds the tool's full reference documentation so the
// binary is self-describing: an agent driving `mail` can run `mail
// reference` and get the complete guide without needing the source repo.
package reference

import _ "embed"

//go:embed reference.md
var doc string

// Markdown returns the full reference document.
func Markdown() string { return doc }
