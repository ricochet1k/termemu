package termemu

import (
	"bytes"
	"strings"
	"testing"
)

func TestPalette_Defaults(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	tests := []struct {
		idx  int
		want Color
	}{
		{0, RGB(0, 0, 0)},
		{1, RGB(0xcd, 0, 0)},
		{7, RGB(0xe5, 0xe5, 0xe5)},
		{15, RGB(0xff, 0xff, 0xff)},
		{16, RGB(0, 0, 0)},
		{196, RGB(255, 0, 0)},
		{231, RGB(255, 255, 255)},
		{232, RGB(8, 8, 8)},
		{255, RGB(238, 238, 238)},
	}
	for _, tc := range tests {
		if got := term.PaletteColor(tc.idx); got != tc.want {
			t.Errorf("palette[%d] = %#x, want %#x", tc.idx, got, tc.want)
		}
	}
	if got := term.PaletteColor(300); !got.IsDefault() {
		t.Errorf("out-of-range palette index = %#x, want default", got)
	}
	if got := term.DefaultForeground(); got != RGB(0xff, 0xff, 0xff) {
		t.Errorf("default fg = %#x", got)
	}
	if got := term.DefaultBackground(); got != RGB(0, 0, 0) {
		t.Errorf("default bg = %#x", got)
	}
	if got := term.CursorColor(); got != RGB(0xff, 0xff, 0xff) {
		t.Errorf("cursor color = %#x", got)
	}
}

func TestOSC4_SetAndQuery(t *testing.T) {
	r, term, mf := MakeTerminalWithMock(TextReadModeRune)

	feedText(t, term, "\x1b]4;1;rgb:00/00/ff\x07")
	if got, want := term.PaletteColor(1), RGB(0, 0, 0xff); got != want {
		t.Fatalf("palette[1] = %#x, want %#x", got, want)
	}
	if got := mf.Regions[len(mf.Regions)-1].C; got != CRPalette {
		t.Fatalf("last change reason = %v, want CRPalette", got)
	}

	feedQueryExpectReply(t, term, r, "\x1b]4;1;?\a", "\x1b]4;1;rgb:0000/0000/ffff\x1b\\")

	// Several pairs in one sequence: a set and a query.
	feedQueryExpectReply(t, term, r, "\x1b]4;2;rgb:12/34/56;3;?\a",
		"\x1b]4;3;rgb:cdcd/cdcd/0000\x1b\\")
	if got, want := term.PaletteColor(2), RGB(0x12, 0x34, 0x56); got != want {
		t.Fatalf("palette[2] = %#x, want %#x", got, want)
	}
}

func TestOSC4_InvalidValuesIgnored(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	before := len(mf.Regions)
	for _, seq := range []string{
		"\x1b]4;1;notacolor\x07",
		"\x1b]4;999;rgb:ff/ff/ff\x07",
		"\x1b]4;-1;rgb:ff/ff/ff\x07",
		"\x1b]4;1\x07",
	} {
		feedText(t, term, seq)
	}
	if got := term.PaletteColor(1); got != RGB(0xcd, 0, 0) {
		t.Fatalf("palette[1] was modified to %#x", got)
	}
	if len(mf.Regions) != before {
		t.Fatalf("invalid OSC 4 sequences emitted %d region changes", len(mf.Regions)-before)
	}
}

func TestOSC4_SettingSameColorDoesNotRepaint(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]4;1;rgb:cd/00/00\x07")
	before := len(mf.Regions)
	feedText(t, term, "\x1b]4;1;rgb:cd/00/00\x07")
	if len(mf.Regions) != before {
		t.Fatal("re-setting the same palette color emitted a repaint")
	}
}

