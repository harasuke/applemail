package mailstore

import (
	"path/filepath"
	"testing"

	"github.com/mirko/applemail/internal/testdata"
)

func newFixturePathIndex(t *testing.T) (*PathIndex, string) {
	t.Helper()
	root := testdata.BuildMailDir(t, testdata.DefaultMessages())
	paths, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("DiscoverPaths: %v", err)
	}
	idx, err := NewPathIndex(paths)
	if err != nil {
		t.Fatalf("NewPathIndex: %v", err)
	}
	return idx, root
}

func TestPathIndexResolvesKnownMessage(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	got, ok := idx.Resolve(1)
	if !ok {
		t.Fatal("Resolve(1) = not found, want found")
	}
	if filepath.Base(got) != "1.emlx" {
		t.Errorf("Resolve(1) = %q, want basename 1.emlx", got)
	}
}

func TestPathIndexResolvesAcrossMailboxes(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	// ROWID 3 lives in Archive.mbox, not INBOX.mbox.
	got, ok := idx.Resolve(3)
	if !ok {
		t.Fatal("Resolve(3) = not found, want found")
	}
	if filepath.Base(got) != "3.emlx" {
		t.Errorf("Resolve(3) = %q, want basename 3.emlx", got)
	}
}

func TestPathIndexMissingMessage(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	if _, ok := idx.Resolve(99999); ok {
		t.Error("Resolve(99999) = found, want not found")
	}
}

func TestPathIndexCollectsMessageDirs(t *testing.T) {
	idx, _ := newFixturePathIndex(t)
	// The fixture creates INBOX.mbox and Archive.mbox.
	if n := idx.DirCount(); n != 2 {
		t.Errorf("DirCount() = %d, want 2", n)
	}
}
