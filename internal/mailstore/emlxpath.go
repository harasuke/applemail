package mailstore

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PathIndex maps message ROWIDs to their .emlx files on disk.
//
// The filesystem is walked once at construction to collect the Messages
// directories — typically 10-50 of them regardless of how many messages
// exist — so that each lookup is a handful of stat calls rather than a
// recursive search.
type PathIndex struct {
	dirs []string
}

// NewPathIndex walks the Mail version directory collecting message dirs.
func NewPathIndex(paths *Paths) (*PathIndex, error) {
	var dirs []string
	err := filepath.WalkDir(paths.VersionDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree must not abort the whole walk.
			if os.IsPermission(err) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() && d.Name() == "Messages" {
			dirs = append(dirs, path)
			return fs.SkipDir // no message dirs nest inside another
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk mail directory: %w", err)
	}
	return &PathIndex{dirs: dirs}, nil
}

// DirCount reports how many Messages directories were found.
func (p *PathIndex) DirCount() int { return len(p.dirs) }

// Resolve returns the .emlx path for a ROWID. Mail writes a .partial.emlx
// when only the headers were downloaded, so both forms are tried.
func (p *PathIndex) Resolve(rowid int64) (string, bool) {
	names := []string{
		fmt.Sprintf("%d.emlx", rowid),
		fmt.Sprintf("%d.partial.emlx", rowid),
	}
	for _, dir := range p.dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, true
			}
		}
	}
	return "", false
}
