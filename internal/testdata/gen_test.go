package testdata

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBuildMailDirCreatesReadableIndex(t *testing.T) {
	root := BuildMailDir(t, DefaultMessages())

	index := filepath.Join(root, "V12", "MailData", "Envelope Index")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("Envelope Index not created: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+index+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != len(DefaultMessages()) {
		t.Errorf("message count = %d, want %d", count, len(DefaultMessages()))
	}
}

func TestBuildMailDirCreatesEmlxFiles(t *testing.T) {
	msgs := DefaultMessages()
	root := BuildMailDir(t, msgs)

	var found int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".emlx") {
			found++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(msgs) {
		t.Errorf("found %d .emlx files, want %d", found, len(msgs))
	}
}

func TestEmlxFileHasValidByteCountPrefix(t *testing.T) {
	msgs := DefaultMessages()
	root := BuildMailDir(t, msgs)

	path := EmlxPath(root, msgs[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	nl := strings.IndexByte(string(raw), '\n')
	if nl < 0 {
		t.Fatal("no newline terminating the byte count line")
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw[:nl])))
	if err != nil {
		t.Fatalf("byte count line %q is not a number: %v", raw[:nl], err)
	}
	if n != len(msgs[0].RawMIME) {
		t.Errorf("byte count = %d, want %d", n, len(msgs[0].RawMIME))
	}
	body := string(raw[nl+1 : nl+1+n])
	if body != msgs[0].RawMIME {
		t.Error("MIME content does not match the declared byte count region")
	}
	if !strings.Contains(string(raw[nl+1+n:]), "<?xml") {
		t.Error("plist trailer missing after the MIME region")
	}
}
