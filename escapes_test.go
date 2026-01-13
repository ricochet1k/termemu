package termemu

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHandleCmdCSI_CursorMovement(t *testing.T) {
	t1, _ := MakeTerminalWithMock()

	t1.screen().setCursorPos(5, 5)

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[A"))) {
		t.Fatalf("handleCommand failed for [A")
	}
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 4 {
		t.Fatalf("expected cursor (5,4), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[2B"))) {
		t.Fatalf("handleCommand failed for [2B")
	}
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 6 {
		t.Fatalf("expected cursor (5,6), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[3C"))) {
		t.Fatalf("handleCommand failed for [3C")
	}
	if pos := t1.screen().CursorPos(); pos.X != 8 || pos.Y != 6 {
		t.Fatalf("expected cursor (8,6), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[4D"))) {
		t.Fatalf("handleCommand failed for [4D")
	}
	if pos := t1.screen().CursorPos(); pos.X != 4 || pos.Y != 6 {
		t.Fatalf("expected cursor (4,6), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[10G"))) {
		t.Fatalf("handleCommand failed for [10G")
	}
	if pos := t1.screen().CursorPos(); pos.X != 9 || pos.Y != 6 {
		t.Fatalf("expected cursor (9,6), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[7;12H"))) {
		t.Fatalf("handleCommand failed for [7;12H")
	}
	if pos := t1.screen().CursorPos(); pos.X != 11 || pos.Y != 6 {
		t.Fatalf("expected cursor (11,6), got (%d,%d)", pos.X, pos.Y)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[9;2f"))) {
		t.Fatalf("handleCommand failed for [9;2f")
	}
	if pos := t1.screen().CursorPos(); pos.X != 1 || pos.Y != 8 {
		t.Fatalf("expected cursor (1,8), got (%d,%d)", pos.X, pos.Y)
	}
}

func TestHandleCmdCSI_SetColors(t *testing.T) {
	// test that CSI m sequences update colors and notify frontend
	t1, mf := MakeTerminalWithMock()

	// send ESC [1;31;42m  (bold, fg=red, bg=green)
	r := bufio.NewReader(strings.NewReader("[1;31;42m"))
	ok := t1.handleCommand(r)
	if !ok {
		t.Fatalf("handleCommand returned false for CSI m sequence")
	}

	if len(mf.Colors) == 0 {
		t.Fatalf("ColorsChanged not called")
	}
	last := mf.Colors[len(mf.Colors)-1]
	if !last.F.TestMode(ModeBold) {
		t.Fatalf("expected FG to have ModeBold set")
	}
	if last.F.Color() != int(ColRed) {
		t.Fatalf("expected FG color red, got %d", last.F.Color())
	}
	if last.B.Color() != int(ColGreen) {
		t.Fatalf("expected BG color green, got %d", last.B.Color())
	}
}

