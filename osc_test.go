package termemu

import (
	"bytes"
	"io"
	"testing"
)

func TestDECSET_2026_SynchronizedOutput(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[?2026h")
	if !mf.ViewFlags[VFSynchronizedOutput] {
		t.Fatalf("expected VFSynchronizedOutput set after DECSET 2026")
	}

	t1.mustHandleCommand(t, "[?2026l")
	if mf.ViewFlags[VFSynchronizedOutput] {
		t.Fatalf("expected VFSynchronizedOutput cleared after DECRST 2026")
	}
}

func feedText(t *testing.T, t1 *terminal, s string) {
	t.Helper()
	if err := t1.testFeedTerminalInputFromBackend([]byte(s), TextReadModeRune); err != nil {
		t.Fatal(err)
	}
}

func TestOSC8_HyperlinkSpans(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]8;;https://example.com\a")
	feedText(t, t1, "click")

	line := t1.screen().StyledLine(0, 5, 0)
	if len(line.Spans) == 0 {
		t.Fatal("expected spans for link text")
	}
	for _, sp := range line.Spans {
		if sp.Link != "https://example.com" {
			t.Fatalf("expected link on span, got %q (text %q)", sp.Link, sp.Text)
		}
	}
}

func TestOSC8_EndLink(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]8;;https://example.com\x1b\\")
	feedText(t, t1, "a")
	// Empty URI ends the link (ST-terminated form above also covered).
	t1.mustHandleCommand(t, "]8;;\x1b\\")
	feedText(t, t1, "b")

	line := t1.screen().StyledLine(0, 2, 0)
	if len(line.Spans) != 2 {
		t.Fatalf("expected 2 spans across link boundary, got %d: %+v", len(line.Spans), line.Spans)
	}
	if line.Spans[0].Link != "https://example.com" {
		t.Fatalf("first span should keep link, got %q", line.Spans[0].Link)
	}
	if line.Spans[1].Link != "" {
		t.Fatalf("span after link end should have no link, got %q", line.Spans[1].Link)
	}
}

func TestOSC8_LinkSpansWrappedLine(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.screen().setSize(10, 4)
	t1.mustHandleCommand(t, "[?7h") // enable auto-wrap

	t1.mustHandleCommand(t, "]8;id=1;https://example.com\a")
	feedText(t, t1, "0123456789abcde")
	t1.mustHandleCommand(t, "]8;;\a")

	for y := 0; y < 2; y++ {
		line := t1.screen().StyledLine(0, 10, y)
		found := false
		for _, sp := range line.Spans {
			if sp.Text == "" {
				continue
			}
			found = true
			if sp.Link != "https://example.com" {
				t.Fatalf("row %d span %q missing link, got %q", y, sp.Text, sp.Link)
			}
		}
		if !found {
			t.Fatalf("row %d has no content spans", y)
		}
	}
}

func TestOSC8_LinkPreservedInScrollback(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	t1.screen().setSize(20, 4)

	t1.mustHandleCommand(t, "]8;;https://example.com\a")
	fillScreen(t1, 6, func(i int) string {
		return "linkline"
	})
	t1.mustHandleCommand(t, "]8;;\a")

	sb := t1.Scrollback()
	if sb.Len() < 2 {
		t.Fatalf("expected scrolled-back rows, got %d", sb.Len())
	}
	base, next := sb.Range()
	for id := base; id < next; id++ {
		row, ok := sb.Row(id)
		if !ok {
			t.Fatalf("Row(%d) not found", id)
		}
		for _, sp := range row.Spans {
			if sp.Text == "" {
				continue
			}
			if sp.Link != "https://example.com" {
				t.Fatalf("scrollback row %d span %q missing link, got %q", id, sp.Text, sp.Link)
			}
		}
	}
}

func TestOSC8_LinksNotMergedAcrossBoundary(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]8;;https://a.example\a")
	feedText(t, t1, "a")
	t1.mustHandleCommand(t, "]8;;https://b.example\a")
	feedText(t, t1, "b")
	t1.mustHandleCommand(t, "]8;;\a")

	line := t1.screen().StyledLine(0, 2, 0)
	if len(line.Spans) != 2 {
		t.Fatalf("expected 2 spans for distinct links, got %d: %+v", len(line.Spans), line.Spans)
	}
	if line.Spans[0].Link != "https://a.example" || line.Spans[1].Link != "https://b.example" {
		t.Fatalf("links merged or lost: %+v", line.Spans)
	}
}

