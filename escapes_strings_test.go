package termemu

import (
	"strings"
	"testing"
)

// feedESC feeds raw bytes starting with ESC through the normal read path.
func feedESC(t *testing.T, t1 *terminal, data string) {
	t.Helper()
	if err := t1.testFeedTerminalInputFromBackend([]byte(data), TextReadModeRune); err != nil {
		t.Fatalf("feed %q: %v", data, err)
	}
}

func lineBlank(t *testing.T, t1 *terminal, y int) string {
	t.Helper()
	return t1.Line(y)
}

func cursorPos(t1 *terminal) Pos {
	return t1.CursorPos()
}

func TestESC_AltScreen_1049_SetReset(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(10, 5)

	// ?1049h enters the alt screen.
	t1.mustHandleCommand(t, "[?1049h")
	if !t1.onAltScreen {
		t.Fatal("expected alt screen after ?1049h")
	}

	// Repeated ?1049h is idempotent: stays on alt, does not toggle back.
	t1.mustHandleCommand(t, "[?1049h")
	if !t1.onAltScreen {
		t.Fatal("repeated ?1049h toggled back to main screen")
	}

	// Move the alt cursor, then leave: main cursor must be restored.
	t1.screen().setCursorPos(3, 3)
	t1.mustHandleCommand(t, "[?1049l")
	if t1.onAltScreen {
		t.Fatal("expected main screen after ?1049l")
	}
	if pos := cursorPos(t1); pos.X != 10 || pos.Y != 5 {
		t.Fatalf("main cursor = (%d,%d), want (10,5) preserved across enter/exit", pos.X, pos.Y)
	}

	// ?1049l on the main screen is a no-op (must not enter alt).
	t1.mustHandleCommand(t, "[?1049l")
	if t1.onAltScreen {
		t.Fatal("?1049l on main screen entered the alt screen")
	}
	if pos := cursorPos(t1); pos.X != 10 || pos.Y != 5 {
		t.Fatalf("main cursor = (%d,%d), want still (10,5)", pos.X, pos.Y)
	}
}

func TestESC_AltScreen_1049_ClearsAlt(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Enter alt, leave a marker, exit.
	feedESC(t, t1, "\x1b[?1049h")
	feedESC(t, t1, "MARKER")
	if got := lineBlank(t, t1, 0); !strings.Contains(got, "MARKER") {
		t.Fatalf("setup: expected MARKER on alt screen, got %q", got)
	}
	t1.mustHandleCommand(t, "[?1049l")

	// Re-entering must show a cleared alt screen, not stale content.
	t1.mustHandleCommand(t, "[?1049h")
	if !t1.onAltScreen {
		t.Fatal("expected alt screen after re-enter")
	}
	if got := lineBlank(t, t1, 0); strings.Contains(got, "MARKER") {
		t.Fatalf("stale alt content reappeared after ?1049h: %q", got)
	}
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
		t.Fatalf("expected blank alt line after ?1049h, got %q", got)
	}
}

func TestESC_AltScreen_47_Preserves_1047_Clears(t *testing.T) {
	t.Run("47 preserves", func(t *testing.T) {
		_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
		t1.mustHandleCommand(t, "[?47h")
		feedESC(t, t1, "KEEPME")
		t1.mustHandleCommand(t, "[?47l")
		t1.mustHandleCommand(t, "[?47h")
		if got := lineBlank(t, t1, 0); !strings.Contains(got, "KEEPME") {
			t.Fatalf("?47 re-enter cleared alt content, line=%q", got)
		}
	})

	t.Run("1047 clears on exit", func(t *testing.T) {
		_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
		t1.mustHandleCommand(t, "[?1047h")
		feedESC(t, t1, "KEEPME")
		t1.mustHandleCommand(t, "[?1047l")
		t1.mustHandleCommand(t, "[?1047h")
		if got := lineBlank(t, t1, 0); strings.Contains(got, "KEEPME") {
			t.Fatalf("?1047 re-enter kept alt content, line=%q", got)
		}
	})
}

