package termemu

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// newEncodeTerm returns a terminal used only for key/mouse encoding. Encoding
// does not mutate it, so a fuzz iteration can reuse it.
func newEncodeTerm() (*terminal, *bytes.Buffer) {
	out := &bytes.Buffer{}
	// newTerminal does not start a read loop, so the fuzz body can set
	// keyboard flags and view ints without taking the lock.
	return newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, out), TextReadModeGrapheme), out
}

// TestKeyEncodingProperties is the seed-driven companion to FuzzKeyEncoding; it
// keeps the same checks running in a normal `go test`.
func TestKeyEncodingProperties(t *testing.T) {
	cases := []KeyEvent{
		{Code: KeyRune, Rune: 'a'},
		{Code: KeyRune, Rune: 'a', Mod: ModCtrl},
		{Code: KeyRune, Rune: 'Z', Mod: ModShift | ModAlt},
		{Code: KeyUp},
		{Code: KeyUp, Mod: ModShift},
		{Code: KeyTab},
		{Code: KeyBackspace},
		{Code: KeyEnter},
		{Code: KeyDelete, Event: KeyRelease},
		{Code: KeyRune, Rune: 'x', Text: []rune("xy"), Shifted: 'X', BaseLayout: 'x'},
	}
	for _, flags := range []int{0, 1, 2, 4, 8, 15, 31} {
		for _, ev := range cases {
			checkKeyEncoding(t, flags, ev)
		}
	}
}

func checkKeyEncoding(t *testing.T, flags int, ev KeyEvent) {
	t.Helper()
	term, _ := newEncodeTerm()
	term.keyboardMain.flags = flags
	got := term.encodeKey(ev)
	if !utf8.Valid(got) {
		t.Fatalf("flags=%d event=%+v: encoding %q is not valid UTF-8", flags, ev, got)
	}
	if want := term.encodeKey(ev); !bytes.Equal(got, want) {
		t.Fatalf("flags=%d event=%+v: encoding is not deterministic: %q vs %q", flags, ev, got, want)
	}
	// The encoded length is linear in the associated text (each codepoint is
	// emitted as a decimal number plus a separator in Kitty mode).
	if maxLen := 64 + 8*len(ev.Text); len(got) > maxLen {
		t.Fatalf("flags=%d event=%+v: encoding is %d bytes (limit %d): %q", flags, ev, len(got), maxLen, got)
	}
	// Without enhancement flags the Kitty encoder must not be used.
	if flags == 0 {
		if want := term.encodeLegacyKey(ev); !bytes.Equal(got, want) {
			t.Fatalf("flags=0 event=%+v: encodeKey %q != encodeLegacyKey %q", ev, got, want)
		}
	}
}

func FuzzKeyEncoding(f *testing.F) {
	f.Add(uint8(KeyUp), []byte("a"), []byte("X"), []byte("a"), []byte("a"), uint8(ModShift|ModAlt), uint8(KeyPress), uint8(15))
	f.Add(uint8(KeyRune), []byte("é"), []byte(nil), []byte(nil), []byte(nil), uint8(ModCtrl), uint8(KeyRelease), uint8(31))
	f.Add(uint8(KeyDelete), []byte(nil), []byte(nil), []byte(nil), []byte(nil), uint8(0), uint8(KeyRepeat), uint8(0))

	f.Fuzz(func(t *testing.T, rawCode uint8, runeBytes []byte, shiftedBytes []byte, baseBytes []byte, textBytes []byte, mods uint8, event uint8, flags uint8) {
		ev := KeyEvent{
			Code:  KeyCode(int(rawCode) % (int(KeyISOLevel5Shift) + 3)),
			Mod:   KeyMod(mods),
			Event: KeyEventType(int(event) % 5),
		}
		if r, ok := firstRune(runeBytes); ok {
			ev.Rune = r
		}
		if r, ok := firstRune(shiftedBytes); ok {
			ev.Shifted = r
		}
		if r, ok := firstRune(baseBytes); ok {
			ev.BaseLayout = r
		}
		for _, r := range string(textBytes) {
			ev.Text = append(ev.Text, r)
		}
		checkKeyEncoding(t, int(flags)%32, ev)
	})
}

