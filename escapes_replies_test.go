package termemu

import (
	"io"
	"strings"
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

func TestWindowAndCellSizeReplies(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedQueryExpectReply(t, t1, r, "\x1b[16t", "\x1b[6;16;8t")
	feedQueryExpectReply(t, t1, r, "\x1b[18t", "\x1b[8;14;80t")
	feedQueryExpectReply(t, t1, r, "\x1b[14t", "\x1b[4;224;640t")
}

func TestDECRQSSReplies(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	feedQueryExpectReply(t, t1, r, "\x1bP$qm\x1b\\", "\x1bP1$r0m\x1b\\")
	feedESC(t, t1, "\x1b[3;1m")
	feedQueryExpectReply(t, t1, r, "\x1bP$qm\x1b\\", "\x1bP1$r0;1;3m\x1b\\")
	feedQueryExpectReply(t, t1, r, "\x1bP$qr\x1b\\", "\x1bP1$r1;14r\x1b\\")
	t1.mustHandleCommand(t, "[2;3r")
	feedQueryExpectReply(t, t1, r, "\x1bP$qr\x1b\\", "\x1bP1$r2;3r\x1b\\")
	feedQueryExpectReply(t, t1, r, "\x1bP$q q\x1b\\", "\x1bP1$r0 q\x1b\\")
	t1.mustHandleCommand(t, "[3 q")
	feedQueryExpectReply(t, t1, r, "\x1bP$q q\x1b\\", "\x1bP1$r3 q\x1b\\")
	feedQueryExpectReply(t, t1, r, "\x1bP$q$|\x1b\\", "\x1bP1$r80$|\x1b\\")
	feedQueryExpectReply(t, t1, r, "\x1bP$q*x\x1b\\", "\x1bP1$r0*x\x1b\\")
	feedQueryExpectReply(t, t1, r, "\x1bP$qz\x1b\\", "\x1bP0$r\x1b\\")
}

func TestEscapeQueryRepliesWithKeyboardFlags(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.mustHandleCommand(t, "[=5u")
	feedQueryExpectReply(t, t1, r, "\x1b[?u", "\x1b[?5u")
}

func TestDECRQM_ReportsPrivateModeState(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Default: synchronized output (2026) is reset.
	feedQueryExpectReply(t, t1, r, "\x1b[?2026$p", "\x1b[?2026;2$y")
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
		t.Fatalf("DECRQM leaked text to screen: %q", got)
	}

	t1.mustHandleCommand(t, "[?2026h")
	feedQueryExpectReply(t, t1, r, "\x1b[?2026$p", "\x1b[?2026;1$y")

	t1.mustHandleCommand(t, "[?2026l")
	feedQueryExpectReply(t, t1, r, "\x1b[?2026$p", "\x1b[?2026;2$y")

	// Unknown private mode.
	feedQueryExpectReply(t, t1, r, "\x1b[?9999$p", "\x1b[?9999;0$y")

	// Plain (ANSI) modes report too: IRM is reset by default.
	feedQueryExpectReply(t, t1, r, "\x1b[4$p", "\x1b[4;2$y")
}

func TestDECRQM_ReportsCommonPrivateModes(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	tests := []struct {
		setup string
		query string
		want  string
	}{
		{setup: "[?1h", query: "\x1b[?1$p", want: "\x1b[?1;1$y"},
		{setup: "[?1l", query: "\x1b[?1$p", want: "\x1b[?1;2$y"},
		{setup: "[?7l", query: "\x1b[?7$p", want: "\x1b[?7;2$y"},
		{setup: "[?7h", query: "\x1b[?7$p", want: "\x1b[?7;1$y"},
		{setup: "[?25l", query: "\x1b[?25$p", want: "\x1b[?25;2$y"},
		{setup: "[?25h", query: "\x1b[?25$p", want: "\x1b[?25;1$y"},
		{setup: "[?1000h", query: "\x1b[?1000$p", want: "\x1b[?1000;1$y"},
		{setup: "[?1006h", query: "\x1b[?1006$p", want: "\x1b[?1006;1$y"},
		{setup: "[?2004h", query: "\x1b[?2004$p", want: "\x1b[?2004;1$y"},
	}
	for _, tt := range tests {
		t1.mustHandleCommand(t, tt.setup)
		feedQueryExpectReply(t, t1, r, tt.query, tt.want)
	}
}

func TestDECSTR_SoftReset(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Leave content on screen so we can verify DECSTR does not erase it.
	t1.mustHandleCommand(t, "[1;31m")
	t1.mustHandleCommand(t, "[2;3r")
	t1.mustHandleCommand(t, "[?7l")
	t1.mustHandleCommand(t, "[?25l")
	feedESC(t, t1, "KEEP")

	t1.mustHandleCommand(t, "[!p")

	if got, want := t1.screen().Style(), NewStyle(); got != want {
		t.Fatalf("style after DECSTR = %+v, want default %+v", got, want)
	}
	if got := t1.screen().TopMargin(); got != 0 {
		t.Fatalf("TopMargin after DECSTR = %d, want 0", got)
	}
	if got, want := t1.screen().BottomMargin(), t1.screen().Size().Y-1; got != want {
		t.Fatalf("BottomMargin after DECSTR = %d, want %d", got, want)
	}
	if !t1.screen().AutoWrap() {
		t.Fatal("AutoWrap after DECSTR = false, want true")
	}
	if !t1.viewFlags[VFShowCursor] {
		t.Fatal("cursor hidden after DECSTR")
	}
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "KEEP" {
		t.Fatalf("DECSTR changed screen content: %q, want %q", got, "KEEP")
	}
}

func TestCSI_UnknownIntermediatesAreConsumed(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	for _, seq := range []string{
		"\x1b[\"p",
		"\x1b[1 q",
		"\x1b[?1$z",
		"\x1b[!q",
		"\x1b[2$x",
	} {
		feedESC(t, t1, seq)
	}
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
		t.Fatalf("CSI intermediates leaked text: %q", got)
	}
}
