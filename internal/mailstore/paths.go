package mailstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrNoPermission means macOS TCC denied access to the Mail directory.
	// Full Disk Access must be granted to the terminal application.
	ErrNoPermission = errors.New("permission denied reading Mail directory")
	// ErrNoMailDir means the Mail directory does not exist at all.
	ErrNoMailDir = errors.New("Mail directory not found")
	// ErrNoVersionDir means no V<N> directory with an Envelope Index was found.
	ErrNoVersionDir = errors.New("no Mail version directory with an Envelope Index")
)

// Paths holds the resolved locations of Apple Mail's on-disk data.
type Paths struct {
	Root       string // ~/Library/Mail
	VersionDir string // ~/Library/Mail/V12
	IndexPath  string // ~/Library/Mail/V12/MailData/Envelope Index
	Version    int    // 12
}

// DefaultRoot returns the standard Mail directory for the current user.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Mail"), nil
}

// DiscoverPaths locates the Mail data directory and the highest-numbered
// version directory that actually contains an Envelope Index. Passing an
// empty root uses the current user's default Mail directory.
//
// The version directory number varies across macOS releases, so it is
// always discovered rather than assumed.
func DiscoverPaths(root string) (*Paths, error) {
	if root == "" {
		var err error
		if root, err = DefaultRoot(); err != nil {
			return nil, err
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrPermission):
			return nil, fmt.Errorf("%w: %s", ErrNoPermission, root)
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%w: %s", ErrNoMailDir, root)
		default:
			return nil, err
		}
	}

	type candidate struct {
		version int
		dir     string
		index   string
	}
	var found []candidate
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "V") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "V"))
		if err != nil {
			continue
		}
		dir := filepath.Join(root, e.Name())
		index := filepath.Join(dir, "MailData", "Envelope Index")
		if _, err := os.Stat(index); err != nil {
			continue // a version directory without an index is unusable
		}
		found = append(found, candidate{n, dir, index})
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoVersionDir, root)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].version > found[j].version })

	best := found[0]
	return &Paths{
		Root:       root,
		VersionDir: best.dir,
		IndexPath:  best.index,
		Version:    best.version,
	}, nil
}