// firstRune decodes the first rune of data, reporting whether one was present.
func firstRune(data []byte) (rune, bool) {
	if len(data) == 0 {
		return 0, false
	}
	r, _ := utf8.DecodeRune(data)
	return r, true
}

// mouseReport is a decoded mouse report.
type mouseReport struct {
	btn   byte
	x, y  int
	press bool
}

// decodeMouseReport parses the report written for encoding. It returns false
// when no report was written.
func decodeMouseReport(t *testing.T, encoding int, data []byte) (mouseReport, bool) {
	t.Helper()
	if len(data) == 0 {
		return mouseReport{}, false
	}
	switch encoding {
	case MEX10:
		if len(data) != 6 || !bytes.HasPrefix(data, []byte("\033[M")) {
			t.Fatalf("X10 report %q is malformed", data)
		}
		for _, b := range data[3:] {
			if b < 32 {
				t.Fatalf("X10 report %q has a byte below 32", data)
			}
		}
		btn := data[3] - 32
		return mouseReport{btn: btn, x: int(data[4]) - 32, y: int(data[5]) - 32, press: btn&byte(MRelease) != byte(MRelease)}, true
	case MEUTF8:
		if !bytes.HasPrefix(data, []byte("\033[M")) || len(data) < 4 {
			t.Fatalf("UTF-8 report %q is malformed", data)
		}
		rest := data[3:]
		if !utf8.Valid(rest) {
			t.Fatalf("UTF-8 report %q is not valid UTF-8", data)
		}
		// 1005 encodes all three parameters (button included) as codepoints.
		runes := []rune(string(rest))
		if len(runes) != 3 {
			t.Fatalf("UTF-8 report %q has %d parameter runes", data, len(runes))
		}
		btn := byte(runes[0] - 32)
		return mouseReport{btn: btn, x: int(runes[1]) - 32, y: int(runes[2]) - 32, press: btn&byte(MRelease) != byte(MRelease)}, true
	case MESGR, MEURXVT:
		s := string(data)
		prefix := "\033[<"
		prefixLen := 3
		if encoding == MEURXVT {
			prefix = "\033["
			prefixLen = 2
		}
		if !strings.HasPrefix(s, prefix) {
			t.Fatalf("report %q does not start with %q", data, prefix)
		}
		body := s[prefixLen:]
		final := body[len(body)-1]
		body = body[:len(body)-1]
		parts := strings.Split(body, ";")
		if len(parts) != 3 {
			t.Fatalf("report %q does not have three parameters", data)
		}
		nums := make([]int, 3)
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				t.Fatalf("report %q parameter %d: %v", data, i, err)
			}
			nums[i] = n
		}
		rep := mouseReport{x: nums[1], y: nums[2], press: true}
		if encoding == MESGR {
			rep.btn = byte(nums[0])
			rep.press = final == 'M'
		} else {
			if final != 'M' {
				t.Fatalf("urxvt report %q has final byte %q", data, final)
			}
			rep.btn = byte(nums[0] - 32)
			rep.press = rep.btn&byte(MRelease) != byte(MRelease)
		}
		return rep, true
	}
	t.Fatalf("unknown encoding %d", encoding)
	return mouseReport{}, false
}

func TestMouseEncodingProperties(t *testing.T) {
	for _, encoding := range []int{MEX10, MEUTF8, MESGR, MEURXVT} {
		for _, mode := range []int{MMNone, MMPress, MMPressRelease, MMPressReleaseMove, MMPressReleaseMoveAll} {
			for _, btn := range []MouseBtn{MBtn1, MBtn2, MBtn3, MRelease} {
				for _, mods := range []MouseFlag{0, MShift, MMeta, MControl, MMotion, MWheel} {
					for _, press := range []bool{true, false} {
						checkMouseEncoding(t, mode, encoding, btn, mods, press, 5, 3)
					}
				}
			}
		}
	}
}

