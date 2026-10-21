package termemu

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// recordingFrontend embeds MockFrontend and implements ScrollbackListener.
// MockFrontend itself deliberately stays unchanged.
type recordingFrontend struct {
	*MockFrontend

	mu      sync.Mutex
	batches []scrollbackBatch
}

type scrollbackBatch struct {
	first, next ScrollID
	rows        []Row
}

func newRecordingFrontend() *recordingFrontend {
	return &recordingFrontend{MockFrontend: NewMockFrontend()}
}

func (f *recordingFrontend) ScrollbackRowsAppended(v LockedView, first, next ScrollID) {
	// Read the new rows through the no-lock view. This must not deadlock.
	sb := v.Scrollback()
	rows := make([]Row, 0, int(next-first))
	for id := first; id < next; id++ {
		row, ok := sb.Row(id)
		if !ok {
			continue
		}
		rows = append(rows, row)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, scrollbackBatch{first: first, next: next, rows: rows})
}

func (f *recordingFrontend) recorded() []scrollbackBatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]scrollbackBatch, len(f.batches))
	copy(out, f.batches)
	return out
}

// newRecordingTerminal builds a terminal whose frontend also implements
// ScrollbackListener.
func newRecordingTerminal(mode TextReadMode) (*terminal, *recordingFrontend) {
	f := newRecordingFrontend()
	t := newTerminal(f, NewNoPTYBackend(nil, nil), mode)
	return t, f
}

func feedLines(t *testing.T, term *terminal, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		data := []byte(fmt.Sprintf("Line%03d\n", i))
		if err := term.testFeedTerminalInputFromBackend(data, TextReadModeRune); err != nil {
			t.Fatalf("feed: %v", err)
		}
	}
}

// TestScrollbackListener_Ranges verifies monotonic contiguous batches whose
// IDs match Range()/Len() after the run, and that rows are readable from
// inside the callback.
func TestScrollbackListener_Ranges(t *testing.T) {
	term, f := newRecordingTerminal(TextReadModeRune)
	feedLines(t, term, 20)

	batches := f.recorded()
	if len(batches) == 0 {
		t.Fatal("expected scrollback listener callbacks")
	}

	var total int
	var prevScrollID ScrollID
	for i, b := range batches {
		if b.next <= b.first {
			t.Fatalf("batch %d: empty range [%d, %d)", i, b.first, b.next)
		}
		if i > 0 && b.first != prevScrollID {
			t.Fatalf("batch %d: first %d is not contiguous with previous next %d", i, b.first, prevScrollID)
		}
		if got, want := len(b.rows), int(b.next-b.first); got != want {
			t.Fatalf("batch %d: read %d rows through view, want %d", i, got, want)
		}
		for _, row := range b.rows {
			if !strings.HasPrefix(row.PlainTextString(), "Line") {
				t.Errorf("batch %d: row %d text = %q, want Line prefix", i, row.ID, row.PlainTextString())
			}
			if row.ID < b.first || row.ID >= b.next {
				t.Errorf("batch %d: row ID %d outside [%d, %d)", i, row.ID, b.first, b.next)
			}
		}
		prevScrollID = b.next
		total += int(b.next - b.first)
	}

	sb := term.Scrollback()
	base, next := sb.Range()
	if prevScrollID != next {
		t.Errorf("last callback next = %d, Range() next = %d", prevScrollID, next)
	}
	if got, want := total, sb.Len(); got != want {
		t.Errorf("total appended rows = %d, Len() = %d", got, want)
	}
	if total > 0 && base < batches[0].first {
		t.Errorf("Range() base %d < first callback ID %d", base, batches[0].first)
	}
}

// TestScrollbackListener_MultiRowScroll verifies one callback per capture
// batch covers exactly the rows scrolled by CSI S.
func TestScrollbackListener_MultiRowScroll(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprintf("CSI_%dS", n), func(t *testing.T) {
			term, f := newRecordingTerminal(TextReadModeRune)
			feedLines(t, term, 20)

			before := len(f.recorded())
			if err := term.testFeedTerminalInputFromBackend([]byte(fmt.Sprintf("\x1b[%dS", n)), TextReadModeRune); err != nil {
				t.Fatalf("feed CSI S: %v", err)
			}
			batches := f.recorded()
			if got := len(batches) - before; got != 1 {
				t.Fatalf("CSI %dS produced %d callbacks, want 1", n, got)
			}
			b := batches[len(batches)-1]
			if got := int(b.next - b.first); got != n {
				t.Errorf("CSI %dS callback covers %d rows, want %d", n, got, n)
			}
			if got := len(b.rows); got != n {
				t.Errorf("CSI %dS callback exposed %d rows through view, want %d", n, got, n)
			}
		})
	}
}

// TestScrollbackListener_AltScreen verifies alt-screen output never fires.
func TestScrollbackListener_AltScreen(t *testing.T) {
	term, f := newRecordingTerminal(TextReadModeRune)
	if err := term.testFeedTerminalInputFromBackend([]byte("\x1b[?1049h"), TextReadModeRune); err != nil {
		t.Fatalf("enter alt screen: %v", err)
	}
	feedLines(t, term, 40)
	if batches := f.recorded(); len(batches) != 0 {
		t.Fatalf("alt-screen output produced %d callback(s)", len(batches))
	}
	if n := term.Scrollback().Len(); n != 0 {
		t.Fatalf("alt-screen output added %d scrollback row(s)", n)
	}
}

// TestScrollbackListener_Clear verifies Clear does not fire the listener.
func TestScrollbackListener_Clear(t *testing.T) {
	term, f := newRecordingTerminal(TextReadModeRune)
	feedLines(t, term, 20)
	before := len(f.recorded())
	if before == 0 {
		t.Fatal("expected callbacks before Clear")
	}

	term.Scrollback().Clear()
	if got := len(f.recorded()); got != before {
		t.Fatalf("Clear produced %d callback(s), want 0", got-before)
	}
}

// TestScrollbackListener_UnawareFrontend verifies a frontend that does not
// implement ScrollbackListener keeps working.
func TestScrollbackListener_UnawareFrontend(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedLines(t, term, 20)
	if n := term.Scrollback().Len(); n == 0 {
		t.Fatal("expected scrollback rows with plain MockFrontend")
	}
}
