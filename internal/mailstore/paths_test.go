package mailstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// makeMailDir builds a fake Mail directory containing the given version
// directories, each with an Envelope Index file.
func makeMailDir(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		md := filepath.Join(root, v, "MailData")
		if err := os.MkdirAll(md, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(md, "Envelope Index")
		if err := os.WriteFile(f, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDiscoverPathsPicksHighestVersion(t *testing.T) {
	root := makeMailDir(t, "V10", "V11", "V12")
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 12 {
		t.Errorf("Version = %d, want 12", p.Version)
	}
	if filepath.Base(p.VersionDir) != "V12" {
		t.Errorf("VersionDir = %q, want basename V12", p.VersionDir)
	}
	if filepath.Base(p.IndexPath) != "Envelope Index" {
		t.Errorf("IndexPath = %q, want basename 'Envelope Index'", p.IndexPath)
	}
}

func TestDiscoverPathsSingleVersion(t *testing.T) {
	root := makeMailDir(t, "V10")
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 10 {
		t.Errorf("Version = %d, want 10", p.Version)
	}
}

func TestDiscoverPathsIgnoresVersionsWithoutIndex(t *testing.T) {
	root := makeMailDir(t, "V10")
	// V12 exists but has no Envelope Index — must not be selected.
	if err := os.MkdirAll(filepath.Join(root, "V12", "MailData"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Version != 10 {
		t.Errorf("Version = %d, want 10 (V12 has no index)", p.Version)
	}
}

func TestDiscoverPathsNoMailDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := DiscoverPaths(missing)
	if !errors.Is(err, ErrNoMailDir) {
		t.Errorf("err = %v, want ErrNoMailDir", err)
	}
}

func TestDiscoverPathsNoVersionDir(t *testing.T) {
	root := t.TempDir() // exists but is empty
	_, err := DiscoverPaths(root)
	if !errors.Is(err, ErrNoVersionDir) {
		t.Errorf("err = %v, want ErrNoVersionDir", err)
	}
}

func TestDiscoverPathsNoPermission(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
	_, err := DiscoverPaths(root)
	if !errors.Is(err, ErrNoPermission) {
		t.Errorf("err = %v, want ErrNoPermission", err)
	}
}
