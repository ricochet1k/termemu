package termemu

import (
	"strings"
	"testing"
)

// styleWithUnderlineColor returns a style with an underline color component.
func styleWithUnderlineColor(c Color) Style {
	s := NewStyle()
	if err := s.SetColor(ComponentUnderline, c); err != nil {
		panic(err)
	}
	return s
}

// sgrStyleTerm feeds data and returns the style of the first cell on row 0.
func sgrStyleTerm(t *testing.T, data string) Style {
	t.Helper()
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, data)
	ss := term.mainScreen.(*spanScreen)
	return ss.lines[0].spans[0].Style
}

func styleComponent(t *testing.T, st Style, c ColorComponent) Color {
	t.Helper()
	col, err := st.GetColor(c)
	if err != nil {
		t.Fatalf("GetColor(%v): %v", c, err)
	}
	return col
}

func TestSGR_ColonExtendedColor(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		fg   Color
		bg   Color
		ul   Color
	}{
		{"colon_rgb", "\x1b[38:2::255:0:0m", RGB(255, 0, 0), DefaultColor(), DefaultColor()},
		{"colon_rgb_no_colorspace", "\x1b[38:2:255:0:0m", RGB(255, 0, 0), DefaultColor(), DefaultColor()},
		{"colon_rgb_explicit_colorspace", "\x1b[38:2:0:1:2:3m", RGB(1, 2, 3), DefaultColor(), DefaultColor()},
		{"colon_256", "\x1b[38:5:208m", Color256(208), DefaultColor(), DefaultColor()},
		{"colon_bg_rgb", "\x1b[48:2::4:5:6m", DefaultColor(), RGB(4, 5, 6), DefaultColor()},
		{"colon_bg_256", "\x1b[48:5:200m", DefaultColor(), Color256(200), DefaultColor()},
		{"colon_underline_rgb", "\x1b[58:2::7:8:9m", DefaultColor(), DefaultColor(), RGB(7, 8, 9)},
		{"colon_underline_256", "\x1b[58:5:21m", DefaultColor(), DefaultColor(), Color256(21)},
		{"semicolon_underline_rgb", "\x1b[58;2;7;8;9m", DefaultColor(), DefaultColor(), RGB(7, 8, 9)},
		{"semicolon_underline_256", "\x1b[58;5;21m", DefaultColor(), DefaultColor(), Color256(21)},
		{"mixed", "\x1b[1;38:2::1:2:3;4;48:2::4:5:6;58:5:9m", RGB(1, 2, 3), RGB(4, 5, 6), Color256(9)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := sgrStyleTerm(t, tc.seq+"X")
			if got := styleComponent(t, st, ComponentFG); got != tc.fg {
				t.Fatalf("fg = %#x, want %#x", got, tc.fg)
			}
			if got := styleComponent(t, st, ComponentBG); got != tc.bg {
				t.Fatalf("bg = %#x, want %#x", got, tc.bg)
			}
			if got := styleComponent(t, st, ComponentUnderline); got != tc.ul {
				t.Fatalf("underline color = %#x, want %#x", got, tc.ul)
			}
		})
	}
}

func TestSGR_ColonDoesNotLeakText(t *testing.T) {
	for _, mode := range []TextReadMode{TextReadModeRune, TextReadModeGrapheme} {
		_, term, _ := MakeTerminalWithMock(mode)
		feedText(t, term, "\x1b[38:2::255:0:0mX")
		if got := strings.TrimRight(term.Line(0), " "); got != "X" {
			t.Fatalf("mode %d: line = %q, want %q", mode, got, "X")
		}
		if got, want := term.CursorPos(), (Pos{X: 1, Y: 0}); got != want {
			t.Fatalf("mode %d: cursor = %+v, want %+v", mode, got, want)
		}
	}
}