func TestHandleCmdCSI_DefaultColors(t *testing.T) {
	t1, mf := MakeTerminalWithMock()

	// Set non-default colors first.
	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[31;42m"))) {
		t.Fatalf("handleCommand failed for [31;42m")
	}

	// Reset all attributes/colors.
	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[0m"))) {
		t.Fatalf("handleCommand failed for [0m")
	}

	last := mf.Colors[len(mf.Colors)-1]
	if last.F != ColDefault {
		t.Fatalf("expected FG default after reset, got %v", last.F)
	}
	if last.B != ColDefault {
		t.Fatalf("expected BG default after reset, got %v", last.B)
	}

	// Explicit default foreground/background.
	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[39m"))) {
		t.Fatalf("handleCommand failed for [39m")
	}
	last = mf.Colors[len(mf.Colors)-1]
	if last.F != ColDefault {
		t.Fatalf("expected FG default after 39m, got %v", last.F)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[49m"))) {
		t.Fatalf("handleCommand failed for [49m")
	}
	last = mf.Colors[len(mf.Colors)-1]
	if last.B != ColDefault {
		t.Fatalf("expected BG default after 49m, got %v", last.B)
	}
}

func TestHandleCmdCSI_SetModesAndFlags(t *testing.T) {
	// test ?25l and ?25h toggles show-cursor flag
	t1, mf := MakeTerminalWithMock()

	r1 := bufio.NewReader(strings.NewReader("[?25l"))
	if !t1.handleCommand(r1) {
		t.Fatalf("handleCommand failed for ?25l")
	}
	if v := mf.ViewFlags[VFShowCursor]; v != false {
		t.Fatalf("expected VFShowCursor false after ?25l, got %v", v)
	}

	r2 := bufio.NewReader(strings.NewReader("[?25h"))
	if !t1.handleCommand(r2) {
		t.Fatalf("handleCommand failed for ?25h")
	}
	if v := mf.ViewFlags[VFShowCursor]; v != true {
		t.Fatalf("expected VFShowCursor true after ?25h, got %v", v)
	}
}

func TestHandleCmdCSI_ModifyOtherKeys(t *testing.T) {
	t1, mf := MakeTerminalWithMock()

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[>4;2m"))) {
		t.Fatalf("handleCommand failed for >4;2m")
	}
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 2 {
		t.Fatalf("expected modifyOtherKeys 2, got %v", v)
	}

	if !t1.handleCommand(bufio.NewReader(strings.NewReader("[>4;0m"))) {
		t.Fatalf("handleCommand failed for >4;0m")
	}
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 0 {
		t.Fatalf("expected modifyOtherKeys 0, got %v", v)
	}
}

func TestHandleCmdOSC_WindowTitleAndStrings(t *testing.T) {
	t1, mf := MakeTerminalWithMock()

	// OSC sequence: ]0;title BEL
	r := bufio.NewReader(strings.NewReader("]0;mytitle"))
	// handleCommand expects to see ']' as the first rune
	ok := t1.handleCommand(r)
	if !ok {
		t.Fatalf("handleCommand failed for OSC sequence")
	}
	if mf.ViewStrings[VSWindowTitle] != "mytitle" {
		t.Fatalf("expected window title 'mytitle', got %q", mf.ViewStrings[VSWindowTitle])
	}
}

func TestSendMouseRaw_Encodings(t *testing.T) {
	t1, _ := MakeTerminalWithMock()

	// enable mouse reporting
	t1.viewInts[VIMouseMode] = MMPressReleaseMoveAll

	// create pipe to capture pty writes
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	t1.backend = NewNoPTYBackend(bytes.NewReader(nil), w)

	// X10 encoding
	t1.viewInts[VIMouseEncoding] = MEX10
	t1.SendMouseRaw(MBtn1, true, 0, 1, 2)
	buf := make([]byte, 16)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.HasPrefix(out, "\x1b[M") {
		t.Fatalf("unexpected X10 mouse seq: %q", out)
	}

	// UTF8 encoding
	t1.viewInts[VIMouseEncoding] = MEUTF8
	t1.SendMouseRaw(MBtn2, false, 0, 3, 4)
	n, _ = r.Read(buf)
	out = string(buf[:n])
	if !strings.HasPrefix(out, "\x1b[M") {
		t.Fatalf("unexpected UTF8 mouse seq: %q", out)
	}

	// SGR encoding
	t1.viewInts[VIMouseEncoding] = MESGR
	t1.SendMouseRaw(MBtn3, true, 0, 5, 6)
	// SGR writes longer string; read until newline-like end
	b2 := make([]byte, 64)
	n2, _ := r.Read(b2)
	out2 := string(b2[:n2])
	if !strings.HasPrefix(out2, "\x1b[<") {
		t.Fatalf("unexpected SGR mouse seq: %q", out2)
	}
}

func TestHandleCmdCSI_DeviceAttrsAndAlternateScreen(t *testing.T) {
	t1, mf := MakeTerminalWithMock()

	// Device attributes '>' should write to pty
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	t1.backend = NewNoPTYBackend(bytes.NewReader(nil), w)

	ok := t1.handleCommand(bufio.NewReader(strings.NewReader("[>c")))
	if !ok {
		t.Fatalf("handleCommand failed for >c")
	}
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "\x1b[>1;4402;0c") {
		t.Fatalf("device attrs not written: %q", out)
	}

	// Test alternate screen switch ?1049h
	// Reset regions
	mf.Regions = nil
	ok = t1.handleCommand(bufio.NewReader(strings.NewReader("[?1049h")))
	if !ok {
		t.Fatalf("handleCommand failed for ?1049h")
	}
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
		name    string
		sgr     string
		checkFG func(Color) bool
		checkBG func(Color) bool
	}{
		{
			name:    "SGR 6 - rapid blink",
			sgr:     "[6m",
			checkBG: func(c Color) bool { return c.TestMode(ModeRapidBlink) },
		},
		{
			name:    "SGR 9 - strikethrough",
			sgr:     "[9m",
			checkBG: func(c Color) bool { return c.TestMode(ModeStrike) },
		},
		{
			name:    "SGR 21 - double underline",
			sgr:     "[21m",
			checkBG: func(c Color) bool { return c.TestMode(ModeDoubleUnderline) },
		},
		{
			name:    "SGR 51 - framed",
			sgr:     "[51m",
			checkBG: func(c Color) bool { return c.TestMode(ModeFramed) },
		},
		{
			name:    "SGR 52 - encircled",
			sgr:     "[52m",
			checkBG: func(c Color) bool { return c.TestMode(ModeEncircled) },
		},
		{
			name:    "SGR 53 - overline",
			sgr:     "[53m",
			checkBG: func(c Color) bool { return c.TestMode(ModeOverline) },
		},
		{
			name:    "SGR 25 - reset rapid blink",
			sgr:     "[6;25m",
			checkBG: func(c Color) bool { return !c.TestMode(ModeRapidBlink) },
		},
		{
			name:    "SGR 29 - reset strikethrough",
			sgr:     "[9;29m",
			checkBG: func(c Color) bool { return !c.TestMode(ModeStrike) },
		},
		{
			name:    "SGR 24 - reset underline and double underline",
			sgr:     "[4;21;24m",
			checkFG: func(c Color) bool { return !c.TestMode(ModeUnderline) },
			checkBG: func(c Color) bool { return !c.TestMode(ModeDoubleUnderline) },
		},
		{
			name: "SGR 54 - reset framed and encircled",
			sgr:  "[51;52;54m",
			checkBG: func(c Color) bool {
				return !c.TestMode(ModeFramed) && !c.TestMode(ModeEncircled)
			},
		},
		{
			name:    "SGR 55 - reset overline",
			sgr:     "[53;55m",
			checkBG: func(c Color) bool { return !c.TestMode(ModeOverline) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			term, mf := MakeTerminalWithMock()

			// Send the SGR sequence
			ok := term.handleCommand(bufio.NewReader(strings.NewReader(tt.sgr)))
			if !ok {
				t.Fatalf("handleCommand returned false for SGR sequence %s", tt.sgr)
			}

			if len(mf.Colors) == 0 {
				t.Fatalf("ColorsChanged not called for SGR %s", tt.sgr)
			}

			last := mf.Colors[len(mf.Colors)-1]

			if tt.checkFG != nil && !tt.checkFG(last.F) {
				t.Errorf("FG check failed for SGR %s (FG=%#x)", tt.sgr, last.F)
			}

			if tt.checkBG != nil && !tt.checkBG(last.B) {
				t.Errorf("BG check failed for SGR %s (BG=%#x)", tt.sgr, last.B)
			}
		})
	}
}