func TestOSC104_ResetPalette(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]4;1;rgb:ff/ff/ff;2;rgb:ff/00/ff\x07")
	if got := term.PaletteColor(1); got != RGB(0xff, 0xff, 0xff) {
		t.Fatalf("palette[1] = %#x", got)
	}

	// Reset selected entries.
	feedText(t, term, "\x1b]104;1\x07")
	if got, want := term.PaletteColor(1), RGB(0xcd, 0, 0); got != want {
		t.Fatalf("palette[1] after OSC 104;1 = %#x, want %#x", got, want)
	}
	if got := term.PaletteColor(2); got != RGB(0xff, 0, 0xff) {
		t.Fatalf("palette[2] was reset by OSC 104;1: %#x", got)
	}

	// Reset everything.
	feedText(t, term, "\x1b]104\x07")
	if got, want := term.PaletteColor(2), RGB(0, 0xcd, 0); got != want {
		t.Fatalf("palette[2] after OSC 104 = %#x, want %#x", got, want)
	}
}

func TestOSC_DynamicColors(t *testing.T) {
	r, term, mf := MakeTerminalWithMock(TextReadModeRune)

	feedText(t, term, "\x1b]10;#ff8800\x07")
	if got, want := term.DefaultForeground(), RGB(0xff, 0x88, 0); got != want {
		t.Fatalf("default fg = %#x, want %#x", got, want)
	}
	feedText(t, term, "\x1b]11;rgb:12/34/56\x07")
	if got, want := term.DefaultBackground(), RGB(0x12, 0x34, 0x56); got != want {
		t.Fatalf("default bg = %#x, want %#x", got, want)
	}
	feedText(t, term, "\x1b]12;rgb:abcd/0000/ffff\x07") // 4 digits per component
	if got, want := term.CursorColor(), RGB(0xab, 0, 0xff); got != want {
		t.Fatalf("cursor color = %#x, want %#x", got, want)
	}
	if got := mf.Regions[len(mf.Regions)-1].C; got != CRPalette {
		t.Fatalf("last change reason = %v, want CRPalette", got)
	}

	feedQueryExpectReply(t, term, r, "\x1b]10;?\a", "\x1b]10;rgb:ffff/8888/0000\x1b\\")
	feedQueryExpectReply(t, term, r, "\x1b]11;?\a", "\x1b]11;rgb:1212/3434/5656\x1b\\")
	feedQueryExpectReply(t, term, r, "\x1b]12;?\a", "\x1b]12;rgb:abab/0000/ffff\x1b\\")

	feedText(t, term, "\x1b]110\x07\x1b]111\x07\x1b]112\x07")
	if got := term.DefaultForeground(); got != defaultDynamicColors.fg {
		t.Fatalf("default fg after OSC 110 = %#x", got)
	}
	if got := term.DefaultBackground(); got != defaultDynamicColors.bg {
		t.Fatalf("default bg after OSC 111 = %#x", got)
	}
	if got := term.CursorColor(); got != defaultDynamicColors.cursor {
		t.Fatalf("cursor color after OSC 112 = %#x", got)
	}
}

func TestOSC_DynamicColorInvalidSpec(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	before := len(mf.Regions)
	feedText(t, term, "\x1b]10;rgb:zz/00/00\x07")
	if got := term.DefaultForeground(); got != defaultDynamicColors.fg {
		t.Fatalf("invalid spec changed default fg to %#x", got)
	}
	if len(mf.Regions) != before {
		t.Fatal("invalid color spec emitted a repaint")
	}
}

func TestPalette_RISResets(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]4;1;rgb:ff/ff/ff\x07\x1b]10;#00ff00\x07")
	feedText(t, term, "\x1bc")
	if got, want := term.PaletteColor(1), RGB(0xcd, 0, 0); got != want {
		t.Fatalf("palette[1] after RIS = %#x, want %#x", got, want)
	}
	if got := term.DefaultForeground(); got != defaultDynamicColors.fg {
		t.Fatalf("default fg after RIS = %#x", got)
	}
}

