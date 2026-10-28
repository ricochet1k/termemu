package termemu

import (
	"strings"
	"testing"
)

func TestHandleCmdCSI_CursorMovement(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	t1.screen().setCursorPos(5, 5)

	t1.mustHandleCommand(t, "[A")
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 4 {
		t.Fatalf("expected cursor (5,4), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[2B")
	if pos := t1.screen().CursorPos(); pos.X != 5 || pos.Y != 6 {
		t.Fatalf("expected cursor (5,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[3C")
	if pos := t1.screen().CursorPos(); pos.X != 8 || pos.Y != 6 {
		t.Fatalf("expected cursor (8,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[4D")
	if pos := t1.screen().CursorPos(); pos.X != 4 || pos.Y != 6 {
		t.Fatalf("expected cursor (4,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[10G")
	if pos := t1.screen().CursorPos(); pos.X != 9 || pos.Y != 6 {
		t.Fatalf("expected cursor (9,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[7;12H")
	if pos := t1.screen().CursorPos(); pos.X != 11 || pos.Y != 6 {
		t.Fatalf("expected cursor (11,6), got (%d,%d)", pos.X, pos.Y)
	}

	t1.mustHandleCommand(t, "[9;2f")
	if pos := t1.screen().CursorPos(); pos.X != 1 || pos.Y != 8 {
		t.Fatalf("expected cursor (1,8), got (%d,%d)", pos.X, pos.Y)
	}
}

func TestHandleCmdCSI_SetColors(t *testing.T) {
	// test that CSI m sequences update colors and notify frontend
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// send ESC [1;31;42m  (bold, fg=red, bg=green)
	t1.mustHandleCommand(t, "[1;31;42m")
	if len(mf.Styles) == 0 {
		t.Fatalf("StyleChanged not called")
	}
	last := mf.Styles[len(mf.Styles)-1]
	if !last.TestMode(ModeBold) {
		t.Fatalf("expected to have ModeBold set")
	}
	fgColor, _ := last.GetColor(ComponentFG)
	if fgColor != Color256(ColRed) {
		t.Fatalf("expected FG color red, got %v", fgColor)
	}
	bgColor, _ := last.GetColor(ComponentBG)
	if bgColor != Color256(ColGreen) {
		t.Fatalf("expected BG color green, got %v", bgColor)
	}
}

func TestHandleCmdCSI_DefaultColors(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// Set non-default colors first.
	t1.mustHandleCommand(t, "[31;42m")
	// Reset all attributes/colors.
	t1.mustHandleCommand(t, "[0m")
	last := mf.Styles[len(mf.Styles)-1]
	fgColor, _ := last.GetColor(ComponentFG)
	if fgColor != DefaultColor() {
		t.Fatalf("expected FG default after reset, got %v", fgColor)
	}
	bgColor, _ := last.GetColor(ComponentBG)
	if bgColor != DefaultColor() {
		t.Fatalf("expected BG default after reset, got %v", bgColor)
	}

	// Explicit default foreground/background.
	t1.mustHandleCommand(t, "[39m")
	last = mf.Styles[len(mf.Styles)-1]
	fgColor, _ = last.GetColor(ComponentFG)
	if fgColor != DefaultColor() {
		t.Fatalf("expected FG default after 39m, got %v", fgColor)
	}

	t1.mustHandleCommand(t, "[49m")
	last = mf.Styles[len(mf.Styles)-1]
	bgColor, _ = last.GetColor(ComponentBG)
	if bgColor != DefaultColor() {
		t.Fatalf("expected BG default after 49m, got %v", bgColor)
	}
}

func TestHandleCmdCSI_SetModesAndFlags(t *testing.T) {
	// test ?25l and ?25h toggles show-cursor flag
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[?25l")
	if v := mf.ViewFlags[VFShowCursor]; v != false {
		t.Fatalf("expected VFShowCursor false after ?25l, got %v", v)
	}

	t1.mustHandleCommand(t, "[?25h")
	if v := mf.ViewFlags[VFShowCursor]; v != true {
		t.Fatalf("expected VFShowCursor true after ?25h, got %v", v)
	}
}

func TestHandleCmdCSI_ModifyOtherKeys(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	t1.mustHandleCommand(t, "[>4;2m")
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 2 {
		t.Fatalf("expected modifyOtherKeys 2, got %v", v)
	}

	t1.mustHandleCommand(t, "[>4;0m")
	if v := mf.ViewInts[VIModifyOtherKeys]; v != 0 {
		t.Fatalf("expected modifyOtherKeys 0, got %v", v)
	}
}

func TestHandleCmdOSC_WindowTitleAndStrings(t *testing.T) {
	_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// OSC sequence: ]0;title BEL
	t1.mustHandleCommand(t, "]0;mytitle\a")
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
	go func() { _ = t1.SendMouseRaw(MBtn1, true, 0, 1, 2) }()
	buf := make([]byte, 16)
	n, _ := r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[M") {
		t.Fatalf("unexpected X10 mouse seq: %q", string(buf[:n]))
	}

	// UTF8 encoding
	t1.viewInts[VIMouseEncoding] = MEUTF8
	go func() { _ = t1.SendMouseRaw(MBtn2, false, 0, 3, 4) }()
	n, _ = r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[M") {
		t.Fatalf("unexpected UTF8 mouse seq: %q", string(buf[:n]))
	}

	// SGR encoding
	t1.viewInts[VIMouseEncoding] = MESGR
	go func() { _ = t1.SendMouseRaw(MBtn3, true, 0, 5, 6) }()
	// SGR writes longer string; read until newline-like end
	n, _ = r.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "\x1b[<") {
		t.Fatalf("unexpected SGR mouse seq: %q", string(buf[:n]))
	}

	// urxvt encoding: CSI button;x;yM with 32+button.
	t1.viewInts[VIMouseEncoding] = MEURXVT
	go func() { _ = t1.SendMouseRaw(MBtn1, true, MControl, 7, 8) }()
	n, _ = r.Read(buf)
	if got := string(buf[:n]); got != "\x1b[48;7;8M" {
		t.Fatalf("unexpected urxvt mouse seq: %q, want %q", got, "\x1b[48;7;8M")
	}

	// Release uses button 3 (MRelease), as in X10.
	go func() { _ = t1.SendMouseRaw(MBtn1, false, 0, 7, 8) }()
	n, _ = r.Read(buf)
	if got := string(buf[:n]); got != "\x1b[35;7;8M" {
		t.Fatalf("unexpected urxvt release seq: %q, want %q", got, "\x1b[35;7;8M")
	}
}

func TestHandleCmdCSI_DeviceAttrsAndAlternateScreen(t *testing.T) {
	r, t1, mf := MakeTerminalWithMock(TextReadModeRune)

	// Run in goroutine because device attrs query writes response to backend
	go func() { _ = t1.testHandleCommand(t, "[>c") }()
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "\x1b[>1;4402;0c") {
		t.Fatalf("device attrs not written: %q", string(buf[:n]))
	}

	// Test alternate screen switch ?1049h
	// Reset regions
	mf.Regions = nil
	t1.mustHandleCommand(t, "[?1049h")
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
			t1.mustHandleCommand(t, tt.sgr)
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

func TestCSI_PrivateModes(t *testing.T) {
	tests := []struct {
		name      string
		seq       string
		checkFlag *ViewFlag
		wantFlag  bool
		checkWrap bool
		wantWrap  bool
	}{
		// Application cursor keys
		{"?1h enables app cursor keys", "[?1h", &[]ViewFlag{VFAppCursorKeys}[0], true, false, false},
		{"?1l disables app cursor keys", "[?1l", &[]ViewFlag{VFAppCursorKeys}[0], false, false, false},
		// Auto-wrap
		{"?7h enables auto-wrap", "[?7h", nil, false, true, true},
		{"?7l disables auto-wrap", "[?7l", nil, false, true, false},
		// Blink cursor
		{"?12h enables blink cursor", "[?12h", &[]ViewFlag{VFBlinkCursor}[0], true, false, false},
		{"?12l disables blink cursor", "[?12l", &[]ViewFlag{VFBlinkCursor}[0], false, false, false},
		// Bracketed paste
		{"?2004h enables bracketed paste", "[?2004h", &[]ViewFlag{VFBracketedPaste}[0], true, false, false},
		{"?2004l disables bracketed paste", "[?2004l", &[]ViewFlag{VFBracketedPaste}[0], false, false, false},
		// Report focus
		{"?1004h enables report focus", "[?1004h", &[]ViewFlag{VFReportFocus}[0], true, false, false},
		{"?1004l disables report focus", "[?1004l", &[]ViewFlag{VFReportFocus}[0], false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.seq)

			if tt.checkFlag != nil {
				if got := mf.ViewFlags[*tt.checkFlag]; got != tt.wantFlag {
					t.Errorf("ViewFlags[%v] = %v, want %v", *tt.checkFlag, got, tt.wantFlag)
				}
			}
			if tt.checkWrap {
				if got := t1.screen().AutoWrap(); got != tt.wantWrap {
					t.Errorf("AutoWrap() = %v, want %v", got, tt.wantWrap)
				}
			}
		})
	}
}

func TestCSI_MouseModes(t *testing.T) {
	tests := []struct {
		name     string
		seq      string
		wantMode int
	}{
		// Mouse XY on press (?9)
		{"?9h enables mouse press", "[?9h", MMPress},
		{"?9l disables mouse", "[?9l", MMNone},
		// Press/release (?1000)
		{"?1000h enables press/release", "[?1000h", MMPressRelease},
		{"?1000l disables mouse", "[?1000l", MMNone},
		// Cell motion (?1002)
		{"?1002h enables cell motion", "[?1002h", MMPressReleaseMove},
		{"?1002l disables mouse", "[?1002l", MMNone},
		// All motion (?1003)
		{"?1003h enables all motion", "[?1003h", MMPressReleaseMoveAll},
		{"?1003l disables mouse", "[?1003l", MMNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.seq)

			if got := mf.ViewInts[VIMouseMode]; got != tt.wantMode {
				t.Errorf("ViewInts[VIMouseMode] = %v, want %v", got, tt.wantMode)
			}
		})
	}
}

func TestCSI_MouseEncoding(t *testing.T) {
	tests := []struct {
		name         string
		seq          string
		wantEncoding int
	}{
		// UTF-8 encoding (?1005)
		{"?1005h enables UTF-8 encoding", "[?1005h", MEUTF8},
		{"?1005l resets to X10", "[?1005l", MEX10},
		// SGR encoding (?1006)
		{"?1006h enables SGR encoding", "[?1006h", MESGR},
		{"?1006l resets to X10", "[?1006l", MEX10},
		// urxvt mode (?1015)
		{"?1015h enables urxvt encoding", "[?1015h", MEURXVT},
		{"?1015l resets to X10", "[?1015l", MEX10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.seq)

			if got := mf.ViewInts[VIMouseEncoding]; got != tt.wantEncoding {
				t.Errorf("ViewInts[VIMouseEncoding] = %v, want %v", got, tt.wantEncoding)
			}
		})
	}
}

