package termemu

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// checkScreenInvariants asserts the structural invariants every screen state
// must satisfy: full-width lines whose width matches their spans, a cursor in
// bounds, and valid scroll margins.
func checkScreenInvariants(t *testing.T, s screen, context string) {
	t.Helper()
	size := s.Size()
	w, h := size.X, size.Y
	if w <= 0 || h <= 0 {
		t.Fatalf("%s: invalid size %dx%d", context, w, h)
	}
	ss, ok := s.(*spanScreen)
	if !ok {
		t.Fatalf("%s: unexpected screen implementation %T", context, s)
	}
	if len(ss.lines) != h {
		t.Fatalf("%s: have %d lines, want %d", context, len(ss.lines), h)
	}
	if len(ss.wrapped) != h {
		t.Fatalf("%s: have %d wrapped flags, want %d", context, len(ss.wrapped), h)
	}
	if len(ss.tabs) != w {
		t.Fatalf("%s: have %d tab stops, want %d", context, len(ss.tabs), w)
	}
	for y := range ss.lines {
		line := &ss.lines[y]
		sum := 0
		for i, sp := range line.spans {
			if sp.Width < 0 {
				t.Fatalf("%s: line %d span %d has negative width: %#v", context, y, i, sp)
			}
			sum += sp.Width
		}
		if line.width != sum {
			t.Fatalf("%s: line %d width = %d, sum(span.Width) = %d", context, y, line.width, sum)
		}
		if line.width != w {
			t.Fatalf("%s: line %d width = %d, want %d", context, y, line.width, w)
		}
		if styled := s.StyledLine(0, w, y); styled.Width != w {
			t.Fatalf("%s: StyledLine %d width = %d, want %d", context, y, styled.Width, w)
		}
	}
	pos := s.CursorPos()
	if pos.X < 0 || pos.X >= w || pos.Y < 0 || pos.Y >= h {
		t.Fatalf("%s: cursor %+v outside %dx%d", context, pos, w, h)
	}
	top, bottom := s.TopMargin(), s.BottomMargin()
	if top < 0 || top > bottom || bottom >= h {
		t.Fatalf("%s: invalid scroll margins %d..%d for height %d", context, top, bottom, h)
	}
	if ss.originMode && (pos.Y < top || pos.Y > bottom) {
		t.Fatalf("%s: cursor %+v outside origin-mode margins %d..%d", context, pos, top, bottom)
	}
}

// screenInputChunks is a corpus of text, controls, escape commands, and
// control strings. A random concatenation of these exercises the parser and
// screen state machine together.
var screenInputChunks = []string{
	"a", "hello", " ", "世界", "🐹", "e\u0301", "\u200d", "👍🏽", "1\ufe0f\u20e3",
	"\r", "\n", "\t", "\b", "\x00", "\x7f", "\xff", "\x9c",
	"\x1b[H", "\x1b[2J", "\x1b[K", "\x1b[1K", "\x1b[2K", "\x1b[J",
	"\x1b[1;5H", "\x1b[999;999H", "\x1b[10A", "\x1b[10B", "\x1b[10C", "\x1b[10D",
	"\x1b[2;4r", "\x1b[r", "\x1b[L", "\x1b[2M", "\x1b[S", "\x1b[2T", "\x1b[P", "\x1b[X",
	"\x1b[38;5;196m", "\x1b[48;2;1;2;3m", "\x1b[1;4;31m", "\x1b[0m", "\x1b[22;24m",
	"\x1b[?7l", "\x1b[?7h", "\x1b[?25l", "\x1b[?25h", "\x1b[?2026h", "\x1b[?2026l",
	"\x1b[?1049h", "\x1b[?1049l", "\x1b[?47h", "\x1b[?47l", "\x1b[?1048h", "\x1b[?1048l",
	"\x1b]0;title\x07", "\x1b]8;;https://example.com\x1b\\", "\x1b]8;;\x1b\\",
	"\x1b_apc payload\x1b\\", "\x1bPdcs payload\x1b\\", "\x1b^pm payload\x1b\\", "\x1bXsos payload\x1b\\",
	"\x1b#8", "\x1bc", "\x1b7", "\x1b8", "\x1b[?u", "\x1b[>4;2m", "\x1b[=1;1u", "\x1b[<u",
	"\x1b[6n", "\x1b[c", "\x1b[>c", "\x1b[>q", "\x1b[?2026$p",
	"\x1b[@", "\x1b[3@", "\x1b[4h", "\x1b[4l", "\x1bH", "\x1b[g", "\x1b[3g",
	"\x1b[2I", "\x1b[2Z", "\x1b[3b", "\x1b[?6h", "\x1b[?6l", "\x1b[5;12r",
	"\x1b[20h", "\x1b[20l", "\x1bE", "\x1b[2 q", "\x1b[ x", "\x1b[4$p", "\x1b[20$p",
	"\x1b[38:2::1:2:3m", "\x1b[38:5:9m", "\x1b[58:2::4:5:6m", "\x1b[58;5;7m", "\x1b[59m",
	"\x1b]133;A\a", "\x1b]133;D;1\a", "\x1b]9;hi\a", "\x1b]777;notify;t;b\a",
}

