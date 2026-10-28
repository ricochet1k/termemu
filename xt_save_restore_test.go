package termemu

import (
	"strconv"
	"testing"
)

// TestXTSaveRestoreModes checks that XTSAVE (CSI ? Ps s) and XTRESTORE
// (CSI ? Ps r) store and re-apply DEC private mode state using the same
// code path as DECSET/DECRST.
func TestXTSaveRestoreModes(t *testing.T) {
	tests := []struct {
		name      string
		enableSeq string
		mode      int
		get       func(*terminal) bool
	}{
		{
			name:      "application cursor keys",
			enableSeq: "[?1h",
			mode:      1,
			get:       func(t *terminal) bool { return t.viewFlags[VFAppCursorKeys] },
		},
		{
			name:      "origin mode",
			enableSeq: "[?6h",
			mode:      6,
			get:       func(t *terminal) bool { return t.screen().OriginMode() },
		},
		{
			name:      "autowrap",
			enableSeq: "[?7h",
			mode:      7,
			get:       func(t *terminal) bool { return t.screen().AutoWrap() },
		},
		{
			name:      "cursor visible",
			enableSeq: "[?25h",
			mode:      25,
			get:       func(t *terminal) bool { return t.viewFlags[VFShowCursor] },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, term, _ := MakeTerminalWithMock(TextReadModeRune)

			// Start with the mode reset.
			term.mustHandleCommand(t, tt.enableSeq[:len(tt.enableSeq)-1]+"l")
			if tt.get(term) {
				t.Fatalf("mode %d did not start reset", tt.mode)
			}

			// Save the reset state.
			term.mustHandleCommand(t, "[?"+strconv.Itoa(tt.mode)+"s")
			// Enable it.
			term.mustHandleCommand(t, tt.enableSeq)
			if !tt.get(term) {
				t.Fatalf("mode %d did not enable", tt.mode)
			}
			// Restore should return it to reset.
			term.mustHandleCommand(t, "[?"+strconv.Itoa(tt.mode)+"r")
			if tt.get(term) {
				t.Fatalf("mode %d was not restored to reset", tt.mode)
			}

			// Save the set state, disable, restore -> set.
			term.mustHandleCommand(t, tt.enableSeq)
			term.mustHandleCommand(t, "[?"+strconv.Itoa(tt.mode)+"s")
			term.mustHandleCommand(t, tt.enableSeq[:len(tt.enableSeq)-1]+"l")
			if tt.get(term) {
				t.Fatalf("mode %d did not disable", tt.mode)
			}
			term.mustHandleCommand(t, "[?"+strconv.Itoa(tt.mode)+"r")
			if !tt.get(term) {
				t.Fatalf("mode %d was not restored to set", tt.mode)
			}
		})
	}
}

// TestXTSaveRestoreNoSave verifies that restoring a mode that was never
// saved is a no-op.
func TestXTSaveRestoreNoSave(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	term.mustHandleCommand(t, "[?25l")
	if term.viewFlags[VFShowCursor] {
		t.Fatal("cursor should be hidden")
	}

	// Restore without ever saving should do nothing.
	term.mustHandleCommand(t, "[?25r")
	if term.viewFlags[VFShowCursor] {
		t.Fatal("cursor should still be hidden")
	}
}

// TestXTSaveRestoreMultipleModes checks that several modes can be saved and
// restored in a single sequence.
func TestXTSaveRestoreMultipleModes(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	// Save the default-ish state: cursor visible, normal cursor keys.
	term.mustHandleCommand(t, "[?1;25s")

	// Change both.
	term.mustHandleCommand(t, "[?1h")
	term.mustHandleCommand(t, "[?25l")
	if !term.viewFlags[VFAppCursorKeys] {
		t.Fatal("app cursor keys should be on")
	}
	if term.viewFlags[VFShowCursor] {
		t.Fatal("cursor should be hidden")
	}

	// Restore both at once.
	term.mustHandleCommand(t, "[?1;25r")
	if term.viewFlags[VFAppCursorKeys] {
		t.Fatal("app cursor keys should be restored off")
	}
	if !term.viewFlags[VFShowCursor] {
		t.Fatal("cursor should be restored visible")
	}
}

// TestXTSaveRestoreOverwrite verifies that re-saving a mode overwrites the
// previous saved value.
func TestXTSaveRestoreOverwrite(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	// Save reset state.
	term.mustHandleCommand(t, "[?25l")
	term.mustHandleCommand(t, "[?25s")

	// Enable and re-save the set state.
	term.mustHandleCommand(t, "[?25h")
	term.mustHandleCommand(t, "[?25s")

	// Disable, then restore should bring back the most recently saved state.
	term.mustHandleCommand(t, "[?25l")
	term.mustHandleCommand(t, "[?25r")
	if !term.viewFlags[VFShowCursor] {
		t.Fatal("cursor should be visible after restoring overwritten save")
	}
}

// TestSCOSCKeepSaveCursor ensures CSI s without the '?' prefix still saves
// the cursor position and is not interpreted as XTSAVE.
func TestSCOSCKeepSaveCursor(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	term.screen().setCursorPos(3, 4)
	term.mustHandleCommand(t, "[s")

	term.screen().setCursorPos(0, 0)
	term.mustHandleCommand(t, "[u")

	pos := term.screen().CursorPos()
	if pos.X != 3 || pos.Y != 4 {
		t.Fatalf("expected cursor (3,4), got (%d,%d)", pos.X, pos.Y)
	}
}

// TestXTSaveRestore1049 checks that saving and restoring the alt-screen
// family mode 1049 behaves the same as setting and resetting it directly.
func TestXTSaveRestore1049(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	if term.onAltScreen {
		t.Fatal("should start on main screen")
	}

	// Save main-screen state, enter alt screen, restore -> back to main.
	term.mustHandleCommand(t, "[?1049s")
	term.mustHandleCommand(t, "[?1049h")
	if !term.onAltScreen {
		t.Fatal("should be on alt screen")
	}
	term.mustHandleCommand(t, "[?1049r")
	if term.onAltScreen {
		t.Fatal("should be back on main screen")
	}

	// Save alt-screen state, leave, restore -> back to alt.
	term.mustHandleCommand(t, "[?1049h")
	term.mustHandleCommand(t, "[?1049s")
	term.mustHandleCommand(t, "[?1049l")
	if term.onAltScreen {
		t.Fatal("should be on main screen")
	}
	term.mustHandleCommand(t, "[?1049r")
	if !term.onAltScreen {
		t.Fatal("should be back on alt screen")
	}
}