func TestSGR_UnderlineColorDefault(t *testing.T) {
	// The second cell is written after SGR 59 / SGR 0.
	for _, seq := range []string{
		"\x1b[58:2::1:2:3mA\x1b[59mB",
		"\x1b[58:5:21mA\x1b[0mB",
	} {
		_, term, _ := MakeTerminalWithMock(TextReadModeRune)
		feedText(t, term, seq)
		ss := term.mainScreen.(*spanScreen)
		st := ss.lines[0].spans[1].Style
		if got := styleComponent(t, st, ComponentUnderline); !got.IsDefault() {
			t.Fatalf("%q left underline color %#x", seq, got)
		}
	}
}

func TestSGR_MalformedColonFormsAreSafe(t *testing.T) {
	for _, seq := range []string{
		"\x1b[38:m",
		"\x1b[38:2m",
		"\x1b[38:2:1m",
		"\x1b[38:9:1:2:3m",
		"\x1b[58:2m",
		"\x1b[38::m",
		"\x1b[38:2::1:2:3:4:5m",
	} {
		_, term, _ := MakeTerminalWithMock(TextReadModeRune)
		feedText(t, term, seq+"X")
		if got := strings.TrimRight(term.Line(0), " "); got != "X" {
			t.Fatalf("%q leaked text: line = %q", seq, got)
		}
	}
}

func TestANSIEscapeFrom_UnderlineColor(t *testing.T) {
	def := NewStyle()

	// Setting an RGB underline color from the default style.
	st := styleWithUnderlineColor(RGB(255, 0, 0))
	if got, want := string(st.ANSIEscapeFrom(def)), "\x1b[58;2;255;0;0m"; got != want {
		t.Fatalf("default->underline RGB = %q, want %q", got, want)
	}
	// Returning to the default underline color.
	if got, want := string(def.ANSIEscapeFrom(st)), "\x1b[59m"; got != want {
		t.Fatalf("underline RGB->default = %q, want %q", got, want)
	}
	// Palette and bright palette colors always use the 58;5;n form.
	pal := styleWithUnderlineColor(Color256(21))
	if got, want := string(pal.ANSIEscapeFrom(def)), "\x1b[58;5;21m"; got != want {
		t.Fatalf("default->underline palette = %q, want %q", got, want)
	}
	bright := styleWithUnderlineColor(BrightColor(2))
	if got, want := string(bright.ANSIEscapeFrom(def)), "\x1b[58;5;10m"; got != want {
		t.Fatalf("default->underline bright = %q, want %q", got, want)
	}
	// Unchanged underline color emits nothing.
	if got := st.ANSIEscapeFrom(st); len(got) != 0 {
		t.Fatalf("identical styles emitted %q", got)
	}
}

func TestANSIEscape_UnderlineColorRoundTrip(t *testing.T) {
	for _, st := range []Style{
		styleWithUnderlineColor(RGB(9, 8, 7)),
		styleWithUnderlineColor(Color256(200)),
		styleWith(ComponentUnderline, RGB(1, 2, 3), ModeUnderline),
	} {
		esc := st.ANSIEscape()
		_, term, _ := MakeTerminalWithMock(TextReadModeRune)
		feedText(t, term, string(esc)+"X")
		ss := term.mainScreen.(*spanScreen)
		got := ss.lines[0].spans[0].Style
		if got != st {
			t.Fatalf("style %#v round-tripped through %q as %#v", st, esc, got)
		}
	}

	// A bright underline color has no shorthand, so it renders as the
	// equivalent 256-palette entry (bright N is palette 8+N).
	bright := styleWithUnderlineColor(BrightColor(3))
	if got, want := string(bright.ANSIEscape()), "\x1b[0m\x1b[58;5;11m"; got != want {
		t.Fatalf("bright underline rendered %q, want %q", got, want)
	}
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, string(bright.ANSIEscape())+"X")
	ss := term.mainScreen.(*spanScreen)
	if got := styleComponent(t, ss.lines[0].spans[0].Style, ComponentUnderline); got != Color256(11) {
		t.Fatalf("bright underline parsed back as %#x, want %#x", got, Color256(11))
	}
}