func TestOSC8_ErasedSpansHaveNoLink(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]8;;https://example.com\a")
	feedText(t, t1, "hi")
	// Erase the whole line while the link is still active.
	t1.mustHandleCommand(t, "[2K")

	line := t1.screen().StyledLine(0, 10, 0)
	for _, sp := range line.Spans {
		if sp.Link != "" {
			t.Fatalf("erased span should have no link, got %q", sp.Link)
		}
	}

	// Untouched blank rows never gain the active link either.
	blank := t1.screen().StyledLine(0, 10, 1)
	for _, sp := range blank.Spans {
		if sp.Link != "" {
			t.Fatalf("blank span should have no link, got %q", sp.Link)
		}
	}
	t1.mustHandleCommand(t, "]8;;\a")
}

func TestOSC8_MalformedIgnored(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]8;;https://example.com\a")
	// No params separator: state must not change.
	t1.mustHandleCommand(t, "]8;noseparator\a")
	feedText(t, t1, "x")

	line := t1.screen().StyledLine(0, 1, 0)
	if len(line.Spans) == 0 || line.Spans[0].Link != "https://example.com" {
		t.Fatalf("malformed OSC 8 changed link state: %+v", line.Spans)
	}
	t1.mustHandleCommand(t, "]8;;\a")
}

// clipboardRecorder is a Frontend that captures OSC 52 clipboard writes.
type clipboardRecorder struct {
	*MockFrontend
	sel   string
	data  []byte
	calls int
}

func (c *clipboardRecorder) Clipboard(sel string, data []byte) {
	c.sel = sel
	c.data = append([]byte(nil), data...)
	c.calls++
}

func newClipboardTerminal(rec *clipboardRecorder) *terminal {
	return newTerminal(rec, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeRune)
}

func TestOSC52_ClipboardWrite(t *testing.T) {
	rec := &clipboardRecorder{MockFrontend: NewMockFrontend()}
	t1 := newClipboardTerminal(rec)

	t1.mustHandleCommand(t, "]52;c;aGVsbG8=\a") // "hello"
	if rec.calls != 1 {
		t.Fatalf("expected 1 clipboard call, got %d", rec.calls)
	}
	if rec.sel != "c" {
		t.Fatalf("expected selection %q, got %q", "c", rec.sel)
	}
	if string(rec.data) != "hello" {
		t.Fatalf("expected decoded %q, got %q", "hello", string(rec.data))
	}
}

func TestOSC52_QueryNotForwarded(t *testing.T) {
	rec := &clipboardRecorder{MockFrontend: NewMockFrontend()}
	t1 := newClipboardTerminal(rec)

	t1.mustHandleCommand(t, "]52;c;?\a")
	if rec.calls != 0 {
		t.Fatalf("clipboard query must not be forwarded, got %d calls", rec.calls)
	}
}

func TestOSC52_BadBase64Ignored(t *testing.T) {
	rec := &clipboardRecorder{MockFrontend: NewMockFrontend()}
	t1 := newClipboardTerminal(rec)

	t1.mustHandleCommand(t, "]52;c;!!!not-base64!!!\a")
	if rec.calls != 0 {
		t.Fatalf("malformed base64 must be ignored, got %d calls", rec.calls)
	}
}

func TestOSC52_NoSinkNoPanic(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// MockFrontend does not implement ClipboardSink; must not panic.
	t1.mustHandleCommand(t, "]52;c;aGVsbG8=\a")
	t1.mustHandleCommand(t, "]52;c;?\a")
	t1.mustHandleCommand(t, "]52;c;!!!\a")
}

func TestOSC7_MapsToDirectory(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "]7;file:///home/user\a")
	if mf.ViewStrings[VSCurrentDirectory] != "file:///home/user" {
		t.Fatalf("OSC 7 should set VSCurrentDirectory, got %q", mf.ViewStrings[VSCurrentDirectory])
	}
	if mf.ViewStrings[VSCurrentFile] != "" {
		t.Fatalf("OSC 7 must no longer set VSCurrentFile, got %q", mf.ViewStrings[VSCurrentFile])
	}

	// Legacy OSC 6 still maps to the directory.
	t1.mustHandleCommand(t, "]6;/home/user\a")
	if mf.ViewStrings[VSCurrentDirectory] != "/home/user" {
		t.Fatalf("OSC 6 should set VSCurrentDirectory, got %q", mf.ViewStrings[VSCurrentDirectory])
	}
}
