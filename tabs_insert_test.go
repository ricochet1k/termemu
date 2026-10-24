package termemu

import (
	"strings"
	"testing"
)

// tabTerm returns a terminal of the given size for tab/insert tests.
func tabTerm(t *testing.T, w, h int) *terminal {
	t.Helper()
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(w, h); err != nil {
		t.Fatalf("Resize(%d, %d): %v", w, h, err)
	}
	return term
}

// tabCursor feeds data and returns the resulting cursor column.
func tabCursor(t *testing.T, term *terminal, data string) int {
	t.Helper()
	feedText(t, term, data)
	return term.CursorPos().X
}

func TestTabs_DefaultStops(t *testing.T) {
	term := tabTerm(t, 40, 4)
	for _, tc := range []struct {
		start, want int
	}{
		{0, 8}, {3, 8}, {7, 8}, {8, 16}, {15, 16}, {16, 24}, {39, 39},
	} {
		term.mustHandleCommand(t, "[H")
		feedText(t, term, strings.Repeat("x", tc.start))
		if got := tabCursor(t, term, "\t"); got != tc.want {
			t.Fatalf("TAB from %d landed on %d, want %d", tc.start, got, tc.want)
		}
	}
}

func TestTabs_TABAtOrPastLastStop(t *testing.T) {
	term := tabTerm(t, 20, 4)
	// The last default stop is 16; TAB from 16 has nowhere to go and lands
	// on the right margin (xterm behavior).
	if got := tabCursor(t, term, strings.Repeat("x", 16)+"\t"); got != 19 {
		t.Fatalf("TAB past last stop = %d, want 19", got)
	}
}

func TestTabs_HTS(t *testing.T) {
	term := tabTerm(t, 40, 4)
	// Set a stop at column 3.
	feedText(t, term, "xxx\x1bH")
	// go back to column 0 and tab: 0 -> 3 -> 8.
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 3 {
		t.Fatalf("TAB after HTS = %d, want 3", got)
	}
	if got := tabCursor(t, term, "\t"); got != 8 {
		t.Fatalf("second TAB after HTS = %d, want 8", got)
	}
}

func TestTabs_TBCClearCurrent(t *testing.T) {
	term := tabTerm(t, 40, 4)
	// Clear the default stop at column 8.
	feedText(t, term, "xxxxxxxx\x1b[g")
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 16 {
		t.Fatalf("TAB after TBC 0 = %d, want 16", got)
	}
}

func TestTabs_TBCClearAll(t *testing.T) {
	term := tabTerm(t, 40, 4)
	term.mustHandleCommand(t, "[3g")
	if got := tabCursor(t, term, "\t"); got != 39 {
		t.Fatalf("TAB after TBC 3 = %d, want 39", got)
	}
	// CBT with no stops returns to column 0.
	if got := tabCursor(t, term, "\x1b[Z"); got != 0 {
		t.Fatalf("CBT with no stops = %d, want 0", got)
	}
}

func TestTabs_CursorTabulation(t *testing.T) {
	term := tabTerm(t, 80, 4)
	term.mustHandleCommand(t, "[5I")
	if got := term.CursorPos().X; got != 40 {
		t.Fatalf("CHT 5 = %d, want 40", got)
	}
	term.mustHandleCommand(t, "[2Z")
	if got := term.CursorPos().X; got != 24 {
		t.Fatalf("CBT 2 = %d, want 24", got)
	}

	// Custom stops participate in CHT/CBT.
	term.mustHandleCommand(t, "[H")
	feedText(t, term, "ab\x1bH\x1b[2I")
	if got := term.CursorPos().X; got != 16 {
		t.Fatalf("CHT with custom stop = %d, want 16", got)
	}
	term.mustHandleCommand(t, "[Z")
	if got := term.CursorPos().X; got != 8 {
		t.Fatalf("CBT with custom stop = %d, want 8", got)
	}
}

func TestTabs_ResizeKeepsStops(t *testing.T) {
	term := tabTerm(t, 40, 4)
	feedText(t, term, "xxx\x1bH") // stop at 3
	if err := term.Resize(20, 4); err != nil {
		t.Fatal(err)
	}
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 3 {
		t.Fatalf("TAB after shrink = %d, want 3", got)
	}
	if err := term.Resize(40, 4); err != nil {
		t.Fatal(err)
	}
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 3 {
		t.Fatalf("TAB after grow = %d, want 3", got)
	}
}

func TestTabs_PerScreen(t *testing.T) {
	term := tabTerm(t, 40, 4)
	feedText(t, term, "xxx\x1bH") // main screen: stop at 3
	term.mustHandleCommand(t, "[?1049h")
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 8 {
		t.Fatalf("alt screen TAB = %d, want 8 (its own default stops)", got)
	}
}

func TestTabs_ResetOnSoftAndHardReset(t *testing.T) {
	// RIS resets the stops to the default map.
	term := tabTerm(t, 40, 4)
	term.mustHandleCommand(t, "[3g")
	feedText(t, term, "\x1bc")
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 8 {
		t.Fatalf("TAB after RIS = %d, want 8", got)
	}

	// DECSTR keeps tab stops (xterm only clears them on a full reset).
	term = tabTerm(t, 40, 4)
	term.mustHandleCommand(t, "[3g")
	feedText(t, term, "\x1b[!p")
	term.mustHandleCommand(t, "[H")
	if got := tabCursor(t, term, "\t"); got != 39 {
		t.Fatalf("TAB after DECSTR = %d, want 39 (stops preserved)", got)
	}
}