func checkMouseEncoding(t *testing.T, mode, encoding int, btn MouseBtn, mods MouseFlag, press bool, x, y int) {
	t.Helper()
	mods &= MShift | MMeta | MControl | MMotion | MWheel
	term, out := newEncodeTerm()
	term.viewInts[VIMouseMode] = mode
	term.viewInts[VIMouseEncoding] = encoding
	out.Reset()

	if err := term.SendMouseRaw(btn, press, mods, x, y); err != nil {
		t.Fatalf("mode=%d encoding=%d: SendMouseRaw: %v", mode, encoding, err)
	}
	data := out.Bytes()
	// X10 is a byte protocol; every other encoding emits UTF-8/ASCII.
	if encoding != MEX10 && !utf8.Valid(data) {
		t.Fatalf("mode=%d encoding=%d btn=%d mods=%d: report %q is not valid UTF-8", mode, encoding, btn, mods, data)
	}

	// The mode filters run before encoding.
	wantSilent := false
	switch mode {
	case MMNone:
		wantSilent = true
	case MMPress:
		wantSilent = !press
	case MMPressRelease:
		wantSilent = mods&MMotion != 0
	case MMPressReleaseMove:
		// Motion without a held button is a 1003 (all-motion) event.
		wantSilent = mods&MMotion != 0 && btn&MRelease == MRelease
	}
	if wantSilent {
		if len(data) != 0 {
			t.Fatalf("mode=%d encoding=%d: expected no report, got %q", mode, encoding, data)
		}
		return
	}

	rep, ok := decodeMouseReport(t, encoding, data)
	if !ok {
		t.Fatalf("mode=%d encoding=%d: no report written", mode, encoding)
	}
	wantBtn := (byte(btn) & mWhichBtn) | byte(mods)
	if encoding != MESGR && !press {
		// The X10 family encodes a release by setting the low button bits to
		// 3, which is indistinguishable from a press of button 3 - that
		// ambiguity is inherent to the protocol.
		wantBtn |= byte(MRelease)
	}
	wantPress := press
	if encoding != MESGR {
		wantPress = press && wantBtn&byte(MRelease) != byte(MRelease)
	}
	if rep.press != wantPress {
		t.Fatalf("mode=%d encoding=%d: release flag round-tripped as %v, want %v (%q)", mode, encoding, rep.press, wantPress, data)
	}
	if rep.btn != wantBtn {
		t.Fatalf("mode=%d encoding=%d: button byte %d, want %d (%q)", mode, encoding, rep.btn, wantBtn, data)
	}
	wantX, wantY := x, y
	if encoding == MEX10 {
		// X10 clamps coordinates to the representable range.
		if wantX > 223 {
			wantX = 223
		}
		if wantY > 223 {
			wantY = 223
		}
	}
	if rep.x != wantX || rep.y != wantY {
		t.Fatalf("mode=%d encoding=%d: coords %d,%d round-tripped as %d,%d (%q)",
			mode, encoding, x, y, rep.x, rep.y, data)
	}
}

func FuzzMouseEncoding(f *testing.F) {
	f.Add(uint8(MMPressRelease), uint8(MEX10), uint8(MBtn1), uint8(0), true, uint16(1), uint16(1))
	f.Add(uint8(MMPressReleaseMoveAll), uint8(MESGR), uint8(MBtn3), uint8(MShift), false, uint16(80), uint16(24))
	f.Add(uint8(MMPress), uint8(MEURXVT), uint8(MRelease), uint8(MMotion), true, uint16(999), uint16(0))
	f.Add(uint8(MMPressRelease), uint8(MEUTF8), uint8(MBtn2), uint8(MControl|MMotion), true, uint16(300), uint16(700))

	f.Fuzz(func(t *testing.T, mode, encoding, btn, mods uint8, press bool, x, y uint16) {
		checkMouseEncoding(t,
			int(mode)%5,
			int(encoding)%4,
			MouseBtn(btn),
			MouseFlag(mods),
			press,
			int(x%2000),
			int(y%2000))
	})
}

// collectWholeCommands parses data as one buffer, returning the sequence of
// complete escape commands.
func collectWholeCommands(data []byte) [][]byte {
	var cmds [][]byte
	r := bytes.NewReader(data)
	for {
		cmd, err := readEscapeCommand(nil, r, nil)
		if err != nil {
			return cmds
		}
		cmds = append(cmds, append([]byte(nil), cmd...))
	}
}

// collectResumedCommands feeds data one byte at a time, carrying the partial
// command across read boundaries exactly like Pause/Resume does.
func collectResumedCommands(data []byte) [][]byte {
	var cmds [][]byte
	var partial []byte
	for i := 0; i < len(data); i++ {
		cmd, err := readEscapeCommand(nil, bytes.NewReader(data[i:i+1]), partial)
		if err != nil {
			partial = append([]byte(nil), cmd...)
			if len(partial) == 0 {
				partial = []byte{0x1b}
			}
			continue
		}
		cmds = append(cmds, append([]byte(nil), cmd...))
		partial = nil
	}
	return cmds
}

