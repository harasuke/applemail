package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirko/applemail/internal/mailctl"
	"github.com/mirko/applemail/internal/mailstore"
)

func TestExitCodeForAutomationError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", mailctl.ErrAutomationDenied)
	if got := exitCodeFor(err); got != 3 {
		t.Errorf("exitCodeFor(ErrAutomationDenied) = %d, want 3", got)
	}
}

func TestAutomationMessageNamesTerminalAndSettingsPath(t *testing.T) {
	msg := automationMessage()
	if !strings.Contains(msg, "Automation") {
		t.Error("message does not mention Automation")
	}
	if !strings.Contains(strings.ToLower(msg), "terminal") {
		t.Error("message does not tell the user to grant access to their terminal")
	}
	if !strings.Contains(msg, "Privacy & Security") {
		t.Error("message does not give the System Settings path")
	}
}

func TestExitCodeForPermissionError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", mailstore.ErrNoPermission)
	if got := exitCodeFor(err); got != 2 {
		t.Errorf("exitCodeFor(ErrNoPermission) = %d, want 2", got)
	}
}

func TestExitCodeForGeneralError(t *testing.T) {
	if got := exitCodeFor(errors.New("something broke")); got != 1 {
		t.Errorf("exitCodeFor(generic) = %d, want 1", got)
	}
}

func TestExitCodeForNil(t *testing.T) {
	if got := exitCodeFor(nil); got != 0 {
		t.Errorf("exitCodeFor(nil) = %d, want 0", got)
	}
}

func TestPermissionMessageNamesTerminalNotBinary(t *testing.T) {
	msg := permissionMessage("/Users/someone/Library/Mail")

	if !strings.Contains(msg, "Full Disk Access") {
		t.Error("message does not mention Full Disk Access")
	}
	if !strings.Contains(strings.ToLower(msg), "terminal") {
		t.Error("message does not tell the user to grant access to their terminal")
	}
	if !strings.Contains(msg, "Privacy & Security") {
		t.Error("message does not give the System Settings path")
	}
	if !strings.Contains(strings.ToLower(msg), "restart") {
		t.Error("message does not say the terminal must be restarted")
	}
}

func TestParseSinceRelativeDays(t *testing.T) {
	got, err := parseSince("30d")
	if err != nil {
		t.Fatalf("parseSince(30d): %v", err)
	}
	if got.IsZero() {
		t.Error("parseSince(30d) returned the zero time")
	}
}

func TestParseSinceAbsoluteDate(t *testing.T) {
	got, err := parseSince("2026-08-01")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 8 || got.Day() != 1 {
		t.Errorf("parseSince(2026-08-01) = %v, want that date", got)
	}
}

func TestParseSinceRejectsGarbage(t *testing.T) {
	if _, err := parseSince("last tuesday"); err == nil {
		t.Error("parseSince(\"last tuesday\") = nil error, want a rejection")
	}
}

func TestBuildFilterUntilAbsoluteDateIsDayInclusive(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)
	flagUntil = "2026-08-01"

	f, err := buildFilter(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if f.Until == nil {
		t.Fatal("Until is nil, want an inclusive boundary")
	}
	want := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if !f.Until.Equal(want) {
		t.Errorf("Until = %v, want %v (next midnight)", *f.Until, want)
	}
}

func TestBuildFilterUntilRelativeSpanNotShifted(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)
	flagUntil = "30d"
	now := time.Now()

	f, err := buildFilter(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if f.Until == nil {
		t.Fatal("Until is nil")
	}
	want := now.AddDate(0, 0, -30)
	if diff := f.Until.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("Until = %v, want roughly %v (30d back, not shifted by a day)", *f.Until, want)
	}
}

func fakeAccounts() {
	listAccounts = func() ([]mailctl.Account, error) {
		return []mailctl.Account{
			{Name: "Omnys", ID: "4F602833", Emails: []string{"mirko.spinato@omnys.com"}},
			{Name: "DevPunks", ID: "2CDA1730", Emails: []string{"mirko.spinato@devpunks.com"}},
		}, nil
	}
}

func TestResolveAccountUUIDByExactEmail(t *testing.T) {
	orig := listAccounts
	defer func() { listAccounts = orig }()
	fakeAccounts()

	got, err := resolveAccountUUID("mirko.spinato@omnys.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "4F602833" {
		t.Errorf("got %q, want the Omnys id", got)
	}
}

func TestResolveAccountUUIDBySubstring(t *testing.T) {
	orig := listAccounts
	defer func() { listAccounts = orig }()
	fakeAccounts()

	got, err := resolveAccountUUID("devpunks")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2CDA1730" {
		t.Errorf("got %q, want the DevPunks id", got)
	}
}

func TestResolveAccountUUIDByID(t *testing.T) {
	orig := listAccounts
	defer func() { listAccounts = orig }()
	fakeAccounts()

	got, err := resolveAccountUUID("4F602833")
	if err != nil {
		t.Fatal(err)
	}
	if got != "4F602833" {
		t.Errorf("got %q, want the id passed through", got)
	}
}

func TestResolveAccountUUIDNoMatch(t *testing.T) {
	orig := listAccounts
	defer func() { listAccounts = orig }()
	fakeAccounts()

	if _, err := resolveAccountUUID("nope@example.com"); err == nil {
		t.Error("resolveAccountUUID(nope) = nil error, want a no-match error")
	}
}

func TestBuildFilterResolvesAccountFlag(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)
	orig := listAccounts
	defer func() { listAccounts = orig }()
	fakeAccounts()
	flagAccount = "mirko.spinato@omnys.com"

	f, err := buildFilter(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if f.Account != "4F602833" {
		t.Errorf("filter.Account = %q, want the resolved id", f.Account)
	}
}
