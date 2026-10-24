package termemu

import (
	"testing"
)

func styleWithFG(c Color) Style {
	s := NewStyle()
	if err := s.SetColor(ComponentFG, c); err != nil {
		panic(err)
	}
	return s
}

func styleWithBG(c Color) Style {
	s := NewStyle()
	if err := s.SetColor(ComponentBG, c); err != nil {
		panic(err)
	}
	return s
}

func styleWithFGBG(fg, bg Color) Style {
	s := NewStyle()
	if err := s.SetColor(ComponentFG, fg); err != nil {
		panic(err)
	}
	if err := s.SetColor(ComponentBG, bg); err != nil {
		panic(err)
	}
	return s
}

func TestANSIEscapeFrom_ColoredToDefault(t *testing.T) {
	def := NewStyle()

	if got := string(def.ANSIEscapeFrom(styleWithFG(Color256(ColRed)))); got != "\x1b[39m" {
		t.Fatalf("fg colored->default: got %q, want %q", got, "\x1b[39m")
	}
	if got := string(def.ANSIEscapeFrom(styleWithBG(Color256(ColGreen)))); got != "\x1b[49m" {
		t.Fatalf("bg colored->default: got %q, want %q", got, "\x1b[49m")
	}
	if got := string(def.ANSIEscapeFrom(styleWithFGBG(Color256(ColRed), Color256(ColGreen)))); got != "\x1b[39m\x1b[49m" {
		t.Fatalf("fg+bg colored->default: got %q, want %q", got, "\x1b[39m\x1b[49m")
	}
}

func TestANSIEscapeFrom_DefaultToColored(t *testing.T) {
	def := NewStyle()

	if got := string(styleWithFG(Color256(ColRed)).ANSIEscapeFrom(def)); got != "\x1b[31m" {
		t.Fatalf("fg default->colored: got %q, want %q", got, "\x1b[31m")
	}
	if got := string(styleWithBG(Color256(ColGreen)).ANSIEscapeFrom(def)); got != "\x1b[42m" {
		t.Fatalf("bg default->colored: got %q, want %q", got, "\x1b[42m")
	}
}

func TestANSIEscapeFrom_ExtendedToDefault(t *testing.T) {
	def := NewStyle()

	if got := string(def.ANSIEscapeFrom(styleWithFG(RGB(10, 20, 30)))); got != "\x1b[39m" {
		t.Fatalf("fg RGB->default: got %q, want %q", got, "\x1b[39m")
	}
	if got := string(def.ANSIEscapeFrom(styleWithBG(Color256(200)))); got != "\x1b[49m" {
		t.Fatalf("bg 256-color->default: got %q, want %q", got, "\x1b[49m")
	}
	if got := string(def.ANSIEscapeFrom(styleWithFG(BrightColor(ColRed)))); got != "\x1b[39m" {
		t.Fatalf("fg bright->default: got %q, want %q", got, "\x1b[39m")
	}
	if got := string(def.ANSIEscapeFrom(styleWithBG(BrightColor(ColBlue)))); got != "\x1b[49m" {
		t.Fatalf("bg bright->default: got %q, want %q", got, "\x1b[49m")
	}
}

