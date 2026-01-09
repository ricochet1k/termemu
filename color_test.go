package termemu

import (
	"bytes"
	"strings"
	"testing"
)

func TestColor_Roundtrip(t *testing.T) {
	var c Color

	// set 256-color
	c = c.SetColor(Color(123))
	if got := c.Color(); got != 123 {
		t.Fatalf("Color() = %d; want 123", got)
	}
	if ct := c.ColorType(); ct != ColorType256 {
		t.Fatalf("ColorType() = %v; want ColorType256", ct)
	}

	// set RGB
	c = c.SetColorRGB(10, 20, 30)
	if r, g, b := c.ColorRGB(); r != 10 || g != 20 || b != 30 {
		t.Fatalf("ColorRGB() = %v,%v,%v; want 10,20,30", r, g, b)
	}
	if ct := c.ColorType(); ct != ColorTypeRGB {
		t.Fatalf("ColorType() = %v; want ColorTypeRGB", ct)
	}
}

func TestColor_Modes(t *testing.T) {
	var c Color
	c = c.SetMode(ModeBold)
	if !c.TestMode(ModeBold) {
		t.Fatalf("TestMode(ModeBold) = false; want true")
	}

	c = c.SetMode(ModeUnderline)
	modes := c.Modes()
	foundBold := false
	foundUnderline := false
	for _, m := range modes {
		if m == ModeBold {
			foundBold = true
		}
		if m == ModeUnderline {
			foundUnderline = true
		}
	}
	if !foundBold || !foundUnderline {
		t.Fatalf("Modes() = %v; want contain ModeBold and ModeUnderline", modes)
	}

	c = c.ResetMode(ModeBold)
	if c.TestMode(ModeBold) {
		t.Fatalf("ModeBold still set after ResetMode")
	}
}

func TestANSIEscape_Sequences(t *testing.T) {
	// Bold + fg 3 (yellow) + bg 10 (>=8 -> 48;5;10m)
	fg := Color(0).SetMode(ModeBold).SetColor(Colors8[3])
	bg := Color(0).SetColor(Color(10))
	seq := ANSIEscape(fg, bg)
	s := string(seq)
	if !strings.Contains(s, "\x1b[1m") {
		t.Fatalf("ANSIEscape missing bold sequence: %q", s)
	}
	if !strings.Contains(s, "\x1b[33m") {
		t.Fatalf("ANSIEscape missing fg 33 sequence: %q", s)
	}
	if !strings.Contains(s, "\x1b[48;5;10m") {
		t.Fatalf("ANSIEscape missing bg 48;5;10 sequence: %q", s)
	}

	// RGB colors produce 38;2 and 48;2 sequences
	fg2 := Color(0).SetColorRGB(1, 2, 3)
	bg2 := Color(0).SetColorRGB(4, 5, 6)
	seq2 := ANSIEscape(fg2, bg2)
	if !bytes.Contains(seq2, []byte("\x1b[38;2;1;2;3m")) {
		t.Fatalf("ANSIEscape missing fg RGB seq: %q", string(seq2))
	}
	if !bytes.Contains(seq2, []byte("\x1b[48;2;4;5;6m")) {
		t.Fatalf("ANSIEscape missing bg RGB seq: %q", string(seq2))
	}
}