func TestESC_AltScreen_1047_NoCursorSaveRestore(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(10, 5)
	t1.mustHandleCommand(t, "[?1047h")
	// The switch itself moves nothing: moving on the alt screen changes the
	// shared cursor, and 1047 has no save/restore to undo it.
	t1.screen().setCursorPos(3, 3)
	t1.mustHandleCommand(t, "[?1047l")
	if pos := cursorPos(t1); pos.X != 3 || pos.Y != 3 {
		t.Fatalf("main cursor = (%d,%d), want carried over (3,3)", pos.X, pos.Y)
	}

	// Re-enter: the current cursor carries over again.
	t1.screen().setCursorPos(10, 5)
	t1.mustHandleCommand(t, "[?1047h")
	if pos := cursorPos(t1); pos.X != 10 || pos.Y != 5 {
		t.Fatalf("alt cursor = (%d,%d), want current main cursor (10,5)", pos.X, pos.Y)
	}
}

func TestESC_AltScreen_1048_SaveRestoreOnly(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(10, 5)
	t1.mustHandleCommand(t, "[?1048h")
	if t1.onAltScreen {
		t.Fatal("?1048h must not switch screens")
	}
	t1.screen().setCursorPos(0, 0)
	t1.mustHandleCommand(t, "[?1048l")
	if t1.onAltScreen {
		t.Fatal("?1048l must not switch screens")
	}
	if pos := cursorPos(t1); pos.X != 10 || pos.Y != 5 {
		t.Fatalf("cursor = (%d,%d), want restored (10,5)", pos.X, pos.Y)
	}
}

func TestESC_StringsSwallowed(t *testing.T) {
	tests := []struct {
		name string
		seq  string // full byte stream including leading ESC
	}{
		{"APC with 7-bit ST", "\x1b_payload\x1b\\"},
		{"APC with BEL", "\x1b_payload\x07"},
		{"APC with 8-bit ST", "\x1b_payload\x9c"},
		{"APC kitty-like payload", "\x1b_Gi=1;OK\x1b\\"},
		{"PM with 7-bit ST", "\x1b^payload\x1b\\"},
		{"PM with BEL", "\x1b^payload\x07"},
		{"SOS with 7-bit ST", "\x1bXpayload\x1b\\"},
		{"SOS with BEL", "\x1bXpayload\x07"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			feedESC(t, t1, tt.seq)
			if got := lineBlank(t, t1, 0); strings.Contains(got, "payload") || strings.Contains(got, "Gi=1") {
				t.Fatalf("string payload leaked to screen: %q", got)
			}
			if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
				t.Fatalf("expected blank line, got %q", got)
			}
			if strings.HasSuffix(tt.seq, "\x07") && mf.BellCount != 0 {
				t.Fatalf("BEL terminator rang bell %d times, must be consumed", mf.BellCount)
			}
		})
	}
}

func TestESC_TwoByteCommandsConsumed(t *testing.T) {
	tests := []struct {
		name string
		// cmd is passed to handleCommand, i.e. bytes after ESC (and after feed ESC).
		cmd string
	}{
		{"ESC % G (UTF-8 select)", "%G"},
		{"ESC % @", "%@"},
		{"ESC # other than 8", "#5"},
		{"ESC space arg", " F"},
		{"ESC - arg", "-A"},
		{"ESC . arg", ".A"},
		{"ESC / arg", "/A"},
		{"G0 charset", "(0"},
		{"G1 charset", ")B"},
		{"G2 charset", "*0"},
		{"G3 charset", "+0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			feedESC(t, t1, "\x1b"+tt.cmd)
			if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
				t.Fatalf("ESC %q printed %q to screen", tt.cmd, got)
			}
		})
	}
}

func TestESC_DECALN(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(10, 5)
	feedESC(t, t1, "\x1b#8")

	want := strings.Repeat("E", 80)
	for _, y := range []int{0, 5, 13} {
		if got := lineBlank(t, t1, y); got != want {
			t.Fatalf("line %d = %q, want 80 E's", y, got)
		}
	}
	if pos := cursorPos(t1); pos.X != 0 || pos.Y != 0 {
		t.Fatalf("cursor = (%d,%d), want homed (0,0) after DECALN", pos.X, pos.Y)
	}
}

