// Package emlx parses Apple Mail's .emlx message files.
//
// An .emlx file has three parts: a decimal byte count terminated by a
// newline, exactly that many bytes of RFC-822 MIME content, and an Apple
// plist trailer holding Mail's own metadata.
package emlx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ErrBadByteCount means the leading byte-count line was missing or unparseable.
var ErrBadByteCount = errors.New("malformed .emlx byte count line")

// File is a parsed .emlx.
type File struct {
	ByteCount int    // as declared on the first line
	MIME      []byte // the RFC-822 message
	Plist     []byte // Apple's metadata trailer
	// Truncated is true when the declared byte count overruns the file, so
	// the MIME content ends at EOF and no plist trailer survives.
	Truncated bool
}

// Parse splits raw .emlx bytes into its three parts.
//
// The declared byte count decides where MIME ends. Some real files declare
// more bytes than they contain, so the count is clamped to the available
// data rather than treated as fatal.
func Parse(raw []byte) (*File, error) {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, fmt.Errorf("%w: no newline in %d bytes", ErrBadByteCount, len(raw))
	}

	countStr := string(bytes.TrimSpace(raw[:nl]))
	count, err := strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return nil, fmt.Errorf("%w: %q", ErrBadByteCount, countStr)
	}

	rest := raw[nl+1:]
	end := count
	truncated := false
	if end > len(rest) {
		end = len(rest) // clamp: never index past the end
		truncated = true
	}

	return &File{
		ByteCount: count,
		MIME:      rest[:end],
		Plist:     rest[end:],
		Truncated: truncated,
	}, nil
}

// ParseFile reads and parses an .emlx file from disk.
func ParseFile(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}
