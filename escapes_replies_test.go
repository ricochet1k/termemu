package termemu

import (
	"io"
	"testing"
	"time"
)

// feedQueryExpectReply feeds a terminal query through the normal read path and
// checks the exact bytes written back. Both operations run with a timeout so a
// regression (such as reply handlers deadlocking on the terminal lock) fails
// the test instead of hanging the suite.
func feedQueryExpectReply(t *testing.T, t1 *terminal, r io.ReadCloser, query, want string) {
	t.Helper()

	feedDone := make(chan error, 1)
	go func() {
		feedDone <- t1.testFeedTerminalInputFromBackend([]byte(query), TextReadModeRune)
	}()

	type readResult struct {
		data []byte
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := r.Read(buf)
		readDone <- readResult{data: append([]byte(nil), buf[:n]...), err: err}
	}()

	select {
	case got := <-readDone:
		if got.err != nil {
			t.Fatalf("reading reply to %q: %v", query, got.err)
		}
		if string(got.data) != want {
			t.Fatalf("reply to %q = %q, want %q", query, string(got.data), want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for reply to %q (escape-handler reply deadlock?)", query)
	}

	select {
	case err := <-feedDone:
		if err != nil {
			t.Fatalf("feeding %q: %v", query, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("feeding %q did not finish (escape-handler reply deadlock?)", query)
	}
}

func TestEscapeQueryReplies(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "DSR status", query: "\x1b[5n", want: "\x1b[0n"},
		{name: "DSR cursor position", query: "\x1b[6n", want: "\x1b[1;1R"},
		{name: "DA1 no params", query: "\x1b[c", want: "\x1b[?1;2c"},
		{name: "DA1 zero param", query: "\x1b[0c", want: "\x1b[?1;2c"},
		{name: "DA2", query: "\x1b[>c", want: "\x1b[>1;4402;0c"},
		{name: "kitty keyboard flags", query: "\x1b[?u", want: "\x1b[?0u"},
		{name: "XTVERSION no params", query: "\x1b[>q", want: "\x1bP>|termemu(0.1)\x1b\\"},
		{name: "XTVERSION zero param", query: "\x1b[>0q", want: "\x1bP>|termemu(0.1)\x1b\\"},
		{name: "Program Status feature query", query: "\x1b]7501;?\x1b\\", want: "\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			feedQueryExpectReply(t, t1, r, tt.query, tt.want)
		})
	}
}

func TestEscapeQueryRepliesWithKeyboardFlags(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.mustHandleCommand(t, "[=5u")
	feedQueryExpectReply(t, t1, r, "\x1b[?u", "\x1b[?5u")
}
