package termemu

import (
	"io"
	"strings"
	"testing"
)

func TestEscapeCommandBufferIsBounded(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeGrapheme)

	// A huge but eventually terminated OSC must be consumed without being
	// buffered in full, and following text must still render.
	oversizedOSC := "\x1b]0;" + strings.Repeat("a", maxEscapeCommandBytes*2) + "\x07"
	if err := term.testFeedTerminalInputFromBackend([]byte(oversizedOSC+"X"), TextReadModeGrapheme); err != nil {
		t.Fatalf("oversized OSC: %v", err)
	}
	if got := term.Line(0); !strings.Contains(got, "X") {
		t.Fatalf("text after oversized OSC missing: %q", got)
	}

	// Same for CSI with unbounded parameter bytes.
	oversizedCSI := "\x1b[" + strings.Repeat("1", maxEscapeCommandBytes*2) + "m"
	if err := term.testFeedTerminalInputFromBackend([]byte(oversizedCSI+"Y"), TextReadModeGrapheme); err != nil {
		t.Fatalf("oversized CSI: %v", err)
	}
	if got := term.Line(0); !strings.Contains(got, "Y") {
		t.Fatalf("text after oversized CSI missing: %q", got)
	}
}

func TestCursorVisibleByDefault(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeGrapheme)
	if !term.GetViewFlag(VFShowCursor) {
		t.Fatal("cursor should be visible by default")
	}
	if err := term.testFeedTerminalInputFromBackend([]byte("\x1b[?25l"), TextReadModeGrapheme); err != nil {
		t.Fatal(err)
	}
	if term.GetViewFlag(VFShowCursor) {
		t.Fatal("DECRST 25 should hide the cursor")
	}
}

func TestSendMouseRawReturnsErrors(t *testing.T) {
	term := newTerminal(NewMockFrontend(), NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	term.setViewInt(VIMouseMode, MMPressRelease)
	term.setViewInt(VIMouseEncoding, MESGR)

	// No writer: Write must return an error rather than Panic.
	if err := term.SendMouseRaw(MBtn1, true, 0, 1, 2); err == nil {
		t.Fatal("SendMouseRaw with no backend writer should fail")
	}
	if err := term.SendMouseRaw(MBtn1, true, 0, -1, 2); err == nil {
		t.Fatal("SendMouseRaw with negative coordinates should fail")
	}

	term.setViewInt(VIMouseEncoding, 99)
	if err := term.SendMouseRaw(MBtn1, true, 0, 1, 2); err == nil {
		t.Fatal("SendMouseRaw with unknown encoding should fail")
	}
}

func TestBackendNoopWhenClosed(t *testing.T) {
	backend := NewNoPTYBackend(io.NopCloser(strings.NewReader("")), io.Discard)
	_ = backend.Close()
}

func TestDoneIsLockFree(t *testing.T) {
	term := newTerminal(NewMockFrontend(), NewNoPTYBackend(nil, nil), TextReadModeGrapheme)
	term.Lock()
	// Calling Done while the terminal lock is held must not block (watchdog
	// usage). The old implementation locked here and deadlocked.
	if term.Done() == nil {
		t.Fatal("Done returned nil")
	}
	term.Unlock()
}
