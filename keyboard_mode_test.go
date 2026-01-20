package termemu

import (
	"testing"
)

func TestHandleCmdCSI_KeyboardFlagsSetQuery(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[=5u")
	if t1.keyboardFlags() != 5 {
		t.Fatalf("expected keyboard flags 5, got %d", t1.keyboardFlags())
	}

	// Query keyboard flags - run in goroutine because it writes response
	t1.mustHandleCommand(t, "[?u")
	buf := make([]byte, 16)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "\x1b[?5u" {
		t.Fatalf("unexpected query response: %q", string(buf[:n]))
	}
}

func TestHandleCmdCSI_KeyboardFlagsPushPop(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[=3u")
	t1.mustHandleCommand(t, "[>9u")
	if t1.keyboardFlags() != 9 {
		t.Fatalf("expected keyboard flags 9 after push, got %d", t1.keyboardFlags())
	}

	t1.mustHandleCommand(t, "[<u")
	if t1.keyboardFlags() != 3 {
		t.Fatalf("expected keyboard flags 3 after pop, got %d", t1.keyboardFlags())
	}

	t1.mustHandleCommand(t, "[<2u")
	if t1.keyboardFlags() != 0 {
		t.Fatalf("expected keyboard flags 0 after empty pop, got %d", t1.keyboardFlags())
	}
}