func TestScreenRandomInputInvariants(t *testing.T) {
	for _, mode := range []TextReadMode{TextReadModeGrapheme, TextReadModeRune} {
		mode := mode
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			for seed := int64(1); seed <= 20; seed++ {
				term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
				rng := rand.New(rand.NewSource(seed))
				var saved *Snapshot
				for step := 0; step < 200; step++ {
					var input []byte
					for n := 1 + rng.Intn(6); n > 0; n-- {
						input = append(input, screenInputChunks[rng.Intn(len(screenInputChunks))]...)
					}
					if err := term.testFeedTerminalInputFromBackend(input, mode); err != nil && !errors.Is(err, io.EOF) {
						t.Fatalf("seed %d step %d: feed %q: %v", seed, step, input, err)
					}
					context := fmt.Sprintf("seed %d step %d", seed, step)
					checkScreenInvariants(t, term.screen(), context)

					switch rng.Intn(40) {
					case 0:
						if err := term.Resize(1+rng.Intn(40), 1+rng.Intn(12)); err != nil {
							t.Fatalf("%s: resize: %v", context, err)
						}
					case 1:
						snap := term.Snapshot()
						saved = &snap
						if err := term.Restore(snap); err != nil {
							t.Fatalf("%s: restore: %v", context, err)
						}
					case 2:
						if saved != nil {
							if err := term.Restore(*saved); err != nil {
								t.Fatalf("%s: restore saved: %v", context, err)
							}
							if w, h := term.Size(); w != saved.Main.Width || h != saved.Main.Height {
								t.Fatalf("%s: restore size = %dx%d, want %dx%d", context, w, h, saved.Main.Width, saved.Main.Height)
							}
						}
					}
					checkScreenInvariants(t, term.screen(), context+" after resize/restore")
				}
			}
		})
	}
}

func FuzzTerminalInput(f *testing.F) {
	for _, seed := range screenInputChunks {
		f.Add([]byte(seed))
	}
	f.Add([]byte("hello\x1b[31m world\x1b[0m\r\n"))
	f.Add([]byte("\x1b[?1049h\x1b[2J\x1b[Htest\x1b[?1049l"))
	f.Add([]byte("\x1b]0;title\x07\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"))
	f.Add([]byte("👨\u200d👩\u200d👧\u200d👦界世e\u0301"))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, mode := range []TextReadMode{TextReadModeGrapheme, TextReadModeRune} {
			term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
			if err := term.testFeedTerminalInputFromBackend(data, mode); err != nil && !errors.Is(err, io.EOF) {
				t.Fatalf("mode %d: feed %q: %v", mode, data, err)
			}
			context := fmt.Sprintf("mode %d input %q", mode, data)
			checkScreenInvariants(t, term.screen(), context)

			snap := term.Snapshot()
			if err := term.Restore(snap); err != nil {
				t.Fatalf("%s: restore: %v", context, err)
			}
			checkScreenInvariants(t, term.screen(), context+" after restore")
		}
	})
}

func TestResizeSmallerClampsCursor(t *testing.T) {
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(10, 5)
	s.setCursorPos(9, 4)
	s.saveCursorPos()
	s.setSize(2, 3)
	if got := s.CursorPos(); got != (Pos{X: 1, Y: 2}) {
		t.Fatalf("cursor after shrink = %+v, want {1 2}", got)
	}
	s.restoreCursorPos()
	if got := s.CursorPos(); got != (Pos{X: 1, Y: 2}) {
		t.Fatalf("restored cursor after shrink = %+v, want {1 2}", got)
	}
}

func TestResizeTruncatingWideLineKeepsFullWidth(t *testing.T) {
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(10, 1)
	s.rawWriteSpan(0, 0, Span{Style: NewStyle(), Text: "世界世", Width: 6}, CRText)
	s.setSize(5, 1)
	line := &s.lines[0]
	if line.width != 5 {
		t.Fatalf("line width after shrink = %d, want 5", line.width)
	}
	sum := 0
	for _, sp := range line.spans {
		sum += sp.Width
	}
	if sum != 5 {
		t.Fatalf("span width sum = %d, want 5 (spans %#v)", sum, line.spans)
	}
}

func TestScrollLargeDeltaClampsToRegion(t *testing.T) {
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(4, 6)
	for y := 0; y < 6; y++ {
		s.rawWriteSpan(0, y, Span{Style: NewStyle(), Text: string(rune('a' + y)), Width: 1}, CRText)
	}
	s.setScrollMarginTopBottom(1, 3)

	s.scroll(1, 3, -100)
	for y := 1; y <= 3; y++ {
		if got := strings.TrimRight(s.Line(y), " "); got != "" {
			t.Fatalf("line %d = %q after huge upward scroll, want blank", y, got)
		}
	}
	if got := strings.TrimRight(s.Line(0), " "); got != "a" {
		t.Fatalf("line 0 changed to %q", got)
	}
	if got := strings.TrimRight(s.Line(4), " "); got != "e" {
		t.Fatalf("line 4 changed to %q", got)
	}

	s.scroll(1, 3, 100)
	for y := 1; y <= 3; y++ {
		if got := strings.TrimRight(s.Line(y), " "); got != "" {
			t.Fatalf("line %d = %q after huge downward scroll, want blank", y, got)
		}
	}
}