func TestTabs_SnapshotRestore(t *testing.T) {
	term := tabTerm(t, 40, 4)
	feedText(t, term, "xxx\x1b[3g\x1bH") // clear all, then set only column 3
	snap := term.Snapshot()

	other := tabTerm(t, 40, 4)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	other.mustHandleCommand(t, "[H")
	if got := tabCursor(t, other, "\t"); got != 3 {
		t.Fatalf("restored TAB = %d, want 3", got)
	}
	if got := tabCursor(t, other, "\t"); got != 39 {
		t.Fatalf("restored second TAB = %d, want 39 (only one stop)", got)
	}
}

func TestICH_InsertsBlanks(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abcdef")
	term.mustHandleCommand(t, "[1;3H") // column 2
	term.mustHandleCommand(t, "[3@")

	if got := strings.TrimRight(term.Line(0), " "); got != "ab   cdef" {
		t.Fatalf("after ICH line = %q, want %q", got, "ab   cdef")
	}
	if got := term.CursorPos(); got != (Pos{X: 2, Y: 0}) {
		t.Fatalf("ICH moved the cursor to %+v", got)
	}
}

func TestICH_DefaultParamInsertsOne(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abc\x1b[1;2H\x1b[@")
	if got := strings.TrimRight(term.Line(0), " "); got != "a bc" {
		t.Fatalf("line = %q, want %q", got, "a bc")
	}
}

func TestICH_TruncatesAtRightEdge(t *testing.T) {
	term := tabTerm(t, 10, 4)
	feedText(t, term, "0123456789")
	term.mustHandleCommand(t, "[1;8H") // column 7
	term.mustHandleCommand(t, "[10@")

	want := "0123456   "
	if got := term.Line(0); got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestICH_KeepsLineWidthAndStyles(t *testing.T) {
	term := tabTerm(t, 12, 4)
	feedText(t, term, "\x1b[31mAB\x1b[32mCD\x1b[0m")
	term.mustHandleCommand(t, "[1;2H")
	term.mustHandleCommand(t, "[2@")

	ss := term.mainScreen.(*spanScreen)
	line := ss.lines[0]
	if got := line.width; got != 12 {
		t.Fatalf("line width = %d, want 12", got)
	}
	// The red "A" stays put; the shifted "B" is now at column 3 with its
	// original style.
	styled := ss.StyledLine(0, 12, 0)
	var cells []string
	for _, sp := range styled.Spans {
		if sp.Text == "" {
			cells = append(cells, strings.Repeat(" ", sp.Width))
			continue
		}
		cells = append(cells, sp.Text)
	}
	if got, want := strings.Join(cells, ""), "A  BCD      "; got != want {
		t.Fatalf("styled cells = %q, want %q", got, want)
	}
}

func TestICH_WideClusterAtBoundary(t *testing.T) {
	term := tabTerm(t, 10, 4)
	feedText(t, term, "界界abc")
	term.mustHandleCommand(t, "[1;2H") // column 1, inside the first wide char
	term.mustHandleCommand(t, "[1@")

	ss := term.mainScreen.(*spanScreen)
	if got := ss.lines[0].width; got != 10 {
		t.Fatalf("line width = %d, want 10", got)
	}
	checkScreenInvariants(t, ss, "ICH inside a wide cluster")
}

func TestIRM_InsertMode(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abcdef")
	term.mustHandleCommand(t, "[4h")
	term.mustHandleCommand(t, "[1;1H")
	feedText(t, term, "XY")

	if got := strings.TrimRight(term.Line(0), " "); got != "XYabcdef" {
		t.Fatalf("line = %q, want %q", got, "XYabcdef")
	}
	term.mustHandleCommand(t, "[4l")
	term.mustHandleCommand(t, "[1;1H")
	feedText(t, term, "Z")
	if got := strings.TrimRight(term.Line(0), " "); got != "ZYabcdef" {
		t.Fatalf("after IRM reset line = %q, want %q", got, "ZYabcdef")
	}
}

func TestIRM_InsertsWholeRuns(t *testing.T) {
	term := tabTerm(t, 10, 4)
	feedText(t, term, "0123456789")
	term.mustHandleCommand(t, "[4h")
	term.mustHandleCommand(t, "[1;3H")
	feedText(t, term, "AB")
	if got := term.Line(0); got != "01AB234567" {
		t.Fatalf("line = %q, want %q", got, "01AB234567")
	}
}

func TestIRM_WithAutoWrap(t *testing.T) {
	term := tabTerm(t, 5, 4)
	feedText(t, term, "abcde")
	term.mustHandleCommand(t, "[4h")
	term.mustHandleCommand(t, "[1;1H")
	feedText(t, term, "XYZW")
	// Inserting four cells at column 0 shifts "a" to column 4 and pushes
	// "bcde" past the right edge; the run then overwrites columns 0-3.
	if got := term.Line(0); got != "XYZWa" {
		t.Fatalf("line = %q, want %q", got, "XYZWa")
	}
}

func TestIRM_SnapshotRestore(t *testing.T) {
	term := tabTerm(t, 20, 4)
	term.mustHandleCommand(t, "[4h")
	snap := term.Snapshot()
	if !snap.Main.InsertMode {
		t.Fatal("snapshot does not record insert mode")
	}

	other := tabTerm(t, 20, 4)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	feedText(t, other, "abc")
	other.mustHandleCommand(t, "[H")
	feedText(t, other, "X")
	if got := strings.TrimRight(other.Line(0), " "); got != "Xabc" {
		t.Fatalf("restored terminal line = %q, want %q", got, "Xabc")
	}
}