func TestPalette_SnapshotRestore(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]4;1;rgb:01/02/03\x07\x1b]11;rgb:0a/0b/0c\x07")
	snap := term.Snapshot()

	feedText(t, term, "\x1b]4;1;rgb:ff/ff/ff\x07\x1b]104\x07")
	_, other, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if got, want := other.PaletteColor(1), RGB(1, 2, 3); got != want {
		t.Fatalf("restored palette[1] = %#x, want %#x", got, want)
	}
	if got, want := other.DefaultBackground(), RGB(0x0a, 0x0b, 0x0c); got != want {
		t.Fatalf("restored default bg = %#x, want %#x", got, want)
	}
}

func TestPalette_ViewAccessors(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]4;1;rgb:01/02/03\x07")
	var got Color
	term.View(func(v LockedView) {
		got = v.PaletteColor(1)
		if v.DefaultForeground() != defaultDynamicColors.fg {
			t.Error("view default fg mismatch")
		}
	})
	if want := (RGB(1, 2, 3)); got != want {
		t.Fatalf("view palette[1] = %#x, want %#x", got, want)
	}

	// A zero view reports the defaults instead of panicking.
	var zero LockedView
	if got := zero.PaletteColor(1); got != defaultPalette[1] {
		t.Fatalf("zero view palette[1] = %#x", got)
	}
	if got := zero.DefaultBackground(); got != defaultDynamicColors.bg {
		t.Fatalf("zero view default bg = %#x", got)
	}
}

func TestParseColorSpec(t *testing.T) {
	tests := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"rgb:ff/00/80", RGB(0xff, 0, 0x80), true},
		{"rgb:f/0/8", RGB(0xff, 0, 0x88), true},
		{"rgb:ffff/0000/8888", RGB(0xff, 0, 0x88), true},
		{"#ff0080", RGB(0xff, 0, 0x80), true},
		{"#f08", RGB(0xff, 0, 0x88), true},
		{"#fff000888", RGB(0xff, 0, 0x88), true},
		{"rgb:ff/00", 0, false},
		{"#ff00", 0, false},
		{"red", 0, false},
		{"rgb:gg/00/00", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseColorSpec(tc.in)
		if ok != tc.ok {
			t.Fatalf("parseColorSpec(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("parseColorSpec(%q) = %#x, want %#x", tc.in, got, tc.want)
		}
	}
	if got := formatColorSpec(RGB(0x12, 0x34, 0x56)); got != "rgb:1212/3434/5656" {
		t.Fatalf("formatColorSpec = %q", got)
	}
}

// newModeTestTerm returns a terminal that writes to buf, for input encoding
// tests (mouse reports and translated arrow keys).
func newModeTestTerm() (*terminal, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return newTerminal(&EmptyFrontend{}, NewNoPTYBackend(bytes.NewReader(nil), buf), TextReadModeRune), buf
}