func TestCSI_DeviceStatusReport(t *testing.T) {
	tests := []struct {
		name     string
		seq      string
		wantResp string
		cursorX  int
		cursorY  int
	}{
		{"5n operating status", "[5n", "\x1b[0n", 0, 0},
		{"6n cursor position at origin", "[6n", "\x1b[1;1R", 0, 0},
		{"6n cursor position at 10,5", "[6n", "\x1b[6;11R", 10, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

			if tt.cursorX != 0 || tt.cursorY != 0 {
				t1.screen().setCursorPos(tt.cursorX, tt.cursorY)
			}

			go func() { _ = t1.testHandleCommand(t, tt.seq) }()

			buf := make([]byte, 64)
			n, err := r.Read(buf)
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}
			got := string(buf[:n])
			if got != tt.wantResp {
				t.Errorf("response = %q, want %q", got, tt.wantResp)
			}
		})
	}
}

func TestCSI_CUUAlias_k(t *testing.T) {
	tests := []struct {
		name   string
		seq    string
		startY int
		wantY  int
	}{
		{"no param moves 1", "[k", 5, 4},
		{"param moves N", "[3k", 5, 2},
		{"param 0 treated as 1", "[0k", 5, 4},
		{"clamps at top margin", "[10k", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(5, tt.startY)
			t1.mustHandleCommand(t, tt.seq)
			pos := t1.screen().CursorPos()
			if pos.X != 5 || pos.Y != tt.wantY {
				t.Errorf("cursor = (%d,%d), want (%d,%d)", pos.X, pos.Y, 5, tt.wantY)
			}
		})
	}
}

