package termemu

import (
	"strings"
	"testing"
)

func TestHandleCmdCSI_CursorMovement(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(5, 5)

	t1.testHandleCommand(t, "[A")
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 4 {
		t.Fatalf("expected cursor (5,4), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[2B")
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 6 {
		t.Fatalf("expected cursor (5,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[3C")
	if pos := t1.screen().CursorPos(); pos.X != 8 || pos.Y != 6 {
		t.Fatalf("expected cursor (8,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[4D")
	if pos := t1.screen().CursorPos(); pos.X != 4 || pos.Y != 6 {
		t.Fatalf("expected cursor (4,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[10G")
	if pos := t1.screen().CursorPos(); pos.X != 9 || pos.Y != 6 {
		t.Fatalf("expected cursor (9,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[7;12H")
	if pos := t1.screen().CursorPos(); pos.X != 11 || pos.Y != 6 {
		t.Fatalf("expected cursor (11,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.testHandleCommand(t, "[9;2f")
	if pos := t1.screen().CursorPos(); pos.X != 1 || pos.Y != 8 {
		t.Fatalf("expected cursor (1,8), got (%d,%d)", pos.X, pos.Y)
	}
}

func TestHandleCmdCSI_SetColors(t *testing.T) {
	// test that CSI m sequences update colors and notify frontend
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// send ESC [1;31;42m  (bold, fg=red, bg=green)
	t1.testHandleCommand(t, "[1;31;42m")
	if len(mf.Styles) == 0 {
		t.Fatalf("StyleChanged not called")
	}
	last := mf.Styles[len(mf.Styles)-1]
	if !last.TestMode(ModeBold) {
		t.Fatalf("expected to have ModeBold set")
	}
	fgColor, _, _ := last.GetColor(ComponentFG)
	if fgColor != int(ColRed) {
		t.Fatalf("expected FG color red, got %d", fgColor)
	}
	bgColor, _, _ := last.GetColor(ComponentBG)
	if bgColor != int(ColGreen) {
		t.Fatalf("expected BG color green, got %d", bgColor)
	}
}

func TestHandleCmdCSI_DefaultColors(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// Set non-default colors first.
	t1.testHandleCommand(t, "[31;42m")
	// Reset all attributes/colors.
	t1.testHandleCommand(t, "[0m")
	last := mf.Styles[len(mf.Styles)-1]
	fgColor, _, _ := last.GetColor(ComponentFG)
	if fgColor != 0 {
		t.Fatalf("expected FG default after reset, got %d", fgColor)
	}
	bgColor, _, _ := last.GetColor(ComponentBG)
	if bgColor != 0 {
		t.Fatalf("expected BG default after reset, got %d", bgColor)
	}

	// Explicit default foreground/background.
	t1.testHandleCommand(t, "[39m")
	last = mf.Styles[len(mf.Styles)-1]
	fgColor, _, _ = last.GetColor(ComponentFG)
	if fgColor != 0 {
		t.Fatalf("expected FG default after 39m, got %d", fgColor)
	}

	t1.testHandleCommand(t, "[49m")
	last = mf.Styles[len(mf.Styles)-1]
	bgColor, _, _ = last.GetColor(ComponentBG)
	if bgColor != 0 {
		t.Fatalf("expected BG default after 49m, got %d", bgColor)
	}
}

func TestHandleCmdCSI_SetModesAndFlags(t *testing.T) {
	// test ?25l and ?25h toggles show-cursor flag
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.testHandleCommand(t, "[?25l")
	if v := mf.ViewFlags[VFShowCursor]; v != false {
		t.Fatalf("expected VFShowCursor false after ?25l, got %v", v)
	}

	t1.testHandleCommand(t, "[?25h")
	if v := mf.ViewFlags[VFShowCursor]; v != true {
		t.Fatalf("expected VFShowCursor true after ?25h, got %v", v)
	}
}

func TestHandleCmdCSI_ModifyOtherKeys(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.testHandleCommand(t, "[>4;2m")
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 2 {
		t.Fatalf("expected modifyOtherKeys 2, got %v", v)
	}

	t1.testHandleCommand(t, "[>4;0m")
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 0 {
		t.Fatalf("expected modifyOtherKeys 0, got %v", v)
	}
}

func TestHandleCmdOSC_WindowTitleAndStrings(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// OSC sequence: ]0;title BEL
	t1.testHandleCommand(t, "]0;mytitle")
	if mf.ViewStrings[VSWindowTitle] != "mytitle" {
		t.Fatalf("expected window title 'mytitle', got %q", mf.ViewStrings[VSWindowTitle])
	}
}

func TestSendMouseRaw_Encodings(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// enable mouse reporting
	t1.viewInts[VIMouseMode] = MMPressReleaseMoveAll

	// X10 encoding
	t1.viewInts[VIMouseEncoding] = MEX10
	go t1.SendMouseRaw(MBtn1, true, 0, 1, 2)
	buf := make([]byte, 16)
	n, _ := r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[M") {
		t.Fatalf("unexpected X10 mouse seq: %q", string(buf[:n]))
	}

	// UTF8 encoding
	t1.viewInts[VIMouseEncoding] = MEUTF8
	go t1.SendMouseRaw(MBtn2, false, 0, 3, 4)
	n, _ = r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[M") {
		t.Fatalf("unexpected UTF8 mouse seq: %q", string(buf[:n]))
	}

	// SGR encoding
	t1.viewInts[VIMouseEncoding] = MESGR
	go t1.SendMouseRaw(MBtn3, true, 0, 5, 6)
	// SGR writes longer string; read until newline-like end
	n, _ = r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[<") {
		t.Fatalf("unexpected SGR mouse seq: %q", string(buf[:n]))
	}
}

func TestHandleCmdCSI_DeviceAttrsAndAlternateScreen(t *testing.T) {
	r, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// Run in goroutine because device attrs query writes response to backend
	go t1.testHandleCommand(t, "[>c")
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "\x1b[>1;4402;0c") {
		t.Fatalf("device attrs not written: %q", string(buf[:n]))
	}

	// Test alternate screen switch ?1049h
	// Reset regions
	mf.Regions = nil
	t1.testHandleCommand(t, "[?1049h")
	found := false
	for _, reg := range mf.Regions {
		if reg.C == CRScreenSwitch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CRScreenSwitch RegionChanged for ?1049h")
	}
}

func TestHandleCmdCSI_NewSGRModes(t *testing.T) {
	// Test new SGR modes: 6 (rapid blink), 9 (strikethrough), 21 (double underline),
	// 51 (framed), 52 (encircled), 53 (overline)
	tests := []struct {
		name  string
		sgr   string
		check func(Style) bool
	}{
		{
			name:  "SGR 6 - rapid blink",
			sgr:   "[6m",
			check: func(s Style) bool { return s.TestMode(ModeRapidBlink) },
		},
		{
			name:  "SGR 9 - strikethrough",
			sgr:   "[9m",
			check: func(s Style) bool { return s.TestMode(ModeStrike) },
		},
		{
			name:  "SGR 21 - double underline",
			sgr:   "[21m",
			check: func(s Style) bool { return s.TestMode(ModeDoubleUnderline) },
		},
		{
			name:  "SGR 51 - framed",
			sgr:   "[51m",
			check: func(s Style) bool { return s.TestMode(ModeFramed) },
		},
		{
			name:  "SGR 52 - encircled",
			sgr:   "[52m",
			check: func(s Style) bool { return s.TestMode(ModeEncircled) },
		},
		{
			name:  "SGR 53 - overline",
			sgr:   "[53m",
			check: func(s Style) bool { return s.TestMode(ModeOverline) },
		},
		{
			name:  "SGR 25 - reset rapid blink",
			sgr:   "[6;25m",
			check: func(s Style) bool { return !s.TestMode(ModeRapidBlink) },
		},
		{
			name:  "SGR 29 - reset strikethrough",
			sgr:   "[9;29m",
			check: func(s Style) bool { return !s.TestMode(ModeStrike) },
		},
		{
			name: "SGR 24 - reset underline and double underline",
			sgr:  "[4;21;24m",
			check: func(s Style) bool {
				return !s.TestMode(ModeUnderline) && !s.TestMode(ModeDoubleUnderline)
			},
		},
		{
			name: "SGR 54 - reset framed and encircled",
			sgr:  "[51;52;54m",
			check: func(s Style) bool {
				return !s.TestMode(ModeFramed) && !s.TestMode(ModeEncircled)
			},
		},
		{
			name:  "SGR 55 - reset overline",
			sgr:   "[53;55m",
			check: func(s Style) bool { return !s.TestMode(ModeOverline) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			// Send the SGR sequence
			t1.testHandleCommand(t, tt.sgr)
			if len(mf.Styles) == 0 {
				t.Fatalf("StyleChanged not called for SGR %s", tt.sgr)
			}

			last := mf.Styles[len(mf.Styles)-1]

			if tt.check != nil && !tt.check(last) {
				t.Errorf("check failed for SGR %s", tt.sgr)
			}
		})
	}
}