func TestESC_DECSC_DECRC(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(10, 5)
	feedESC(t, t1, "\x1b7") // DECSC save
	t1.screen().setCursorPos(0, 0)
	feedESC(t, t1, "\x1b8") // DECRC restore
	if pos := cursorPos(t1); pos.X != 10 || pos.Y != 5 {
		t.Fatalf("cursor = (%d,%d), want restored (10,5)", pos.X, pos.Y)
	}
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
		t.Fatalf("ESC 7/8 printed to screen: %q", got)
	}
}

func TestESC_DECSC_DECRC_RestoresStyle(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[31;4m")
	feedESC(t, t1, "\x1b7") // DECSC save cursor + attributes
	t1.mustHandleCommand(t, "[0m")
	feedESC(t, t1, "\x1b8") // DECRC restore
	style := t1.screen().Style()
	if fg, _ := style.GetColor(ComponentFG); fg != Color256(1) {
		t.Fatalf("fg after DECRC = %v, want red", fg)
	}
	if !style.TestMode(ModeUnderline) {
		t.Fatal("underline not restored by DECRC")
	}
}

func TestESC_RIS(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Dirty state: style, margins, wrap, cursor, and alt screen.
	t1.mustHandleCommand(t, "[1;31m")
	t1.mustHandleCommand(t, "[5;10r")
	t1.mustHandleCommand(t, "[?7l")
	t1.mustHandleCommand(t, "[?1049h")
	feedESC(t, t1, "DIRTY")
	t1.screen().setCursorPos(9, 9)

	feedESC(t, t1, "\x1bc")

	if t1.onAltScreen {
		t.Fatal("RIS must return to the main screen")
	}
	if pos := cursorPos(t1); pos.X != 0 || pos.Y != 0 {
		t.Fatalf("cursor = (%d,%d), want homed (0,0) after RIS", pos.X, pos.Y)
	}
	if got := t1.screen().TopMargin(); got != 0 {
		t.Fatalf("TopMargin = %d, want 0 after RIS", got)
	}
	if got, want := t1.screen().BottomMargin(), t1.screen().Size().Y-1; got != want {
		t.Fatalf("BottomMargin = %d, want %d after RIS", got, want)
	}
	if !t1.screen().AutoWrap() {
		t.Fatal("AutoWrap = false, want default true after RIS")
	}
	if got, want := t1.screen().Style(), NewStyle(); got != want {
		t.Fatalf("style = %+v, want default %+v after RIS", got, want)
	}
	if got := lineBlank(t, t1, 0); strings.Contains(got, "DIRTY") || strings.TrimSpace(got) != "" {
		t.Fatalf("screen not cleared after RIS: %q", got)
	}
}

func TestOSC_Title_UTF8ContinuationIsNotST(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// Ü is 0xC3 0x9C. The 0x9C is a UTF-8 continuation byte and must not be
	// mistaken for an 8-bit ST terminator.
	feedESC(t, t1, "\x1b]2;A\xc3\x9cB\x1b\\")
	if got := mf.ViewStrings[VSWindowTitle]; got != "AÜB" {
		t.Fatalf("window title = %q, want %q", got, "AÜB")
	}
}

func TestDCS_UTF8ContinuationIsNotST(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// The payload contains 0xC3 0x9C. With correct continuation tracking the
	// whole DCS (through ESC \) is consumed and nothing is printed.
	feedESC(t, t1, "\x1bPpayload\xc3\x9cstill-payload\x1b\\")
	if got := lineBlank(t, t1, 0); strings.TrimSpace(got) != "" {
		t.Fatalf("DCS payload leaked to the screen: %q", got)
	}
}

func TestOSC_Standalone8BitSTStillTerminates(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	feedESC(t, t1, "\x1b]2;TITLE\x9cTAIL")
	if got := mf.ViewStrings[VSWindowTitle]; got != "TITLE" {
		t.Fatalf("window title = %q, want %q", got, "TITLE")
	}
	if got := lineBlank(t, t1, 0); !strings.Contains(got, "TAIL") {
		t.Fatalf("text after standalone 0x9C ST was lost, line = %q", got)
	}
}
