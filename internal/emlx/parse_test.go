package emlx

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/harasuke/applemail/internal/testdata"
)

func TestParseSplitsThreeParts(t *testing.T) {
	mime := "Subject: Hi\r\n\r\nBody text\r\n"
	plist := "<?xml version=\"1.0\"?><plist></plist>\n"
	// The three parts: count line, then exactly that many bytes of MIME,
	// then the plist trailer.
	raw := []byte(strconv.Itoa(len(mime)) + "\n" + mime + plist)

	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.ByteCount != len(mime) {
		t.Errorf("ByteCount = %d, want %d", f.ByteCount, len(mime))
	}
	if string(f.MIME) != mime {
		t.Errorf("MIME = %q, want %q", f.MIME, mime)
	}
	if f.Truncated {
		t.Error("Truncated = true, want false for an exact byte count")
	}
	if !strings.Contains(string(f.Plist), "<plist>") {
		t.Errorf("Plist = %q, want the trailer", f.Plist)
	}
}

func TestParseFileOnFixture(t *testing.T) {
	msgs := testdata.DefaultMessages()
	root := testdata.BuildMailDir(t, msgs)

	f, err := ParseFile(testdata.EmlxPath(root, msgs[0]))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if string(f.MIME) != msgs[0].RawMIME {
		t.Error("parsed MIME does not match the fixture")
	}
}

func TestParseRejectsMissingByteCountLine(t *testing.T) {
	_, err := Parse([]byte("no newline here"))
	if !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}

func TestParseRejectsNonNumericByteCount(t *testing.T) {
	_, err := Parse([]byte("abc\r\nSubject: x\r\n"))
	if !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}

func TestParseClampsOverlongByteCount(t *testing.T) {
	mime := "Subject: Hi\r\n\r\nShort\r\n"
	// Declare far more bytes than the file actually contains.
	raw := []byte("99999\n" + mime)

	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse should clamp rather than fail: %v", err)
	}
	if string(f.MIME) != mime {
		t.Errorf("MIME = %q, want the available content %q", f.MIME, mime)
	}
	if !f.Truncated {
		t.Error("Truncated = false, want true when the count overruns the file")
	}
	if len(f.Plist) != 0 {
		t.Errorf("Plist = %q, want empty when the count overruns", f.Plist)
	}
}

func TestParseEmptyInput(t *testing.T) {
	if _, err := Parse(nil); !errors.Is(err, ErrBadByteCount) {
		t.Errorf("err = %v, want ErrBadByteCount", err)
	}
}