func TestCSI_HPA(t *testing.T) {
	tests := []struct {
		name   string
		seq    string
		startX int
		wantX  int
	}{
		{"no param goes to col 1", "[`", 5, 0},
		{"param sets col", "[10`", 5, 9},
		{"param 0 treated as 1", "[0`", 5, 0},
		{"clamps to width", "[1000`", 5, 79},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(tt.startX, 5)
			t1.mustHandleCommand(t, tt.seq)
			pos := t1.screen().CursorPos()
			if pos.X != tt.wantX || pos.Y != 5 {
				t.Errorf("cursor = (%d,%d), want (%d,%d)", pos.X, pos.Y, tt.wantX, 5)
			}
		})
	}
}

func TestCSI_HPA_ClearsPendingWrap(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Move to the last column, write a character to set pending wrap, then
	// use HPA to column 1 and write another character. If HPA cleared pending
	// wrap, the second character stays on the first row.
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[80GX\x1b[1`Z"), TextReadModeRune); err != nil {
		t.Fatalf("feed error: %v", err)
	}
	pos := t1.screen().CursorPos()
	if pos.X != 1 || pos.Y != 0 {
		t.Errorf("cursor = (%d,%d), want (1,0); pending wrap was not cleared", pos.X, pos.Y)
	}
}

func TestCSI_DECRQM(t *testing.T) {
	tests := []struct {
		name     string
		prep     string
		seq      string
		wantResp string
	}{
		{"?117 permanently reset", "", "[?117$p", "\x1b[?117;4$y"},
		{"ANSI 1 permanently reset", "", "[1$p", "\x1b[1;4$y"},
		{"ANSI 5 permanently reset", "", "[5$p", "\x1b[5;4$y"},
		{"ANSI 19 permanently reset", "", "[19$p", "\x1b[19;4$y"},
		{"ANSI 9 not recognized", "", "[9$p", "\x1b[9;0$y"},
		{"ANSI 4 IRM set", "[4h", "[4$p", "\x1b[4;1$y"},
		{"ANSI 4 IRM reset", "[4l", "[4$p", "\x1b[4;2$y"},
		{"?25 set", "[?25h", "[?25$p", "\x1b[?25;1$y"},
		{"?25 reset", "[?25l", "[?25$p", "\x1b[?25;2$y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

			if tt.prep != "" {
				t1.mustHandleCommand(t, tt.prep)
			}

			go func() { _ = t1.testHandleCommand(t, tt.seq) }()

			buf := make([]byte, 64)
			n, err := r.Read(buf)
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}
			got := string(buf[:n])
			if got != tt.wantResp {
				t.Errorf("response = %q, want %q", got, tt.wantResp)
			}
		})
	}
}

func TestCSI_DeviceAttributes(t *testing.T) {
	tests := []struct {
		name        string
		seq         string
		wantContain string
	}{
		// Note: [c without params defaults to 1, but only case 0 writes response
		// So only [0c triggers primary DA response
		{"primary DA with [0c", "[0c", "\x1b[?"},
		{"secondary DA with [>c", "[>c", "\x1b[>1;4402;0c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

			go func() { _ = t1.testHandleCommand(t, tt.seq) }()

			buf := make([]byte, 64)
			n, err := r.Read(buf)
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}
			got := string(buf[:n])
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("response = %q, want to contain %q", got, tt.wantContain)
			}
		})
	}
}

func TestCSI_SaveRestoreCursor(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Set cursor to (10, 5)
	t1.screen().setCursorPos(10, 5)
	pos := t1.screen().CursorPos()
	if pos.X != 10 || pos.Y != 5 {
		t.Fatalf("initial cursor position = (%d,%d), want (10,5)", pos.X, pos.Y)
	}

	// Save cursor position
	t1.mustHandleCommand(t, "[s")

	// Move cursor to (0, 0)
	t1.screen().setCursorPos(0, 0)
	pos = t1.screen().CursorPos()
	if pos.X != 0 || pos.Y != 0 {
		t.Fatalf("cursor after move = (%d,%d), want (0,0)", pos.X, pos.Y)
	}

	// Restore cursor position
	t1.mustHandleCommand(t, "[u")

	// Verify back at (10, 5)
	pos = t1.screen().CursorPos()
	if pos.X != 10 || pos.Y != 5 {
		t.Errorf("cursor after restore = (%d,%d), want (10,5)", pos.X, pos.Y)
	}
}

func TestCSI_SGR_BasicModes(t *testing.T) {
	tests := []struct {
		name     string
		sgr      string
		checkFn  func(Style) bool
		describe string
	}{
		{
			name:     "reset all (no param)",
			sgr:      "[m",
			checkFn:  func(s Style) bool { return len(s.Modes()) == 0 },
			describe: "all modes should be cleared",
		},
		{
			name:     "bold",
			sgr:      "[1m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeBold) },
			describe: "ModeBold should be set",
		},
		{
			name:     "dim",
			sgr:      "[2m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeDim) },
			describe: "ModeDim should be set",
		},
		{
			name:     "italic",
			sgr:      "[3m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeItalic) },
			describe: "ModeItalic should be set",
		},
		{
			name:     "underline",
			sgr:      "[4m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeUnderline) },
			describe: "ModeUnderline should be set",
		},
		{
			name:     "blink",
			sgr:      "[5m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeBlink) },
			describe: "ModeBlink should be set",
		},
		{
			name:     "reverse",
			sgr:      "[7m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeReverse) },
			describe: "ModeReverse should be set",
		},
		{
			name:     "invisible",
			sgr:      "[8m",
			checkFn:  func(s Style) bool { return s.TestMode(ModeInvisible) },
			describe: "ModeInvisible should be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.sgr)
			if len(mf.Styles) == 0 {
				t.Fatalf("StyleChanged not called for SGR %s", tt.sgr)
			}

			last := mf.Styles[len(mf.Styles)-1]
			if !tt.checkFn(last) {
				t.Errorf("%s: %s", tt.sgr, tt.describe)
			}
		})
	}
}

func TestCSI_SGR_ResetModes(t *testing.T) {
	tests := []struct {
		name     string
		setMode  Mode
		setSGR   string
		resetSGR string
	}{
		{
			name:     "reset bold/dim with 22m",
			setMode:  ModeBold,
			setSGR:   "[1m",
			resetSGR: "[22m",
		},
		{
			name:     "reset dim with 22m",
			setMode:  ModeDim,
			setSGR:   "[2m",
			resetSGR: "[22m",
		},
		{
			name:     "reset italic with 23m",
			setMode:  ModeItalic,
			setSGR:   "[3m",
			resetSGR: "[23m",
		},
		{
			name:     "reset underline with 24m",
			setMode:  ModeUnderline,
			setSGR:   "[4m",
			resetSGR: "[24m",
		},
		{
			name:     "reset blink with 25m",
			setMode:  ModeBlink,
			setSGR:   "[5m",
			resetSGR: "[25m",
		},
		{
			name:     "reset reverse with 27m",
			setMode:  ModeReverse,
			setSGR:   "[7m",
			resetSGR: "[27m",
		},
		{
			name:     "reset invisible with 28m",
			setMode:  ModeInvisible,
			setSGR:   "[8m",
			resetSGR: "[28m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.setSGR)
			if len(mf.Styles) == 0 {
				t.Fatalf("StyleChanged not called for set SGR %s", tt.setSGR)
			}
			last := mf.Styles[len(mf.Styles)-1]
			if !last.TestMode(tt.setMode) {
				t.Fatalf("mode not set after %s", tt.setSGR)
			}

			t1.mustHandleCommand(t, tt.resetSGR)
			last = mf.Styles[len(mf.Styles)-1]
			if last.TestMode(tt.setMode) {
				t.Errorf("mode still set after reset %s", tt.resetSGR)
			}
		})
	}
}

func TestCSI_SGR_ExtendedColors(t *testing.T) {
	tests := []struct {
		name      string
		sgr       string
		component ColorComponent
		wantColor Color
	}{
		{
			name:      "256-color foreground (red 196)",
			sgr:       "[38;5;196m",
			component: ComponentFG,
			wantColor: Color256(196),
		},
		{
			name:      "256-color background (blue 21)",
			sgr:       "[48;5;21m",
			component: ComponentBG,
			wantColor: Color256(21),
		},
		{
			name:      "RGB foreground (255;128;64)",
			sgr:       "[38;2;255;128;64m",
			component: ComponentFG,
			wantColor: RGB(255, 128, 64),
		},
		{
			name:      "RGB background (32;64;128)",
			sgr:       "[48;2;32;64;128m",
			component: ComponentBG,
			wantColor: RGB(32, 64, 128),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.sgr)
			if len(mf.Styles) == 0 {
				t.Fatalf("StyleChanged not called for SGR %s", tt.sgr)
			}

			last := mf.Styles[len(mf.Styles)-1]
			gotColor, err := last.GetColor(tt.component)
			if err != nil {
				t.Fatalf("GetColor error: %v", err)
			}

			if gotColor != tt.wantColor {
				t.Errorf("color: got %v (0x%X), want %v (0x%X)", gotColor, uint32(gotColor), tt.wantColor, uint32(tt.wantColor))
			}
		})
	}
}

func TestCSI_SGR_BrightColors(t *testing.T) {
	tests := []struct {
		name      string
		sgr       string
		component ColorComponent
		wantIdx   int
	}{
		{"bright fg 90 (black)", "[90m", ComponentFG, 0},
		{"bright fg 91 (red)", "[91m", ComponentFG, 1},
		{"bright fg 92 (green)", "[92m", ComponentFG, 2},
		{"bright fg 93 (yellow)", "[93m", ComponentFG, 3},
		{"bright fg 94 (blue)", "[94m", ComponentFG, 4},
		{"bright fg 95 (magenta)", "[95m", ComponentFG, 5},
		{"bright fg 96 (cyan)", "[96m", ComponentFG, 6},
		{"bright fg 97 (white)", "[97m", ComponentFG, 7},
		{"bright bg 100 (black)", "[100m", ComponentBG, 0},
		{"bright bg 101 (red)", "[101m", ComponentBG, 1},
		{"bright bg 102 (green)", "[102m", ComponentBG, 2},
		{"bright bg 103 (yellow)", "[103m", ComponentBG, 3},
		{"bright bg 104 (blue)", "[104m", ComponentBG, 4},
		{"bright bg 105 (magenta)", "[105m", ComponentBG, 5},
		{"bright bg 106 (cyan)", "[106m", ComponentBG, 6},
		{"bright bg 107 (white)", "[107m", ComponentBG, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)

			t1.mustHandleCommand(t, tt.sgr)
			if len(mf.Styles) == 0 {
				t.Fatalf("StyleChanged not called for SGR %s", tt.sgr)
			}

			last := mf.Styles[len(mf.Styles)-1]
			gotColor, err := last.GetColor(tt.component)
			if err != nil {
				t.Fatalf("GetColor error: %v", err)
			}

			if gotColor != BrightColor(tt.wantIdx) {
				t.Errorf("bright color: got %v, want BrightColor(%d)", gotColor, tt.wantIdx)
			}
		})
	}
}

func TestCSI_KeyboardModes(t *testing.T) {
	tests := []struct {
		name          string
		sequence      string
		expectedFlags int
		query         bool
		setupSeq      string // full setup sequence
	}{
		{
			name:          "push keyboard flags with no params",
			sequence:      "[>u",
			expectedFlags: 0,
		},
		{
			name:          "push keyboard flags with flags=5",
			sequence:      "[>5u",
			expectedFlags: 5,
		},
		{
			name:          "pop keyboard flags restores previous",
			sequence:      "[<u",
			expectedFlags: 3,
			setupSeq:      "[=3u", // set to 3, then push 5, pop should restore 3
		},
		{
			name:          "set keyboard flags with no params",
			sequence:      "[=u",
			expectedFlags: 0,
		},
		{
			name:          "set keyboard flags=5",
			sequence:      "[=5u",
			expectedFlags: 5,
		},
		{
			name:          "set flags=5 mode=2",
			sequence:      "[=5;2u",
			expectedFlags: 5,
		},
		{
			name:          "query keyboard flags",
			sequence:      "[?u",
			expectedFlags: 5,
			query:         true,
			setupSeq:      "[=5u",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

			if tt.setupSeq != "" {
				t1.mustHandleCommand(t, tt.setupSeq)
			}

			// For pop test, we need to push first
			if tt.sequence == "[<u" {
				t1.mustHandleCommand(t, "[>5u") // push 5
			}

			if tt.query {
				go func() { _ = t1.testHandleCommand(t, tt.sequence) }()
				buf := make([]byte, 32)
				n, _ := r.Read(buf)
				got := string(buf[:n])
				if !strings.Contains(got, "\x1b[?") || !strings.Contains(got, "u") {
					t.Fatalf("expected query response to contain escape sequence, got %q", got)
				}
			} else {
				t1.mustHandleCommand(t, tt.sequence)
				if t1.keyboardFlags() != tt.expectedFlags {
					t.Fatalf("expected keyboard flags %d, got %d", tt.expectedFlags, t1.keyboardFlags())
				}
			}
		})
	}
}

func TestCSI_WindowManipulation(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "save window title",
			sequence: "[22t",
		},
		{
			name:     "restore window title",
			sequence: "[23t",
		},
		{
			name:     "unknown window manipulation",
			sequence: "[99t",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.mustHandleCommand(t, tt.sequence)
		})
	}
}

func TestCSI_IgnoredCommands(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "select character set",
			sequence: "[%G",
		},
		{
			name:     "SGR mouse with params",
			sequence: "[<1;2;3M",
		},
		{
			name:     "private SGR",
			sequence: "[?0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.mustHandleCommand(t, tt.sequence)
		})
	}
}

func TestCSI_ParamParsing(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "empty params with separators",
			sequence: "[;;A",
		},
		{
			name:     "more than 8 params overflow handling",
			sequence: "[1;2;3;4;5;6;7;8;9H",
		},
		{
			name:     "mixed empty and filled params",
			sequence: "[1;;3;H",
		},
		{
			name:     "multiple consecutive semicolons",
			sequence: "[;;;A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.mustHandleCommand(t, tt.sequence)
		})
	}
}

func TestCSI_UnknownCommands(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "unknown command with no prefix",
			sequence: "[Z",
		},
		{
			name:     "unknown ? command",
			sequence: "[?Z",
		},
		{
			name:     "unknown > command",
			sequence: "[>Z",
		},
		{
			name:     "unknown < command",
			sequence: "[<Z",
		},
		{
			name:     "unknown = command",
			sequence: "[=Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.mustHandleCommand(t, tt.sequence)
		})
	}
}

func TestCSI_ModifyOtherKeysExtended(t *testing.T) {
	tests := []struct {
		name         string
		sequence     string
		expectedMode int
	}{
		{
			name:         "reset modifyOtherKeys (mode param omitted)",
			sequence:     "[>4m",
			expectedMode: 0,
		},
		{
			name:         "set modifyOtherKeys to mode 1",
			sequence:     "[>4;1m",
			expectedMode: 1,
		},
		{
			name:         "set modifyOtherKeys to mode 2",
			sequence:     "[>4;2m",
			expectedMode: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			t1.mustHandleCommand(t, tt.sequence)
			if v := mf.ViewInts[VIModifyOtherKeys]; v != tt.expectedMode {
				t.Fatalf("expected VIModifyOtherKeys %d, got %d", tt.expectedMode, v)
			}
		})
	}
}

func TestCSI_CursorCommands(t *testing.T) {
	tests := []struct {
		name   string
		seq    string
		startX int
		startY int
		wantX  int
		wantY  int
	}{
		// Cursor Up (A)
		{"A no param defaults to 1", "[A", 5, 5, 5, 4},
		{"A with param", "[3A", 5, 5, 5, 2},
		// Cursor Down (B)
		{"B no param defaults to 1", "[B", 5, 5, 5, 6},
		{"B with param", "[2B", 5, 5, 5, 7},
		// Cursor Forward (C)
		{"C no param defaults to 1", "[C", 5, 5, 6, 5},
		{"C with param", "[4C", 5, 5, 9, 5},
		// Cursor Backward (D)
		{"D no param defaults to 1", "[D", 5, 5, 4, 5},
		{"D with param", "[3D", 5, 5, 2, 5},
		// Cursor Next Line (E) / Previous Line (F)
		{"E moves down and to column 1", "[E", 5, 5, 0, 6},
		{"E with param", "[2E", 5, 5, 0, 7},
		{"F moves up and to column 1", "[F", 5, 5, 0, 4},
		{"F with param", "[2F", 5, 5, 0, 3},
		// Cursor Forward (a) / Down (e)
		{"a moves right", "[a", 5, 5, 6, 5},
		{"a with param", "[4a", 5, 5, 9, 5},
		{"e moves down", "[e", 5, 5, 5, 6},
		{"e with param", "[2e", 5, 5, 5, 7},
		// Cursor Forward/Backward Tabulation (I/Z)
		{"I next tab stop", "[I", 5, 5, 8, 5},
		{"I two tab stops", "[2I", 5, 5, 16, 5},
		{"Z previous tab stop", "[Z", 5, 5, 0, 5},
		{"Z two tab stops", "[2Z", 20, 5, 8, 5},
		// Cursor Character Absolute (G)
		{"G no param defaults to col 1", "[G", 10, 5, 0, 5},
		{"G with param", "[8G", 5, 5, 7, 5},
		// Line Position Absolute (d)
		{"d no param defaults to row 1", "[d", 5, 10, 5, 0},
		{"d with param", "[7d", 5, 5, 5, 6},
		// Cursor Home (H)
		{"H no params defaults to 1,1", "[H", 10, 10, 0, 0},
		{"H with row only", "[5H", 10, 10, 0, 4},
		{"H with row and col", "[3;7H", 10, 10, 6, 2},
		// Cursor Position (f) - same as H
		{"f with row and col", "[4;8f", 10, 10, 7, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(tt.startX, tt.startY)

			t1.mustHandleCommand(t, tt.seq)

			pos := t1.screen().CursorPos()
			if pos.X != tt.wantX || pos.Y != tt.wantY {
				t.Errorf("cursor = (%d,%d), want (%d,%d)", pos.X, pos.Y, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestCSI_EraseInLine(t *testing.T) {
	tests := []struct {
		name    string
		seq     string
		cursorX int
		cursorY int
	}{
		// K or 0K - erase to end of line
		{"K erases to end of line", "[K", 5, 5},
		{"0K erases to end of line", "[0K", 5, 5},
		// 1K - erase to start of line
		{"1K erases to start of line", "[1K", 5, 5},
		// 2K - erase entire line
		{"2K erases entire line", "[2K", 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			mf.Regions = nil // clear initial regions
			t1.screen().setCursorPos(tt.cursorX, tt.cursorY)

			t1.mustHandleCommand(t, tt.seq)

			found := false
			for _, reg := range mf.Regions {
				if reg.C == CRClear && reg.R.Y == tt.cursorY {
					found = true
					break
				}
			}
			if !found {
				t.Error("no CRClear region found on cursor line")
			}
		})
	}
}

func TestCSI_EraseDisplay(t *testing.T) {
	tests := []struct {
		name        string
		seq         string
		cursorX     int
		cursorY     int
		checkCursor bool
		wantCursorX int
		wantCursorY int
	}{
		// J or 0J - erase to bottom
		{"J erases to bottom", "[J", 5, 5, false, 0, 0},
		{"0J erases to bottom", "[0J", 5, 5, false, 0, 0},
		// 1J - erase to top
		{"1J erases to top", "[1J", 5, 5, false, 0, 0},
		// 2J - erase screen, cursor is unchanged
		{"2J erases screen without moving cursor", "[2J", 5, 5, true, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(tt.cursorX, tt.cursorY)

			t1.mustHandleCommand(t, tt.seq)

			found := false
			for _, reg := range mf.Regions {
				if reg.C == CRClear {
					found = true
					break
				}
			}
			if !found {
				t.Error("no CRClear region found")
			}

			if tt.checkCursor {
				pos := t1.screen().CursorPos()
				if pos.X != tt.wantCursorX || pos.Y != tt.wantCursorY {
					t.Errorf("cursor = (%d,%d), want (%d,%d)", pos.X, pos.Y, tt.wantCursorX, tt.wantCursorY)
				}
			}
		})
	}
}

func TestCSI_ScrollCommands(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		// L - insert lines (scroll down)
		{"L no param defaults to 1", "[L"},
		{"L with param", "[3L"},
		// M - delete lines (scroll up)
		{"M no param defaults to 1", "[M"},
		{"M with param", "[2M"},
		// S - scroll up
		{"S no param defaults to 1", "[S"},
		{"S with param", "[5S"},
		// T - scroll down
		{"T no param defaults to 1", "[T"},
		{"T with param", "[4T"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(5, 5)

			t1.mustHandleCommand(t, tt.seq)

			found := false
			for _, reg := range mf.Regions {
				if reg.C == CRScroll {
					found = true
					break
				}
			}
			if !found {
				t.Error("no CRScroll region found")
			}
		})
	}
}

func TestCSI_DeleteEraseChars(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		// P - delete chars
		{"P no param defaults to 1", "[P"},
		{"P with param", "[5P"},
		// X - erase chars from cursor
		{"X no param defaults to 1", "[X"},
		{"X with param", "[10X"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, mf := MakeTerminalWithMock(TextReadModeRune)
			t1.screen().setCursorPos(5, 5)

			t1.mustHandleCommand(t, tt.seq)

			found := false
			for _, reg := range mf.Regions {
				if reg.C == CRClear || reg.C == CRText {
					found = true
					break
				}
			}
			if !found {
				t.Error("no region change found for delete/erase")
			}
		})
	}
}

func TestCSI_ScrollMargins(t *testing.T) {
	// Screen is 80x14 (0-13), so default bottom margin is 13
	tests := []struct {
		name       string
		seq        string
		wantTop    int
		wantBottom int
	}{
		{"r resets to full screen", "[r", 0, 13},
		{"5r sets top margin only", "[5r", 4, 13},
		{"5;10r sets top and bottom", "[5;10r", 4, 9},
		{"1;14r sets full screen explicitly", "[1;14r", 0, 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

			t1.screen().setCursorPos(5, 5)
			t1.mustHandleCommand(t, tt.seq)

			if got := t1.screen().TopMargin(); got != tt.wantTop {
				t.Errorf("TopMargin() = %d, want %d", got, tt.wantTop)
			}
			if got := t1.screen().BottomMargin(); got != tt.wantBottom {
				t.Errorf("BottomMargin() = %d, want %d", got, tt.wantBottom)
			}
			if pos := t1.CursorPos(); pos.X != 0 || pos.Y != 0 {
				t.Errorf("cursor = %+v, want homed (0,0) after DECSTBM", pos)
			}
		})
	}
}
