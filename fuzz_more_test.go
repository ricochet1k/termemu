package termemu

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzTerminalResizeRestore drives arbitrary screen content through
// resize/restore combinations, including restoring a snapshot captured at a
// different size than the one currently on screen.
func FuzzTerminalResizeRestore(f *testing.F) {
	f.Add([]byte("hello世界"), uint8(5), uint8(3))
	f.Add([]byte("a\x1b[2Jb\x1b[?1049hc"), uint8(2), uint8(6))
	f.Add([]byte("👨\u200d👩e\u0301🇺🇸"), uint8(9), uint8(1))
	f.Add([]byte(strings.Repeat("line\r\n", 30)), uint8(12), uint8(4))

	f.Fuzz(func(t *testing.T, data []byte, width, height uint8) {
		for _, mode := range []TextReadMode{TextReadModeGrapheme, TextReadModeRune} {
			term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
			startW, startH := int(width)%30+1, int(height)%8+1
			if err := term.Resize(startW, startH); err != nil {
				t.Fatalf("initial resize: %v", err)
			}
			feedTerminalReader(t, term, bytes.NewReader(data), mode)
			checkScreenInvariants(t, term.screen(), "after input")

			snap := term.Snapshot()
			snapW, snapH := term.Size()

			// Resize to another size, then restore the earlier snapshot. The
			// terminal must return to the snapshot dimensions.
			targetW, targetH := int(height)%20+1, int(width)%8+1
			if err := term.Resize(targetW, targetH); err != nil {
				t.Fatalf("resize: %v", err)
			}
			checkScreenInvariants(t, term.screen(), "after resize")

			if err := term.Restore(snap); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if w, h := term.Size(); w != snapW || h != snapH {
				t.Fatalf("size after restore = %dx%d, want %dx%d", w, h, snapW, snapH)
			}
			checkScreenInvariants(t, term.screen(), "after restore")
		}
	})
}

// FuzzScrollbackLogicalLine stresses LogicalLine's backward walk, trailing
// blank trim, and reflow with arbitrary row contents, wrap flags, and terminal
// widths.
func FuzzScrollbackLogicalLine(f *testing.F) {
	f.Add([]byte("hello world 12345"), uint8(3), uint8(0b101), uint8(5), uint16(0))
	f.Add([]byte("世界e\u0301🇺🇸👍🏽"), uint8(4), uint8(0b1111), uint8(2), uint16(3))
	f.Add([]byte("a"), uint8(1), uint8(0), uint8(1), uint16(0))

	f.Fuzz(func(t *testing.T, data []byte, rowCount, wrapMask, width uint8, pick uint16) {
		mode := TextReadModeGrapheme
		term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
		WithScrollback(64)(term)
		w := int(width)%40 + 1
		if err := term.Resize(w, 4); err != nil {
			t.Fatalf("resize: %v", err)
		}
		// The shrink captures the old blank screen; start with clean history.
		term.Scrollback().Clear()

		text := sanitizeUTF8(string(data))
		n := int(rowCount)%8 + 1
		for i := 0; i < n; i++ {
			start := len(text) * i / n
			end := len(text) * (i + 1) / n
			line := spanLineFromText(text[start:end], mode, NewStyle())
			wrapped := wrapMask&(1<<uint(i%8)) != 0
			term.withLock(func() {
				term.addScrollRow(line.spans, line.width, wrapped)
			})
		}

		sb := term.Scrollback()
		base, next := sb.Range()
		if base >= next {
			return
		}
		id := base + ScrollID(uint64(pick)%uint64(next-base))
		rows := sb.LogicalLine(id)
		if len(rows) == 0 {
			t.Fatalf("LogicalLine(%d) returned no rows", id)
		}
		lastID := ScrollID(0)
		for i, row := range rows {
			if row.ID == 0 || row.ID < lastID {
				t.Fatalf("row %d has ID %d, previous %d", i, row.ID, lastID)
			}
			lastID = row.ID
			sum := 0
			for _, sp := range row.Spans {
				if sp.Width < 0 || !utf8.ValidString(sp.Text) {
					t.Fatalf("row %d has bad span %#v", i, sp)
				}
				sum += sp.Width
			}
			if sum != row.Width {
				t.Fatalf("row %d width = %d, span sum = %d (spans %#v)", i, row.Width, sum, row.Spans)
			}
			if row.Width > w && w > 1 {
				// Width 1 cannot represent a wide cluster; reflow keeps it whole.
				t.Fatalf("row %d width = %d exceeds terminal width %d", i, row.Width, w)
			}
		}
	})
}

// FuzzHandoffTargetSize checks that a Handoff captured at one size can be
// restored at another target size and still produce a structurally valid
// screen with the requested dimensions.
func FuzzHandoffTargetSize(f *testing.F) {
	f.Add([]byte("hello\r\nworld"), uint8(7), uint8(3))
	f.Add([]byte("世界👨\u200d👩"), uint8(3), uint8(5))
	f.Add([]byte("a\x1b[2Jb"), uint8(1), uint8(1))

	f.Fuzz(func(t *testing.T, data []byte, width, height uint8) {
		mode := TextReadModeGrapheme
		old := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), mode)
		if err := old.Resize(12, 4); err != nil {
			t.Fatalf("resize: %v", err)
		}
		feedTerminalReader(t, old, bytes.NewReader(data), mode)
		h, err := old.Pause(context.Background())
		if err != nil {
			t.Fatalf("Pause: %v", err)
		}

		targetW, targetH := int(width)%20+1, int(height)%8+1
		h.Size = Pos{X: targetW, Y: targetH}
		newTerm, err := NewFromHandoff(&EmptyFrontend{}, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), h)
		if err != nil {
			t.Fatalf("NewFromHandoff: %v", err)
		}
		defer func() {
			_ = newTerm.Close()
			select {
			case <-newTerm.Done():
			case <-time.After(time.Second):
			}
		}()

		if w, h := newTerm.Size(); w != targetW || h != targetH {
			t.Fatalf("size = %dx%d, want %dx%d", w, h, targetW, targetH)
		}
		checkScreenInvariants(t, newTerm.(*terminal).screen(), "handoff target")
		if got := newTerm.Scrollback().Len(); got != len(h.Scrollback.Rows) {
			t.Fatalf("scrollback Len = %d, want %d", got, len(h.Scrollback.Rows))
		}
	})
}
