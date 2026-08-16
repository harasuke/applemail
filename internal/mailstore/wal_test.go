package mailstore

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenReadsUncheckpointedWAL reproduces the stale-read bug: Mail writes
// the Envelope Index in WAL mode, and immutable=1 made SQLite skip the -wal
// file, hiding recent changes until a checkpoint. Opening with mode=ro alone
// must surface rows that exist only in the WAL.
func TestOpenReadsUncheckpointedWAL(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "Envelope Index")

	// A writer connection creates the DB in WAL mode and inserts a row
	// without checkpointing, leaving it only in the -wal file.
	w, err := sql.Open("sqlite", "file:"+indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec("CREATE TABLE messages (ROWID INTEGER PRIMARY KEY, subject TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec("INSERT INTO messages (ROWID, subject) VALUES (1, 'in wal')"); err != nil {
		t.Fatal(err)
	}

	// DiscoverPaths resolves the highest V* dir with an Envelope Index; we
	// lay out the expected structure.
	root := filepath.Join(dir, "root")
	vdir := filepath.Join(root, "V12", "MailData")
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Move the writer's DB into place (same file, so -wal/-shm siblings move too).
	for _, name := range []string{"Envelope Index", "Envelope Index-wal", "Envelope Index-shm"} {
		_ = os.Rename(filepath.Join(dir, name), filepath.Join(vdir, name))
	}

	paths, err := DiscoverPaths(root)
	if err != nil {
		t.Fatalf("DiscoverPaths: %v", err)
	}
	s, err := Open(paths)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var subject string
	if err := s.db.QueryRow("SELECT subject FROM messages WHERE ROWID = 1").Scan(&subject); err != nil {
		t.Fatalf("read uncheckpointed WAL row: %v", err)
	}
	if subject != "in wal" {
		t.Errorf("subject = %q, want %q (WAL row not surfaced)", subject, "in wal")
	}
}
