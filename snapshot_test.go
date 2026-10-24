package termemu

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	// Main screen: content, style, cursor, saved cursor, margins, auto-wrap.
	term.mustHandleCommand(t, "[1;4;31m")
	feedText(t, term, "main screen")
	term.mustHandleCommand(t, "[2;5H")
	term.mustHandleCommand(t, "7") // DECSC
	term.mustHandleCommand(t, "[3;10H")
	term.mustHandleCommand(t, "[3;11r")
	term.mustHandleCommand(t, "[3;10H")
	term.mustHandleCommand(t, "[?7l")
	term.mustHandleCommand(t, "[?25h")
	term.mustHandleCommand(t, "[?2004h")
	term.setViewInt(VIMouseMode, MMPress)
	term.setViewInt(VIMouseEncoding, MESGR)
	term.setViewString(VSWindowTitle, "snapshot-title")
	term.setViewString(VSCurrentDirectory, "file:///tmp")
	term.mustHandleCommand(t, "]8;;https://example.com\a")
	feedText(t, term, "link")

	// Alternate screen: content and per-screen cursor state.
	term.mustHandleCommand(t, "[?1049h")
	term.mustHandleCommand(t, "[2;2H")
	term.mustHandleCommand(t, "[32m")
	feedText(t, term, "alt screen")
	term.mustHandleCommand(t, "7") // DECSC on alt
	term.mustHandleCommand(t, "[4;3H")
	term.mustHandleCommand(t, "[?7h")
	term.updateKeyboardFlags(2, 1) // alt keyboard mode

	saved := term.Snapshot()
	if !saved.OnAltScreen {
		t.Fatal("snapshot should record the alt screen as active")
	}
	if saved.Link != "https://example.com" {
		t.Fatalf("snapshot link = %q", saved.Link)
	}
	if saved.Alt.Cursor == saved.Alt.SavedCursor {
		t.Fatal("alt cursor and saved cursor should differ in this fixture")
	}
	if saved.Main.TopMargin != 2 || saved.Main.BottomMargin != 10 {
		t.Fatalf("main margins = %d,%d", saved.Main.TopMargin, saved.Main.BottomMargin)
	}
	if saved.Main.AutoWrap {
		t.Fatal("main auto-wrap should be off in this fixture")
	}
	if saved.KeyboardAlt.flags != 2 {
		t.Fatalf("alt keyboard flags = %d", saved.KeyboardAlt.flags)
	}

	// Mutate everything.
	term.mustHandleCommand(t, "[?1049l")
	term.mustHandleCommand(t, "[0m")
	term.mustHandleCommand(t, "[1;1H")
	feedText(t, term, "mutated")
	term.mustHandleCommand(t, "8") // DECRC (make saved cursor differ)
	term.mustHandleCommand(t, "[1;2r")
	term.mustHandleCommand(t, "[?7h")
	term.mustHandleCommand(t, "[?25l")
	term.mustHandleCommand(t, "[?2004l")
	term.setViewInt(VIMouseMode, MMNone)
	term.setViewInt(VIMouseEncoding, MEX10)
	term.setViewString(VSWindowTitle, "mutated-title")
	term.setViewString(VSCurrentDirectory, "file:///mutated")
	term.mustHandleCommand(t, "]8;;\a")
	term.updateKeyboardFlags(0, 1)
	term.updateKeyboardFlags(0, 1) // both screens' keyboard modes reset

	if err := term.Restore(saved); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := term.Snapshot()
	if !reflect.DeepEqual(got, saved) {
		t.Fatalf("restored snapshot mismatch:\n got: %#v\nwant: %#v", got, saved)
	}

	// Spot-check that both screen buffers actually contain the restored text.
	if line := got.Main.Lines[0].PlainTextString(); !strings.HasPrefix(line, "main screen") {
		t.Fatalf("main line 0 = %q", line)
	}
	if line := got.Alt.Lines[1].PlainTextString(); !strings.Contains(line, "alt screen") {
		t.Fatalf("alt line 1 = %q", line)
	}
	foundLink := false
	for _, line := range got.Main.Lines {
		for _, sp := range line.Spans {
			if sp.Link == "https://example.com" {
				foundLink = true
			}
		}
	}
	if !foundLink {
		t.Fatal("main span link not restored")
	}
}