func FuzzEscapeCommandResume(f *testing.F) {
	f.Add([]byte("\x1b[12;3Hrest"))
	f.Add([]byte("\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"))
	f.Add([]byte("\x1bP1;2|x\x1b\\\x1b(B\x1b(0q"))
	f.Add([]byte("\x1b"))
	f.Add([]byte("\x1b["))
	f.Add([]byte("text\x1b]0;title\x07more"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip("input too large for the byte-wise replay")
		}
		want := collectWholeCommands(data)
		got := collectResumedCommands(data)
		if len(got) != len(want) {
			t.Fatalf("resumed parse produced %d commands, want %d\ncommands: %q\nwant: %q", len(got), len(want), got, want)
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("command %d = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// quietLineEquivalent compares lines ignoring OSC 8 links: the TTY frontend
// intentionally does not re-emit hyperlinks.
func quietLineEquivalent(a, b Line) bool {
	strip := func(l Line) Line {
		out := Line{Width: l.Width, Spans: make([]Span, len(l.Spans))}
		copy(out.Spans, l.Spans)
		for i := range out.Spans {
			out.Spans[i].Link = ""
		}
		return out
	}
	return strip(a).Equivalent(strip(b))
}

func FuzzTTYFrontendRoundTrip(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte("\x1b[31mred\x1b[0m \x1b[1;4mbold\x1b[0m"))
	f.Add([]byte("a\r\nb\tc\x1b[4;1HXYZ"))
	f.Add([]byte("世界界 e\u0301 \U0001F439"))
	f.Add([]byte("\x1b[2J\x1b[3;3Hout"))
	f.Add([]byte("\x1b[?1049halt"))
	f.Add([]byte("\x1b(0lqqqk\x1b(B\x1b)0\x0eq\x0f"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2048 {
			t.Skip("input too large for a per-iteration screen")
		}
		const w, h = 12, 5
		region := Region{X: 0, Y: 0, X2: w, Y2: h}

		var buf bytes.Buffer
		frontend := NewTTYFrontend(&buf)
		inner := newTerminal(frontend, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeGrapheme)
		frontend.SetTerminal(inner)
		if err := inner.Resize(w, h); err != nil {
			t.Fatalf("inner Resize: %v", err)
		}
		frontend.Attach(region)
		if err := inner.testFeedTerminalInputFromBackend(data, TextReadModeGrapheme); err != nil && err != io.EOF {
			t.Fatalf("inner feed: %v", err)
		}
		frontend.Detach()

		outer := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), TextReadModeGrapheme)
		if err := outer.Resize(w, h); err != nil {
			t.Fatalf("outer Resize: %v", err)
		}
		if err := outer.testFeedTerminalInputFromBackend(buf.Bytes(), TextReadModeGrapheme); err != nil && err != io.EOF {
			t.Fatalf("outer feed: %v", err)
		}

		innerLines := inner.StyledLines(region)
		outerLines := outer.StyledLines(region)
		for y := range innerLines {
			if quietLineEquivalent(innerLines[y], outerLines[y]) {
				continue
			}
			t.Fatalf("input %q: line %d differs after the TTY frontend round trip\ninner: %+v\nouter: %+v\nrendered: %q",
				data, y, innerLines[y], outerLines[y], buf.String())
		}
	})
}

// TestFuzzTargetQuietLineEquivalentSanity keeps the helper honest.
func TestFuzzTargetQuietLineEquivalentSanity(t *testing.T) {
	a := Line{Spans: []Span{{Text: "a", Width: 1, Link: "https://x"}, {Width: 2}}, Width: 3}
	b := Line{Spans: []Span{{Text: "a", Width: 1}, {Width: 2}}, Width: 3}
	if !quietLineEquivalent(a, b) {
		t.Fatal("quietLineEquivalent must ignore links")
	}
	if quietLineEquivalent(a, Line{Spans: []Span{{Text: "b", Width: 1}, {Width: 2}}, Width: 3}) {
		t.Fatal("quietLineEquivalent must compare content")
	}
}
