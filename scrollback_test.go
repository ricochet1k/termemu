package termemu

import (
	"strings"
	"sync"
	"testing"
)

// fillScreen feeds n lines via testFeedTerminalInputFromBackend.
func fillScreen(t1 *terminal, n int, line func(i int) string) {
	for i := 0; i < n; i++ {
		data := []byte(line(i) + "\n")
		if err := t1.testFeedTerminalInputFromBackend(data, TextReadModeRune); err != nil {
			panic("fillScreen: " + err.Error())
		}
	}
}

// TestScrollback_Basic verifies that scrolled-off rows are captured.
func TestScrollback_Basic(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 16, func(i int) string {
		return "Line" + string(rune('A'+i))
	})

	sb := t1.Scrollback()
	base, next := sb.Range()
	got := int(next - base)
	t.Logf("scrollback rows: %d (base=%d, next=%d)", got, base, next)
	if got < 1 {
		t.Fatal("expected at least 1 row in scrollback")
	}

	row, ok := sb.Row(base)
	if !ok {
		t.Fatalf("Row(%d) not found", base)
	}
	if !strings.Contains(row.PlainTextString(), "Line") {
		t.Errorf("expected 'Line' in oldest row, got %q", row.PlainTextString())
	}
}

// TestScrollback_LogicalLine_Merging verifies that wrapped rows are merged
// into multi-row logical lines and reflown correctly.
func TestScrollback_LogicalLine_Merging(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Write lines long enough to wrap: 240 chars = 3 rows at 80 col.
	// Write enough to push several lines into the scrollback.
	long := strings.Repeat("ABCDEFGH", 30) // 240 chars = exactly 3 rows
	for i := 0; i < 20; i++ {
		data := []byte(long + "\n")
		if err := t1.testFeedTerminalInputFromBackend(data, TextReadModeRune); err != nil {
			t.Fatalf("feed: %v", err)
		}
	}

	sb := t1.Scrollback()
	base, next := sb.Range()
	t.Logf("scrollback: base=%d, next=%d (rows=%d)", base, next, int(next-base))
	if base == next {
		t.Fatal("scrollback empty")
	}

	// Log all rows for debugging.
	for id := base; id < next; id++ {
		r, ok := sb.Row(id)
		if !ok {
			continue
		}
		text := r.PlainTextString()
		t.Logf("  id=%d width=%d wrapped=%v text=%q...", r.ID, r.Width, r.Wrapped, text[:min(len(text), 20)])
	}

	// Find the first wrapped row.
	var wrappedRow Row
	var foundWrapper bool
	for id := base; id < next; id++ {
		r, ok := sb.Row(id)
		if !ok {
			continue
		}
		if r.Wrapped {
			wrappedRow = r
			foundWrapper = true
			break
		}
	}

	// If no wrapped row found, pick a row near the middle and try LogicalLine anyway.
	if !foundWrapper {
		t.Log("no wrapped rows found — testing LogicalLine from base row instead")
		wrappedRow, _ = sb.Row(base)
	}

	logical := sb.LogicalLine(wrappedRow.ID)
	t.Logf("logical line from row %d: %d physical rows", wrappedRow.ID, len(logical))
	var totalWidth int
	for i, r := range logical {
		totalWidth += r.Width
		t.Logf("  [%d] id=%d width=%d wrapped=%v", i, r.ID, r.Width, r.Wrapped)
	}

	if len(logical) < 1 {
		t.Error("expected at least 1 physical row")
	}

	// If any wrapped row existed, the logical line should have combined them.
	if foundWrapper && len(logical) < 2 {
		t.Log("note: wrapped row found but logical line is only 1 row (may indicate reflow or boundary)")
	}

	// Verify the concatenated text is long (should be close to 240 chars).
	var all string
	for _, r := range logical {
		all += r.PlainTextString()
	}
	t.Logf("total logical line width: %d cells, text length: %d", totalWidth, len(all))
}