func TestSnapshotDeepCopyIndependence(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "original")
	term.mustHandleCommand(t, "[?25h")
	term.setViewString(VSWindowTitle, "original-title")

	snap := term.Snapshot()

	// Mutate the snapshot: the terminal must be unaffected.
	snap.Main.Lines[0].Spans[0].Text = "hacked"
	snap.Main.Lines[0].Spans[0].Width = 999
	snap.Main.Wrapped[0] = true
	snap.Main.Cursor = Pos{X: 7, Y: 7}
	snap.ViewFlags[VFShowCursor] = false
	snap.ViewStrings[VSWindowTitle] = "hacked"
	snap.Link = "hacked"

	if got := term.Line(0); !strings.HasPrefix(got, "original") {
		t.Fatalf("terminal line changed after mutating snapshot: %q", got)
	}
	if !term.GetViewFlag(VFShowCursor) {
		t.Fatal("terminal flag changed after mutating snapshot")
	}
	if got := term.GetViewString(VSWindowTitle); got != "original-title" {
		t.Fatalf("terminal string changed after mutating snapshot: %q", got)
	}
	if got := term.CursorPos(); got == (Pos{X: 7, Y: 7}) {
		t.Fatal("terminal cursor changed after mutating snapshot")
	}
	if ss := term.mainScreen.(*spanScreen); ss.wrapped[0] {
		t.Fatal("terminal wrapped flag changed after mutating snapshot")
	}
	if term.hyperlink == "hacked" {
		t.Fatal("terminal link changed after mutating snapshot")
	}

	// Mutate the terminal: an earlier snapshot must be unaffected.
	snap2 := term.Snapshot()
	wantLine := snap2.Main.Lines[0].PlainTextString()
	wantCursor := snap2.Main.Cursor
	wantTitle := snap2.ViewStrings[VSWindowTitle]

	feedText(t, term, "XYZ")
	term.mustHandleCommand(t, "[5;5H")
	term.mustHandleCommand(t, "[33m")
	term.setViewString(VSWindowTitle, "changed-title")
	term.mustHandleCommand(t, "]8;;https://changed.example\a")

	if got := snap2.Main.Lines[0].PlainTextString(); got != wantLine {
		t.Fatalf("snapshot line changed after mutating terminal: %q != %q", got, wantLine)
	}
	if snap2.Main.Cursor != wantCursor {
		t.Fatalf("snapshot cursor changed after mutating terminal: %v != %v", snap2.Main.Cursor, wantCursor)
	}
	if got := snap2.ViewStrings[VSWindowTitle]; got != wantTitle {
		t.Fatalf("snapshot string changed after mutating terminal: %q != %q", got, wantTitle)
	}
	if snap2.Link != "" {
		t.Fatalf("snapshot link changed after mutating terminal: %q", snap2.Link)
	}
}

func TestSnapshotRestoreDifferentSize(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "keep me")

	snap := term.Snapshot()
	if err := term.Resize(40, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	feedText(t, term, "resized")

	if err := term.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if w, h := term.Size(); w != 80 || h != 14 {
		t.Fatalf("Restore left terminal at %dx%d, want snapshot size 80x14", w, h)
	}
	if got := term.Line(0); !strings.HasPrefix(got, "keep me") {
		t.Fatalf("restored line = %q, want the snapshot content", got)
	}
	if err := term.Restore(Snapshot{}); !errors.Is(err, ErrSnapshotSizeMismatch) {
		t.Fatalf("Restore(zero snapshot) = %v, want ErrSnapshotSizeMismatch", err)
	}
}

