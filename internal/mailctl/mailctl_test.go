package mailctl

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildScriptContainsTrashMechanics(t *testing.T) {
	s := buildScript()
	for _, want := range []string{
		"on run argv",
		"message id",
		"delete eachMessage",
		"every mailbox",
		"isTrashMailbox",
		"my isTrashMailbox",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("buildScript() is missing %q:\n%s", want, s)
		}
	}
}

func TestBuildScriptSkipsTrashMailboxesByName(t *testing.T) {
	s := buildScript()
	// Mail's `deleted status` reports false even for trashed messages, so the
	// script must skip trash/junk mailboxes by name — otherwise a re-run
	// would `delete` already-trashed messages, which is permanent in Mail.
	for _, name := range []string{
		"Deleted Messages", "Cestino", "Posta eliminata", "Trash", "Junk", "Spam", "Posta indesiderata",
	} {
		if !strings.Contains(s, name) {
			t.Errorf("buildScript() must skip the trash mailbox %q:\n%s", name, s)
		}
	}
	// The obsolete, unreliable `deleted status` guard must be gone.
	if strings.Contains(s, "deleted status") {
		t.Errorf("buildScript() must not rely on `deleted status` (always false in Mail):\n%s", s)
	}
}

func TestBuildScriptMatchesByManualLoopNotWhoseClause(t *testing.T) {
	s := buildScript()
	// Mail's AppleScript dictionary cannot evaluate `whose message id is in
	// <list>` — it sends the list as a specifier and fails with -1700. The
	// matching must be a client-side repeat loop comparing each message's
	// message id against the argv list.
	if strings.Contains(s, "whose") {
		t.Errorf("buildScript() must not use a `whose ... is in` clause (Mail rejects it with -1700):\n%s", s)
	}
	if !strings.Contains(s, "set msgs to (get every message of eachMailbox)") {
		t.Errorf("buildScript() must materialize the message list with `get`:\n%s", s)
	}
	if !strings.Contains(s, "(message id of eachMessage) is in msgIDs") {
		t.Errorf("buildScript() must compare each message id against the list:\n%s", s)
	}
	// No recursive handler: passing a mailbox reference through `my` re-enters
	// Mail's tell context and fails with -1728.
	if strings.Contains(s, "my matchingMessages") {
		t.Errorf("buildScript() must not recurse via a handler (Mail fails with -1728):\n%s", s)
	}
}

func TestBuildScriptPassesIDsAsArgumentsNotInterpolated(t *testing.T) {
	s := buildScript()
	// IDs must arrive via argv, never be baked into the source, so the
	// script is identical for every call and safe from injection.
	if strings.Contains(s, "<fixture-") {
		t.Errorf("buildScript() should not hardcode any message id:\n%s", s)
	}
	if !strings.Contains(s, "msgIDs") || !strings.Contains(s, "argv") {
		t.Errorf("buildScript() must reference argv:\n%s", s)
	}
}

func TestClassifyAutomationDenied(t *testing.T) {
	err := classify("execution error: Not authorized to send Apple events to Mail. (-1743)", errors.New("exit status 1"))
	if !errors.Is(err, ErrAutomationDenied) {
		t.Errorf("classify(-1743) = %v, want ErrAutomationDenied", err)
	}
}

func TestClassifyGenericErrorKeepsMessage(t *testing.T) {
	err := classify("execution error: Some other failure. (-1)", errors.New("exit status 1"))
	if errors.Is(err, ErrAutomationDenied) {
		t.Errorf("classify(generic) = %v, want a non-automation error", err)
	}
	if !strings.Contains(err.Error(), "Some other failure") {
		t.Errorf("classify(generic) = %v, want the script stderr preserved", err)
	}
}

func TestClassifyNilRunErrorIsNil(t *testing.T) {
	if err := classify("", nil); err != nil {
		t.Errorf("classify(nil) = %v, want nil", err)
	}
}

func TestMoveToTrashEmptyListIsNoop(t *testing.T) {
	orig := runOsaScript
	runOsaScript = func(script string, args []string) (string, string, error) {
		t.Fatal("runOsaScript called for an empty id list")
		return "", "", nil
	}
	defer func() { runOsaScript = orig }()

	notFound, err := MoveToTrash(nil)
	if err != nil || notFound != 0 {
		t.Errorf("MoveToTrash(nil) = (%d, %v), want (0, nil)", notFound, err)
	}
}

func TestMoveToTrashReturnsNotFoundCount(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	var gotArgs []string
	runOsaScript = func(script string, args []string) (string, string, error) {
		gotArgs = append([]string{}, args...)
		return "3", "", nil // 3 of 5 matched
	}

	ids := []string{"<a@x>", "<b@x>", "<c@x>", "<d@x>", "<e@x>"}
	notFound, err := MoveToTrash(ids)
	if err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	if notFound != 2 {
		t.Errorf("notFound = %d, want 2", notFound)
	}
	if len(gotArgs) != 5 {
		t.Fatalf("got %d args, want 5", len(gotArgs))
	}
	// The angle brackets from the RFC header must be stripped: Mail's
	// AppleScript `message id` returns the ID without them.
	if gotArgs[0] != "a@x" {
		t.Errorf("first arg = %q, want the stripped id \"a@x\"", gotArgs[0])
	}
}