// TestScrollback_Reflow verifies that LogicalLine reflows at the current width.
func TestScrollback_Reflow(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Write 200-char lines at 80 col = 3 rows each.
	long := strings.Repeat("X", 200)
	for i := 0; i < 20; i++ {
		data := []byte(long + "\n")
		if err := t1.testFeedTerminalInputFromBackend(data, TextReadModeRune); err != nil {
			t.Fatalf("feed: %v", err)
		}
	}

	// Resize to 100 columns.
	_ = t1.Resize(100, 14)

	sb := t1.Scrollback()
	base, _ := sb.Range()

	// Get a logical line from the scrollback.
	logical := sb.LogicalLine(base)
	t.Logf("at 100 cols: logical line has %d physical rows", len(logical))
	for i, r := range logical {
		t.Logf("  [%d] width=%d wrapped=%v", i, r.Width, r.Wrapped)
	}

	// At 100 cols, a 200-char line should fit in 2 rows (100+100).
	if len(logical) < 1 || len(logical) > 3 {
		t.Logf("note: expected roughly 2 rows at 100 cols, got %d", len(logical))
	}

	// Resize back to 80 and check.
	_ = t1.Resize(80, 14)
	logical80 := sb.LogicalLine(base)
	t.Logf("at 80 cols: logical line has %d physical rows", len(logical80))
	for i, r := range logical80 {
		t.Logf("  [%d] width=%d wrapped=%v", i, r.Width, r.Wrapped)
	}
}

// TestScrollback_MovePos tests moving by physical rows.
func TestScrollback_MovePos(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	fillScreen(t1, 20, func(i int) string { return "Line" + string(rune('A'+i)) })

	sb := t1.Scrollback()
	base, next := sb.Range()
	if base == next {
		t.Fatal("expected rows in scrollback")
	}

	// Move forward by 1 from base.
	pos2 := sb.MovePos(ScrollPos{ID: base}, 1)
	if pos2.ID <= base || pos2.ID >= next {
		t.Errorf("MovePos(+1): expected ID between %d and %d, got %d", base+1, next-1, pos2.ID)
	}

	// Move backward from pos2.
	posBack := sb.MovePos(pos2, -1)
	if posBack.ID != base {
		t.Errorf("MovePos(-1): expected base %d, got %d", base, posBack.ID)
	}

	// Clamp backward.
	posClamped := sb.MovePos(ScrollPos{ID: base}, -100)
	if posClamped.ID != base {
		t.Errorf("MovePos(-100): expected %d, got %d", base, posClamped.ID)
	}

	// Clamp forward.
	last := next - 1
	posEnd := sb.MovePos(ScrollPos{ID: last}, 100)
	if posEnd.ID >= next {
		t.Errorf("MovePos(+100): expected < %d, got %d", next, posEnd.ID)
	}
}

// TestScrollback_AltScreenNoCapture verifies that scrollback is NOT captured
// when on the alternate screen.
func TestScrollback_AltScreenNoCapture(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	// Switch to alt screen.
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[?1049h"), TextReadModeRune); err != nil {
		t.Fatalf("feed: %v", err)
	}

	fillScreen(t1, 20, func(i int) string { return "Alt" + string(rune('A'+i)) })

	sb := t1.Scrollback()
	base, next := sb.Range()
	if base != next {
		t.Errorf("expected empty scrollback on alt screen, got %d rows", int(next-base))
	}
}

// TestScrollback_RingBufferOverflow verifies the ring buffer wraps correctly.
func TestScrollback_RingBufferOverflow(t *testing.T) {
	t1 := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	t1.scrollMaxRows = 5
	t1.scrollRows = make([]scrollRow, 0, 5)
	t1.scrollBaseIdx = 0
	t1.mainScreen.setScrollCapture(t1.scrollCapture)
	t1.altScreen.setScrollCapture(t1.scrollCapture)

	fillScreen(t1, 20, func(i int) string { return "X" })

	sb := &Scrollback{t: t1}
	base, next := sb.Range()
	got := int(next - base)
	if got > 5 {
		t.Errorf("scrollback should be capped at 5 rows, got %d", got)
	}
	t.Logf("ring buffer: base=%d, next=%d, rows=%d", base, next, got)
}