func TestRestoreEmitsFullRepaint(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "hello")
	term.mustHandleCommand(t, "[?25l")
	snap := term.Snapshot()

	// Mutate so restore has visible work to report.
	term.mustHandleCommand(t, "[?25h")
	term.mustHandleCommand(t, "[2;3H")
	feedText(t, term, "more")
	term.mustHandleCommand(t, "[35m")

	regionsBefore := mf.RegionCount()
	cursorBefore := mf.CursorMovedCount
	stylesBefore := len(mf.Styles)

	if err := term.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := mf.RegionCount() - regionsBefore; got != 1 {
		t.Fatalf("Restore emitted %d regions, want 1", got)
	}
	last := mf.Regions[len(mf.Regions)-1]
	if last.C != CRRestore {
		t.Fatalf("Restore region reason = %v, want CRRestore", last.C)
	}
	w, h := term.Size()
	if last.R != (Region{X: 0, Y: 0, X2: w, Y2: h}) {
		t.Fatalf("Restore region = %+v, want full screen 0,0,%d,%d", last.R, w, h)
	}
	if got := mf.CursorMovedCount - cursorBefore; got != 1 {
		t.Fatalf("Restore emitted %d cursor moves, want 1", got)
	}
	wantCursor := snap.Main.Cursor
	wantStyle := snap.Main.Style
	if snap.OnAltScreen {
		wantCursor = snap.Alt.Cursor
		wantStyle = snap.Alt.Style
	}
	if mf.CursorX != wantCursor.X || mf.CursorY != wantCursor.Y {
		t.Fatalf("Restore cursor callback = (%d,%d), want (%d,%d)",
			mf.CursorX, mf.CursorY, wantCursor.X, wantCursor.Y)
	}
	if got := len(mf.Styles) - stylesBefore; got != 1 {
		t.Fatalf("Restore emitted %d style changes, want 1", got)
	}
	if mf.Styles[len(mf.Styles)-1] != wantStyle {
		t.Fatal("Restore style callback does not match snapshot style")
	}
}

func TestSnapshotRestoreWrappedFlags(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	term.mustHandleCommand(t, "[?7h")
	feedText(t, term, strings.Repeat("x", 100))

	ss := term.mainScreen.(*spanScreen)
	if !ss.wrapped[1] {
		t.Fatal("fixture did not wrap onto the second row")
	}
	snap := term.Snapshot()
	if !snap.Main.Wrapped[1] {
		t.Fatal("snapshot did not capture wrapped flag")
	}

	term.mustHandleCommand(t, "[2J")
	term.mustHandleCommand(t, "[H")
	if ss.wrapped[1] {
		t.Fatal("clear did not reset wrapped flag")
	}

	if err := term.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !ss.wrapped[1] {
		t.Fatal("restore did not restore wrapped flag")
	}
	if got := term.Line(1); !strings.HasPrefix(got, "x") {
		t.Fatalf("restored wrapped row 1 = %q", got)
	}
}

func TestSnapshotRestoreZeroWidthSpans(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	// Install a zero-width span (combining mark) directly so the test
	// exercises restoreState rather than the parser's merge path.
	ss := term.mainScreen.(*spanScreen)
	style := NewStyle()
	ss.lines[0] = spanLine{
		spans: []Span{
			{Style: style, Text: "e", Width: 1, Link: "https://link.example"},
			{Style: style, Text: "\u0301", Width: 0, Link: "https://link.example"},
			{Style: style, Width: 79},
		},
		width: 80,
	}

	snap := term.Snapshot()
	hasZeroWidth := false
	for _, sp := range snap.Main.Lines[0].Spans {
		if sp.Width == 0 {
			hasZeroWidth = true
			break
		}
	}
	if !hasZeroWidth {
		t.Fatalf("snapshot did not capture a zero-width span: %+v", snap.Main.Lines[0].Spans)
	}
	want := term.StyledLine(0, 2, 0)

	term.mustHandleCommand(t, "[2J")
	term.mustHandleCommand(t, "[H")
	if err := term.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := term.StyledLine(0, 2, 0)
	if !got.Equivalent(want) {
		t.Fatalf("restored line not equivalent:\n got: %+v\nwant: %+v", got, want)
	}
	for _, sp := range got.Spans {
		if sp.Text != "" && sp.Link != "https://link.example" {
			t.Fatalf("restored span lost its link: %+v", sp)
		}
	}
}
