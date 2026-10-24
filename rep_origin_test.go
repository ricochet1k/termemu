package termemu

import (
	"strings"
	"testing"
)

func TestREP_RepeatsLastChar(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "ab\x1b[3b")
	if got, want := strings.TrimRight(term.Line(0), " "), "abbbb"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_Defaults(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "a\x1b[b")
	if got, want := strings.TrimRight(term.Line(0), " "), "aa"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	// Ps=0 is treated as the default (1), like xterm's one_if_default.
	term = tabTerm(t, 20, 4)
	feedText(t, term, "a\x1b[0b")
	if got, want := strings.TrimRight(term.Line(0), " "), "aa"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

// TestREP_AppliedOnce: REP ends the run of graphic characters, so a second REP
// has nothing to repeat (tmux behaves the same way; xterm would keep the
// character, but no program relies on chained REPs).
func TestREP_AppliedOnce(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "ab\x1b[2b\x1b[2b")
	if got, want := strings.TrimRight(term.Line(0), " "), "abbb"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_BeforeAnyText(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "\x1b[5b")
	if got := strings.TrimRight(term.Line(0), " "); got != "" {
		t.Fatalf("line = %q, want empty", got)
	}
	if got := term.CursorPos(); got != (Pos{X: 0, Y: 0}) {
		t.Fatalf("cursor = %+v, want origin", got)
	}
}

// TestREP_ClearedByEscape: an escape sequence between the character and REP
// invalidates the repeat (xterm only repeats when the preceding parsed item
// was a graphic character).
func TestREP_ClearedByEscape(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "A\x1b[0m\x1b[2b")
	if got := strings.TrimRight(term.Line(0), " "); got != "A" {
		t.Fatalf("line = %q, want %q", got, "A")
	}

	// A control character clears it too.
	term = tabTerm(t, 20, 4)
	feedText(t, term, "A\r\x1b[2b")
	if got := strings.TrimRight(term.Line(0), " "); got != "A" {
		t.Fatalf("line = %q, want %q", got, "A")
	}
}

// TestREP_RepeatedGlyphKeepsStyle: REP re-emits the last character with the
// attributes in effect, so a styled repeat keeps the style.
func TestREP_RepeatedGlyphKeepsStyle(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "\x1b[31mA\x1b[2b")

	ss := term.mainScreen.(*spanScreen)
	styled := ss.StyledLine(0, 20, 0)
	red := styleWith(ComponentFG, Color256(1))
	seen := 0
	for _, sp := range styled.Spans {
		if sp.Text == "" {
			continue
		}
		if sp.Style != red {
			t.Fatalf("cell style = %#v, want %#v", sp.Style, red)
		}
		seen += sp.Width
	}
	if seen != 3 {
		t.Fatalf("styled cells = %d, want 3", seen)
	}
}

func TestREP_UsesCurrentCharset(t *testing.T) {
	term := tabTerm(t, 20, 4)
	// REP stores the code point ('q') and re-emits it through the active
	// charset, so the repeats render as DEC graphics.
	feedText(t, term, "\x1b(0q\x1b[2b")
	if got, want := strings.TrimRight(term.Line(0), " "), "───"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_ClearedByCursorMove(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abc\x1b[1;1H\x1b[2b")
	if got, want := strings.TrimRight(term.Line(0), " "), "abc"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_WideChar(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "界\x1b[2b")
	if got, want := strings.TrimRight(term.Line(0), " "), "界界界"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_AfterCombiningMarkIsNoop(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "e\u0301\x1b[3b")
	if got, want := strings.TrimRight(term.Line(0), " "), "e\u0301"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestREP_Wraps(t *testing.T) {
	term := tabTerm(t, 5, 4)
	feedText(t, term, "abcde\x1b[b")
	if got := term.Line(1); !strings.HasPrefix(got, "e") {
		t.Fatalf("line 1 = %q, want it to start with e", got)
	}
}

func TestREP_SnapshotRestore(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "ab")
	snap := term.Snapshot()
	if snap.Main.LastChar != 'b' {
		t.Fatalf("snapshot LastChar = %q, want 'b'", snap.Main.LastChar)
	}

	other := tabTerm(t, 20, 4)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	feedText(t, other, "\x1b[2b")
	if got, want := strings.TrimRight(other.Line(0), " "), "abbb"; got != want {
		t.Fatalf("restored line = %q, want %q", got, want)
	}
}

// originTerm returns a terminal with scroll margins 5..10 (0-based 4..9) and
// origin mode enabled.
func originTerm(t *testing.T) *terminal {
	t.Helper()
	term := tabTerm(t, 20, 12)
	term.mustHandleCommand(t, "[5;10r")
	term.mustHandleCommand(t, "[?6h")
	return term
}

func TestDECOM_HomesToTopMargin(t *testing.T) {
	term := originTerm(t)
	if !term.mainScreen.OriginMode() {
		t.Fatal("origin mode not enabled")
	}
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 4}); got != want {
		t.Fatalf("cursor after DECOM = %+v, want %+v", got, want)
	}
}

func TestDECOM_RowsAreMarginRelative(t *testing.T) {
	term := originTerm(t)
	for _, tc := range []struct {
		seq  string
		want Pos
	}{
		{"\x1b[1;1H", Pos{X: 0, Y: 4}},
		{"\x1b[3;1H", Pos{X: 0, Y: 6}},
		{"\x1b[99;1H", Pos{X: 0, Y: 9}}, // clamped to the bottom margin
		{"\x1b[2d", Pos{X: 0, Y: 5}},    // VPA is margin-relative too
		{"\x1b[1;5f", Pos{X: 4, Y: 4}},  // HVP
	} {
		term.mustHandleCommand(t, "[H") // normal home is outside the margins here
		term.mustHandleCommand(t, "[?6h")
		feedText(t, term, tc.seq)
		if got := term.CursorPos(); got != tc.want {
			t.Fatalf("%q moved cursor to %+v, want %+v", tc.seq, got, tc.want)
		}
	}
}

func TestDECOM_ConfinesCursorToMargins(t *testing.T) {
	term := originTerm(t)
	feedText(t, term, "\x1b[1;1H\x1b[5A") // up from the top margin
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 4}); got != want {
		t.Fatalf("CUU left the region: %+v, want %+v", got, want)
	}
	feedText(t, term, "\x1b[99;1H\x1b[5B") // down from the bottom margin
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 9}); got != want {
		t.Fatalf("CUD left the region: %+v, want %+v", got, want)
	}
}

