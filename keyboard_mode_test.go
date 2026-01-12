package termemu

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHandleCmdCSI_KeyboardFlagsSetQuery(t *testing.T) {
	t1, _ := MakeTerminalWithMock()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	t1.backend = NewNoPTYBackend(bytes.NewReader(nil), w)

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[=5u"))) {
		t.Fatalf("handleCommand failed for =5u")
	}
	if t1.keyboardFlags() != 5 {
		t.Fatalf("expected keyboard flags 5, got %d", t1.keyboardFlags())
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[?u"))) {
		t.Fatalf("handleCommand failed for ?u")
	}
	buf := make([]byte, 16)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "\x1b[?5u" {
		t.Fatalf("unexpected query response: %q", string(buf[:n]))
	}
}

func TestHandleCmdCSI_KeyboardFlagsPushPop(t *testing.T) {
	t1, _ := MakeTerminalWithMock()

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[=3u"))) {
		t.Fatalf("handleCommand failed for =3u")
	}
	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[>9u"))) {
		t.Fatalf("handleCommand failed for >9u")
	}
	if t1.keyboardFlags() != 9 {
		t.Fatalf("expected keyboard flags 9 after push, got %d", t1.keyboardFlags())
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[<u"))) {
		t.Fatalf("handleCommand failed for <u")
	}
	if t1.keyboardFlags() != 3 {
		t.Fatalf("expected keyboard flags 3 after pop, got %d", t1.keyboardFlags())
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[<2u"))) {
		t.Fatalf("handleCommand failed for <2u")
	}
	if t1.keyboardFlags() != 0 {
		t.Fatalf("expected keyboard flags 0 after empty pop, got %d", t1.keyboardFlags())
	}
}