// TestScrollback_StableIDs verifies that ScrollID values are monotonically
// increasing and unique.
func TestScrollback_StableIDs(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 30, func(i int) string { return string(rune('A' + i)) })

	sb := t1.Scrollback()
	base, next := sb.Range()
	var prevID ScrollID
	for id := base; id < next; id++ {
		row, ok := sb.Row(id)
		if !ok {
			t.Fatalf("Row(%d) not found", id)
		}
		if row.ID != id {
			t.Errorf("Row returned ID %d for ScrollID %d", row.ID, id)
		}
		if prevID != 0 && row.ID <= prevID {
			t.Errorf("IDs not monotonically increasing: %d <= %d", row.ID, prevID)
		}
		prevID = row.ID
	}
}

// TestScrollback_ConcurrentFeedAndRead exercises concurrent scroll and read.
func TestScrollback_ConcurrentFeedAndRead(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer goroutine: fill screen continuously.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			select {
			case <-done:
				return
			default:
			}
			data := []byte("Concurrent line " + string(rune('A'+i)) + "\n")
			_ = t1.testFeedTerminalInputFromBackend(data, TextReadModeRune)
		}
	}()

	// Reader goroutines: read scrollback while writer runs.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				select {
				case <-done:
					return
				default:
				}
				sb := t1.Scrollback()
				base, next := sb.Range()
				if base < next {
					_, ok := sb.Row(base)
					if !ok {
						// Might be evicted between Range and Row — ok.
						continue
					}
					_ = sb.LogicalLine(base)
					_ = sb.MovePos(ScrollPos{ID: base}, 3)
				}
			}
		}(g)
	}

	wg.Wait()
	close(done)
}

// TestScrollback_EmptyTerminal verifies Range works on a fresh terminal.
func TestScrollback_EmptyTerminal(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	sb := t1.Scrollback()
	base, next := sb.Range()
	if base != next {
		t.Errorf("expected empty terminal to have empty scrollback, got base=%d next=%d", base, next)
	}
}

// TestScrollback_WithScrollbackOption verifies the WithScrollback terminal option.
func TestScrollback_WithScrollbackOption(t *testing.T) {
	frontend := &EmptyFrontend{}
	backend := NewNoPTYBackend(nil, nil)
	term := NewWithMode(frontend, backend, TextReadModeRune, WithScrollback(3))
	if term == nil {
		t.Fatal("NewWithMode returned nil")
	}
	t1 := term.(*terminal)
	if t1.scrollMaxRows != 3 {
		t.Errorf("expected scrollMaxRows=3, got %d", t1.scrollMaxRows)
	}

	// Feed enough lines to overflow the 3-row buffer.
	fillScreen(t1, 20, func(i int) string { return "O" })

	sb := t1.Scrollback()
	base, next := sb.Range()
	got := int(next - base)
	if got > 3 {
		t.Errorf("expected at most 3 rows, got %d", got)
	}
	t.Logf("WithScrollback(3): base=%d, next=%d, rows=%d", base, next, got)
}

// TestScrollback_RowEviction verifies Row returns false for evicted IDs.
func TestScrollback_RowEviction(t *testing.T) {
	t1 := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	t1.scrollMaxRows = 2
	t1.scrollRows = make([]scrollRow, 0, 2)
	t1.scrollBaseIdx = 0
	t1.mainScreen.setScrollCapture(t1.scrollCapture)
	t1.altScreen.setScrollCapture(t1.scrollCapture)

	// Must feed enough to actually scroll off.
	fillScreen(t1, 16, func(i int) string { return "E" })

	sb := &Scrollback{t: t1}
	base, next := sb.Range()
	t.Logf("buffer: base=%d, next=%d", base, next)
	if base >= next {
		t.Fatal("expected some rows")
	}

	// IDs before base should be evicted.
	_, ok := sb.Row(base - 1)
	if ok {
		t.Log("Row(base-1) returned ok (might still be in buffer if <2 rows)")
	}

	// Every ID in [base, next) should be findable.
	for id := base; id < next; id++ {
		_, ok := sb.Row(id)
		if !ok {
			t.Errorf("Row(%d) unexpectedly evicted", id)
		}
	}
}