func TestDECOM_ResetHomesCursor(t *testing.T) {
	term := originTerm(t)
	feedText(t, term, "\x1b[3;1H")
	term.mustHandleCommand(t, "[?6l")
	if term.mainScreen.OriginMode() {
		t.Fatal("origin mode still enabled")
	}
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 0}); got != want {
		t.Fatalf("cursor after DECRST 6 = %+v, want %+v", got, want)
	}
	// Absolute addressing works again.
	feedText(t, term, "\x1b[3;1H")
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 2}); got != want {
		t.Fatalf("CUP after reset = %+v, want %+v", got, want)
	}
}

func TestDECOM_DECSTBMHomesToMargin(t *testing.T) {
	term := originTerm(t)
	term.mustHandleCommand(t, "[2;8r")
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 1}); got != want {
		t.Fatalf("DECSTBM home = %+v, want %+v", got, want)
	}
}

func TestDECOM_SavedAndRestoredByDECSC(t *testing.T) {
	term := originTerm(t)
	feedText(t, term, "\x1b[2;1H") // row 5 (margin-relative 2)
	term.mustHandleCommand(t, "7") // DECSC
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 5}); got != want {
		t.Fatalf("saved cursor = %+v, want %+v", got, want)
	}
	term.mustHandleCommand(t, "[?6l")
	if term.mainScreen.OriginMode() {
		t.Fatal("origin mode still enabled after reset")
	}
	term.mustHandleCommand(t, "8") // DECRC restores origin mode and position
	if !term.mainScreen.OriginMode() {
		t.Fatal("DECRC did not restore origin mode")
	}
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 5}); got != want {
		t.Fatalf("restored cursor = %+v, want %+v", got, want)
	}
}

func TestDECOM_Resets(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"RIS", "\x1bc"},
		{"DECSTR", "\x1b[!p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := originTerm(t)
			feedText(t, term, tc.seq)
			if term.mainScreen.OriginMode() {
				t.Fatalf("%s did not clear origin mode", tc.name)
			}
		})
	}
}

func TestDECOM_DECRQMReport(t *testing.T) {
	term := originTerm(t)
	if got := term.privateModeState(6); got != 1 {
		t.Fatalf("DECRQM ?6 set = %d, want 1", got)
	}
	term.mustHandleCommand(t, "[?6l")
	if got := term.privateModeState(6); got != 2 {
		t.Fatalf("DECRQM ?6 reset = %d, want 2", got)
	}
}

func TestDECOM_SnapshotRestore(t *testing.T) {
	term := originTerm(t)
	feedText(t, term, "\x1b[2;1H")
	term.mustHandleCommand(t, "7") // DECSC also saves origin mode
	snap := term.Snapshot()
	if !snap.Main.OriginMode || !snap.Main.SavedOriginMode {
		t.Fatalf("snapshot origin modes = %v/%v, want true/true",
			snap.Main.OriginMode, snap.Main.SavedOriginMode)
	}

	other := tabTerm(t, 20, 12)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if !other.mainScreen.OriginMode() {
		t.Fatal("restored terminal lost origin mode")
	}
	feedText(t, other, "\x1b[1;1H")
	if got, want := other.CursorPos(), (Pos{X: 0, Y: 4}); got != want {
		t.Fatalf("restored CUP = %+v, want %+v", got, want)
	}

	// The saved origin mode survives the snapshot too.
	other.mustHandleCommand(t, "[?6l")
	other.mustHandleCommand(t, "8")
	if !other.mainScreen.OriginMode() {
		t.Fatal("restored DECRC did not bring back origin mode")
	}
}

func TestDECOM_InsertModeResetByDECSTR(t *testing.T) {
	term := tabTerm(t, 20, 4)
	term.mustHandleCommand(t, "[4h")
	feedText(t, term, "\x1b[!p")
	if term.mainScreen.InsertMode() {
		t.Fatal("DECSTR did not clear insert mode")
	}
}