func TestAltScroll_WheelBecomesArrowKeys(t *testing.T) {
	term, buf := newModeTestTerm()
	term.viewInts[VIMouseMode] = MMNone
	feedText(t, term, "\x1b[?1007h\x1b[?1049h")

	buf.Reset()
	if err := term.SendMouseRaw(MBtn1, true, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\x1b[A"; got != want {
		t.Fatalf("wheel up = %q, want %q", got, want)
	}

	buf.Reset()
	if err := term.SendMouseRaw(MBtn2, true, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\x1b[B"; got != want {
		t.Fatalf("wheel down = %q, want %q", got, want)
	}

	// Application cursor keys change the encoding.
	feedText(t, term, "\x1b[?1h")
	buf.Reset()
	if err := term.SendMouseRaw(MBtn1, true, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\x1bOA"; got != want {
		t.Fatalf("wheel up with DECCKM = %q, want %q", got, want)
	}
}

func TestAltScroll_IgnoredOffAltScreenOrWithMouseMode(t *testing.T) {
	term, buf := newModeTestTerm()
	term.viewInts[VIMouseMode] = MMNone
	feedText(t, term, "\x1b[?1007h") // main screen

	buf.Reset()
	if err := term.SendMouseRaw(MBtn1, true, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wheel on the main screen produced %q", buf.String())
	}

	// On the alt screen with mouse reporting enabled, the report wins.
	feedText(t, term, "\x1b[?1049h\x1b[?1000h\x1b[?1006h")
	buf.Reset()
	if err := term.SendMouseRaw(MBtn1, true, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.HasPrefix(got, "\x1b[<") {
		t.Fatalf("wheel with mouse mode = %q, want an SGR report", got)
	}
}

func TestAltScroll_ReleaseIgnored(t *testing.T) {
	term, buf := newModeTestTerm()
	term.viewInts[VIMouseMode] = MMNone
	feedText(t, term, "\x1b[?1007h\x1b[?1049h")
	buf.Reset()
	if err := term.SendMouseRaw(MBtn1, false, MWheel, 5, 5); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wheel release produced %q", buf.String())
	}
}

func TestMousePixelMode_FlagAndReport(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if term.GetViewFlag(VFMousePixel) {
		t.Fatal("pixel mouse mode enabled by default")
	}
	feedText(t, term, "\x1b[?1016h")
	if !term.GetViewFlag(VFMousePixel) {
		t.Fatal("DECSET 1016 did not set VFMousePixel")
	}
	feedText(t, term, "\x1b[?1016l")
	if term.GetViewFlag(VFMousePixel) {
		t.Fatal("DECRST 1016 did not clear VFMousePixel")
	}
}

func TestAltScreenAllowedMode(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if !term.GetViewFlag(VFAltScreenAllowed) {
		t.Fatal("alt-screen switching forbidden by default")
	}

	feedText(t, term, "\x1b[?1046l")
	if term.GetViewFlag(VFAltScreenAllowed) {
		t.Fatal("DECRST 1046 did not forbid alt-screen switching")
	}
	for _, seq := range []string{"\x1b[?1049h", "\x1b[?1047h", "\x1b[?47h"} {
		feedText(t, term, seq)
		if term.onAltScreen {
			t.Fatalf("%q switched to the alt screen while mode 1046 forbids it", seq)
		}
	}

	feedText(t, term, "\x1b[?1046h")
	if !term.GetViewFlag(VFAltScreenAllowed) {
		t.Fatal("DECSET 1046 did not allow alt-screen switching")
	}
	feedText(t, term, "\x1b[?1049h")
	if !term.onAltScreen {
		t.Fatal("alt screen not entered after 1046 was set")
	}
}

func TestNewPrivateModes_DECRQM(t *testing.T) {
	r, term, _ := MakeTerminalWithMock(TextReadModeRune)
	tests := []struct {
		seq  string
		want string
	}{
		{"\x1b[?1007$p", "\x1b[?1007;2$y"},
		{"\x1b[?1016$p", "\x1b[?1016;2$y"},
		{"\x1b[?1046$p", "\x1b[?1046;1$y"},
	}
	for _, tc := range tests {
		feedQueryExpectReply(t, term, r, tc.seq, tc.want)
	}
	feedText(t, term, "\x1b[?1007h\x1b[?1016h\x1b[?1046l")
	for _, tc := range []struct{ seq, want string }{
		{"\x1b[?1007$p", "\x1b[?1007;1$y"},
		{"\x1b[?1016$p", "\x1b[?1016;1$y"},
		{"\x1b[?1046$p", "\x1b[?1046;2$y"},
	} {
		feedQueryExpectReply(t, term, r, tc.seq, tc.want)
	}
}

func TestAltScreenAllowed_RISRestores(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b[?1046l\x1bc")
	if !term.GetViewFlag(VFAltScreenAllowed) {
		t.Fatal("RIS did not restore alt-screen permission")
	}
}