func TestANSIEscapeFrom_ModeEncoding(t *testing.T) {
	def := NewStyle()
	style := func(modes ...Mode) Style {
		s := NewStyle()
		s.SetMode(modes...)
		return s
	}
	redBold := styleWithFG(Color256(ColRed))
	redBold.SetMode(ModeBold)
	redBlueDouble := styleWithFGBG(Color256(ColRed), Color256(ColBlue))
	redBlueDouble.SetMode(ModeDoubleUnderline)
	redBlueUnder := styleWithFGBG(Color256(ColRed), Color256(ColBlue))
	redBlueUnder.SetMode(ModeUnderline)
	redBoldBlueBG := styleWithFGBG(Color256(ColRed), Color256(ColBlue))
	redBoldBlueBG.SetMode(ModeBold)

	cases := []struct {
		name string
		prev Style
		next Style
		want string
	}{
		// Returning to a plain default style uses one SGR 0; otherwise modes
		// are toggled individually.
		{"add bold default colors", def, style(ModeBold), "\x1b[1m"},
		{"remove bold default colors", style(ModeBold), def, "\x1b[0m"},
		{"bold to dim", style(ModeBold), style(ModeDim), "\x1b[22m\x1b[2m"},
		{"bold+dim to dim", style(ModeBold, ModeDim), style(ModeDim), "\x1b[22m\x1b[2m"},
		{"underline to double", style(ModeUnderline), style(ModeDoubleUnderline), "\x1b[24m\x1b[21m"},
		{"blink to rapid", style(ModeBlink), style(ModeRapidBlink), "\x1b[25m\x1b[6m"},
		{"framed to encircled", style(ModeFramed), style(ModeEncircled), "\x1b[54m\x1b[52m"},
		{"remove overline", style(ModeOverline), def, "\x1b[0m"},
		{"remove italic", style(ModeItalic), def, "\x1b[0m"},
		{"remove strike", style(ModeStrike), def, "\x1b[0m"},
		{"add italic", def, style(ModeItalic), "\x1b[3m"},

		// With non-default colors, individual mode codes avoid re-emitting
		// the colors and win.
		{"add bold over red", styleWithFG(Color256(ColRed)), redBold, "\x1b[1m"},
		{"remove bold over red", redBold, styleWithFG(Color256(ColRed)), "\x1b[22m"},
		{"underline to double over colors", redBlueUnder, redBlueDouble, "\x1b[24m\x1b[21m"},

		// Dropping all modes: plain default uses SGR 0, unchanged colors use
		// individual reset codes, and a new non-default color uses a reset.
		{"modes+colors to default", redBold, def, "\x1b[0m"},
		{"modes to unchanged red", redBold, styleWithFG(Color256(ColRed)), "\x1b[22m"},
		{"modes to new blue", redBold, styleWithFG(Color256(ColBlue)), "\x1b[0m\x1b[34m"},
		{"modes+fg to unchanged bg", redBoldBlueBG, styleWithBG(Color256(ColBlue)), "\x1b[22m\x1b[39m"},
	}
	for _, tc := range cases {
		if got, want := string(tc.next.ANSIEscapeFrom(tc.prev)), tc.want; got != want {
			t.Errorf("%s: got %q, want %q", tc.name, got, want)
		}
	}

	// A color change on its own never emits a reset.
	red := styleWithFG(Color256(ColRed))
	if got, want := string(red.ANSIEscapeFrom(NewStyle())), "\x1b[31m"; got != want {
		t.Errorf("color only: got %q, want %q", got, want)
	}

	// A style identical to prev emits nothing.
	if got := def.ANSIEscapeFrom(def); got != nil {
		t.Fatalf("identical styles should emit nothing, got %q", string(got))
	}
}

func TestANSIEscape_Unchanged(t *testing.T) {
	red := styleWithFG(Color256(ColRed))
	if got := string(red.ANSIEscape()); got != "\x1b[0m\x1b[31m" {
		t.Fatalf("ANSIEscape: got %q, want %q", got, "\x1b[0m\x1b[31m")
	}
	if got := string(NewStyle().ANSIEscape()); got != "\x1b[0m" {
		t.Fatalf("default ANSIEscape: got %q, want %q", got, "\x1b[0m")
	}
}

// TestModeSGRCodesCoverAllModes pins the bit-position table used by the ANSI
// renderer to the SGR codes the emulator has always emitted.
func TestModeSGRCodesCoverAllModes(t *testing.T) {
	want := map[Mode]int16{
		ModeBold:            1,
		ModeDim:             2,
		ModeItalic:          3,
		ModeUnderline:       4,
		ModeBlink:           5,
		ModeRapidBlink:      6,
		ModeReverse:         7,
		ModeInvisible:       8,
		ModeStrike:          9,
		ModeDoubleUnderline: 21,
		ModeFramed:          51,
		ModeEncircled:       52,
		ModeOverline:        53,
	}
	for mode, code := range want {
		idx := 0
		for m := mode; m > 1; m >>= 1 {
			idx++
		}
		if idx >= len(modeSGRCodes) {
			t.Fatalf("mode %d maps to bit %d outside modeSGRCodes", mode, idx)
		}
		if got := modeSGRCodes[idx]; got != code {
			t.Errorf("modeSGRCodes[%d] (mode %d) = %d, want %d", idx, mode, got, code)
		}
	}
	if got := len(want); got != len(modeSGRCodes) {
		t.Errorf("modeSGRCodes covers %d modes, want %d", len(modeSGRCodes), got)
	}
}
