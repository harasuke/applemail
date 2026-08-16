package mailstore

import (
	"strings"
	"testing"
	"time"
)

func TestBuildWhereEmptyFilterExcludesDeleted(t *testing.T) {
	f := Filter{}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.deleted = 0") {
		t.Errorf("where = %q, want it to exclude deleted messages", where)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildWhereFrom(t *testing.T) {
	// --from matches either the address or the display name, so the
	// value is bound twice.
	f := Filter{From: "alice@example.com"}
	where, args := f.buildWhere()
	if !strings.Contains(where, "addr.address LIKE ?") {
		t.Errorf("where = %q, want a sender address clause", where)
	}
	if !strings.Contains(where, "addr.comment LIKE ?") {
		t.Errorf("where = %q, want a sender display-name clause", where)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want two (address and display name)", args)
	}
	if args[0] != "%alice@example.com%" || args[1] != "%alice@example.com%" {
		t.Errorf("args = %v, want both wildcarded", args)
	}
}

func TestBuildWhereSubjectAndUnread(t *testing.T) {
	f := Filter{Subject: "invoice", Unread: true}
	where, args := f.buildWhere()
	if !strings.Contains(where, "subj.subject LIKE ?") {
		t.Errorf("where = %q, want a subject clause", where)
	}
	if !strings.Contains(where, "m.read = 0") {
		t.Errorf("where = %q, want an unread clause", where)
	}
	if len(args) != 1 || args[0] != "%invoice%" {
		t.Errorf("args = %v, want one wildcarded subject", args)
	}
}

func TestBuildWhereDateRangeUsesEpochTimestamps(t *testing.T) {
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	f := Filter{Since: &since}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.date_received >= ?") {
		t.Errorf("where = %q, want a since clause", where)
	}
	if len(args) != 1 {
		t.Fatalf("args = %v, want one", args)
	}
	if args[0] != TimeToEpoch(since) {
		t.Errorf("args[0] = %v, want the epoch form %v", args[0], TimeToEpoch(since))
	}
}

func TestBuildWhereIncludeDeleted(t *testing.T) {
	f := Filter{IncludeDeleted: true}
	where, _ := f.buildWhere()
	if strings.Contains(where, "m.deleted = 0") {
		t.Errorf("where = %q, want no deleted-exclusion clause", where)
	}
}

func TestBuildWhereAccountMatchesMailboxURLAuthority(t *testing.T) {
	f := Filter{Account: "4F602833-0253-4FD2-BECD-D3BE468777DC"}
	where, args := f.buildWhere()
	if !strings.Contains(where, "mb.url LIKE '%://' || ? || '/%'") {
		t.Errorf("where = %q, want an account clause on the mailbox URL", where)
	}
	if len(args) != 1 || args[0] != "4F602833-0253-4FD2-BECD-D3BE468777DC" {
		t.Errorf("args = %v, want the account identifier", args)
	}
}

func TestBuildWhereROWIDIsAPrimaryKeyLookup(t *testing.T) {
	f := Filter{ROWID: 48213, IncludeDeleted: true}
	where, args := f.buildWhere()
	if !strings.Contains(where, "m.ROWID = ?") {
		t.Errorf("where = %q, want a ROWID clause", where)
	}
	if len(args) != 1 || args[0] != int64(48213) {
		t.Errorf("args = %v, want the ROWID bound once", args)
	}
}
