package termemu

import (
	"strings"
	"testing"
)

// TestViewConsistent verifies View exposes a coherent snapshot of terminal
// state and that plain read methods agree with it.
func TestViewConsistent(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	if err := term.testFeedTerminalInputFromBackend([]byte("hello view"), TextReadModeRune); err != nil {
		t.Fatalf("feed: %v", err)
	}
	term.setViewFlag(VFShowCursor, true)

	var (
		w, h    int
		cursor  Pos
		line0   string
		shown   bool
		title   string
		ansi0   string
		styled  Line
		styleds []Line
	)
	term.View(func(v LockedView) {
		w, h = v.Size()
		cursor = v.CursorPos()
		line0 = v.Line(0)
		shown = v.GetViewFlag(VFShowCursor)
		title = v.GetViewString(VSWindowTitle)
		ansi0 = v.ANSILine(0)
		styled = v.StyledLine(0, w, 0)
		styleds = v.StyledLines(Region{X: 0, Y: 0, X2: w, Y2: 2})
	})

	if w == 0 || h == 0 {
		t.Fatalf("View reported empty size %dx%d", w, h)
	}
	if cursor.Y != 0 || cursor.X != len("hello view") {
		t.Fatalf("unexpected cursor %+v", cursor)
	}
	if !strings.Contains(line0, "hello view") {
		t.Fatalf("line 0 = %q, want hello view", line0)
	}
	if !shown {
		t.Fatal("VFShowCursor should be set in the view")
	}
	if title != "" {
		t.Fatalf("unexpected title %q", title)
	}
	if ansi0 != term.ANSILine(0) {
		t.Fatalf("view ANSILine differs from locking ANSILine")
	}
	if styled.PlainTextString() != term.StyledLine(0, w, 0).PlainTextString() {
		t.Fatalf("view StyledLine differs from locking StyledLine")
	}
	if len(styleds) != 2 {
		t.Fatalf("view StyledLines returned %d lines, want 2", len(styleds))
	}
	if pw, ph := term.Size(); pw != w || ph != h {
		t.Fatalf("plain Size = %dx%d, view Size = %dx%d", pw, ph, w, h)
	}
}

// TestCallbackReceivesUsableView checks that Frontend callbacks get a view
// backed by the terminal.
func TestCallbackReceivesUsableView(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	term.setViewFlag(VFShowCursor, true)

	if err := term.testFeedTerminalInputFromBackend([]byte("callback"), TextReadModeRune); err != nil {
		t.Fatalf("feed: %v", err)
	}

	mf.mu.Lock()
	valid := mf.ViewValid
	gotSize := mf.ViewSize
	gotShow := mf.ViewShowCursor
	mf.mu.Unlock()

	if !valid {
		t.Fatal("RegionChanged received a view not backed by the terminal")
	}
	if w, h := term.Size(); gotSize.X != w || gotSize.Y != h {
		t.Fatalf("callback view size = %dx%d, terminal size = %dx%d", gotSize.X, gotSize.Y, w, h)
	}
	if !gotShow {
		t.Fatal("callback view did not observe VFShowCursor")
	}
}

type scrollbackViewFrontend struct {
	*MockFrontend

	ranges       int
	rows         int
	logicalLines int
	lastRow      Row
	hadRow       bool
}

func (f *scrollbackViewFrontend) RegionChanged(v LockedView, r Region, c ChangeReason) {
	f.MockFrontend.RegionChanged(v, r, c)
	if c != CRScroll {
		return
	}
	sb := v.Scrollback()
	base, next := sb.Range()
	if next <= base {
		return
	}
	f.ranges++
	if sb.Len() > 0 {
		f.rows = sb.Len()
	}
	if row, ok := sb.Row(base); ok {
		f.lastRow = row
		f.hadRow = true
	}
	if lines := sb.LogicalLine(base); len(lines) > 0 {
		f.logicalLines++
	}
	// MovePos must also work without taking the lock again.
	if pos := sb.MovePos(ScrollPos{ID: base}, 0); pos.ID != base {
		f.ranges--
	}
}

// TestViewScrollbackInCallback verifies LockedView.Scrollback does not
// deadlock and can read captured rows from inside a frontend callback.
func TestViewScrollbackInCallback(t *testing.T) {
	f := &scrollbackViewFrontend{MockFrontend: NewMockFrontend()}
	term := newTerminal(f, NewNoPTYBackend(strings.NewReader(""), nil), TextReadModeRune)

	// Enough lines to scroll the default 14-row screen several times.
	payload := strings.Repeat("scrollback line\r\n", 30)
	if err := term.testFeedTerminalInputFromBackend([]byte(payload), TextReadModeRune); err != nil {
		t.Fatalf("feed: %v", err)
	}

	if f.ranges == 0 {
		t.Fatal("no scroll events observed by the callback")
	}
	if !f.hadRow {
		t.Fatal("callback could not read a scrollback row through the view")
	}
	if f.lastRow.PlainTextString() == "" {
		t.Fatalf("scrollback row read in callback is empty: %+v", f.lastRow)
	}
	if f.logicalLines == 0 {
		t.Fatal("callback could not read a logical line through the view")
	}
	if got := term.Scrollback().Len(); got != f.rows {
		t.Fatalf("plain Scrollback Len = %d, callback saw %d", got, f.rows)
	}
}