func TestNormalizeIDStripsAngleBrackets(t *testing.T) {
	for in, want := range map[string]string{
		"<a@x>":                  "a@x",
		"a@x":                    "a@x",
		"<CAF...@mail.gmail.com>": "CAF...@mail.gmail.com",
		"  <a@x>  ":              "a@x",
	} {
		if got := normalizeID(in); got != want {
			t.Errorf("normalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMoveToTrashSurfacesAutomationError(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	runOsaScript = func(script string, args []string) (string, string, error) {
		return "", "execution error: Not authorized (-1743)", errors.New("exit status 1")
	}

	_, err := MoveToTrash([]string{"<a@x>"})
	if !errors.Is(err, ErrAutomationDenied) {
		t.Errorf("err = %v, want ErrAutomationDenied", err)
	}
}

func TestBuildVerifyScriptSkipsTrashAndAllMail(t *testing.T) {
	s := buildVerifyScript()
	for _, name := range []string{
		"Deleted Messages", "Cestino", "Posta eliminata", "Trash",
		"Junk", "Spam", "Posta indesiderata",
		"Tutti i messaggi", "All Mail", "All Messages",
	} {
		if !strings.Contains(s, name) {
			t.Errorf("buildVerifyScript() must skip the mailbox %q:\n%s", name, s)
		}
	}
	if !strings.Contains(s, "message id") || !strings.Contains(s, "linefeed") {
		t.Errorf("buildVerifyScript() must compare message ids and join with linefeeds:\n%s", s)
	}
}

func TestRemainingInMailboxesParsesStdout(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	var gotArgs []string
	runOsaScript = func(script string, args []string) (string, string, error) {
		gotArgs = append([]string{}, args...)
		return "a@x\nb@y\n", "", nil
	}

	remaining, err := RemainingInMailboxes([]string{"<a@x>", "<b@y>", "<c@z>"})
	if err != nil {
		t.Fatalf("RemainingInMailboxes: %v", err)
	}
	if len(remaining) != 2 || remaining[0] != "a@x" || remaining[1] != "b@y" {
		t.Errorf("remaining = %v, want [a@x b@y]", remaining)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "a@x" {
		t.Errorf("args = %v, want the angle brackets stripped", gotArgs)
	}
}

func TestRemainingInMailboxesEmptyStdoutMeansNoneRemain(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	runOsaScript = func(script string, args []string) (string, string, error) {
		return "\n", "", nil
	}

	remaining, err := RemainingInMailboxes([]string{"<a@x>"})
	if err != nil {
		t.Fatalf("RemainingInMailboxes: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining = %v, want empty", remaining)
	}
}

func TestRemainingInMailboxesEmptyListIsNoop(t *testing.T) {
	orig := runOsaScript
	runOsaScript = func(script string, args []string) (string, string, error) {
		t.Fatal("runOsaScript called for an empty id list")
		return "", "", nil
	}
	defer func() { runOsaScript = orig }()

	remaining, err := RemainingInMailboxes(nil)
	if err != nil || len(remaining) != 0 {
		t.Errorf("RemainingInMailboxes(nil) = (%v, %v), want (nil, nil)", remaining, err)
	}
}

func TestBuildListAccountsScriptCollectsNameIDAndEmails(t *testing.T) {
	s := buildListAccountsScript()
	for _, want := range []string{
		"name of a", "id of a", "email addresses of a",
		"repeat with a in accounts", "linefeed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("buildListAccountsScript() is missing %q:\n%s", want, s)
		}
	}
}

func TestListAccountsParsesStdout(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	runOsaScript = func(script string, args []string) (string, string, error) {
		return "Omnys|4F602833|mirko.spinato@omnys.com,alias@omnys.com\n" +
			"DevPunks|2CDA1730|mirko.spinato@devpunks.com\n", "", nil
	}

	accounts, err := ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	if accounts[0].Name != "Omnys" || accounts[0].ID != "4F602833" {
		t.Errorf("accounts[0] = %+v, want Omnys/4F602833", accounts[0])
	}
	if len(accounts[0].Emails) != 2 || accounts[0].Emails[0] != "mirko.spinato@omnys.com" {
		t.Errorf("accounts[0].Emails = %v, want two emails", accounts[0].Emails)
	}
	if accounts[1].Emails[0] != "mirko.spinato@devpunks.com" {
		t.Errorf("accounts[1].Emails = %v", accounts[1].Emails)
	}
}

func TestListAccountsSurfacesAutomationError(t *testing.T) {
	orig := runOsaScript
	defer func() { runOsaScript = orig }()

	runOsaScript = func(script string, args []string) (string, string, error) {
		return "", "execution error: Not authorized (-1743)", errors.New("exit status 1")
	}

	_, err := ListAccounts()
	if !errors.Is(err, ErrAutomationDenied) {
		t.Errorf("err = %v, want ErrAutomationDenied", err)
	}
}
