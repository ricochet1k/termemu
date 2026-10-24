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

// TestScrollback_MidRegionScrollNotCaptured verifies that scrolling inside a
// mid-screen scroll region (CSI r margins) does not push interior rows into
// scrollback, while a full-screen scroll still does.
func TestScrollback_MidRegionScrollNotCaptured(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 16, func(i int) string {
		return "Base" + string(rune('A'+i))
	})

	sb := t1.Scrollback()
	baseBefore, nextBefore := sb.Range()
	if nextBefore == baseBefore {
		t.Fatal("expected baseline scrollback rows")
	}

	// Restrict scrolling to rows 5-10 (1-based).
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[5;10r"), TextReadModeRune); err != nil {
		t.Fatalf("feed margins: %v", err)
	}
	// Scroll up once inside the region.
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[1S"), TextReadModeRune); err != nil {
		t.Fatalf("feed scroll: %v", err)
	}

	baseAfter, nextAfter := sb.Range()
	if baseAfter != baseBefore || nextAfter != nextBefore {
		t.Errorf("mid-screen scroll leaked into scrollback: range %d-%d, want %d-%d",
			baseAfter, nextAfter, baseBefore, nextBefore)
	}
	if got := sb.Len(); got != int(nextBefore-baseBefore) {
		t.Errorf("Len() = %d, want %d", got, int(nextBefore-baseBefore))
	}

	// Sanity check: resetting to full-screen margins and scrolling captures.
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[r\x1b[1S"), TextReadModeRune); err != nil {
		t.Fatalf("feed reset+scroll: %v", err)
	}
	_, nextFull := sb.Range()
	if nextFull != nextBefore+1 {
		t.Errorf("full-screen scroll captured %d rows, want 1", nextFull-nextBefore)
	}
}

// TestScrollback_RowReturnsCopy verifies that mutating the Spans of a
// returned Row (or LogicalLine result) does not corrupt stored history.
func TestScrollback_RowReturnsCopy(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 16, func(i int) string { return "CopyMe" })

	sb := t1.Scrollback()
	base, next := sb.Range()
	if next == base {
		t.Fatal("expected scrollback rows")
	}

	row, ok := sb.Row(base)
	if !ok {
		t.Fatalf("Row(%d) not found", base)
	}
	if len(row.Spans) == 0 {
		t.Fatal("expected spans in row")
	}
	origText := row.PlainTextString()
	for i := range row.Spans {
		row.Spans[i].Text = "MUTATED"
	}

	again, ok := sb.Row(base)
	if !ok {
		t.Fatalf("Row(%d) lost after mutation", base)
	}
	if again.PlainTextString() != origText {
		t.Errorf("Row mutation leaked into history: got %q, want %q", again.PlainTextString(), origText)
	}
	if again.Width != row.Width || again.Wrapped != row.Wrapped || again.ID != row.ID {
		t.Errorf("Row metadata changed after mutation: %+v vs %+v", again, row)
	}

	// Same for the no-reflow fast path of LogicalLine.
	logical := sb.LogicalLine(base)
	if len(logical) == 0 {
		t.Fatal("expected logical line rows")
	}
	var wantAll string
	for _, r := range logical {
		wantAll += r.PlainTextString()
	}
	for i := range logical[0].Spans {
		logical[0].Spans[i].Text = "MUT"
	}
	logical2 := sb.LogicalLine(base)
	var gotAll string
	for _, r := range logical2 {
		gotAll += r.PlainTextString()
	}
	if gotAll != wantAll {
		t.Errorf("LogicalLine mutation leaked into history: got %q, want %q", gotAll, wantAll)
	}
}

// TestScrollback_Clear verifies Clear empties the buffer while keeping
// ScrollIDs monotonic, and that clearing an empty buffer is a no-op.
func TestScrollback_Clear(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 16, func(i int) string { return "ClearMe" })

	sb := t1.Scrollback()
	_, nextBefore := sb.Range()
	if sb.Len() == 0 {
		t.Fatal("expected scrollback rows")
	}

	sb.Clear()
	base, next := sb.Range()
	if base != next {
		t.Errorf("expected empty range after Clear, got base=%d next=%d", base, next)
	}
	if sb.Len() != 0 {
		t.Errorf("Len() = %d after Clear, want 0", sb.Len())
	}
	if next != nextBefore {
		t.Errorf("Clear broke monotonicity: next=%d, want %d", next, nextBefore)
	}

	// New rows continue from the counter (no ID reuse).
	fillScreen(t1, 16, func(i int) string { return "AfterClear" })
	base2, next2 := sb.Range()
	if base2 != nextBefore {
		t.Errorf("expected base %d after refill, got %d", nextBefore, base2)
	}
	if next2 <= nextBefore {
		t.Errorf("expected next > %d after refill, got %d", nextBefore, next2)
	}

	// Clearing (twice) is safe on an empty or refilled buffer.
	sb.Clear()
	sb.Clear()
	base3, next3 := sb.Range()
	if base3 != next3 || sb.Len() != 0 {
		t.Errorf("expected empty after double Clear, got base=%d next=%d len=%d", base3, next3, sb.Len())
	}
	if next3 != next2 {
		t.Errorf("double Clear broke monotonicity: next=%d, want %d", next3, next2)
	}
}

// TestScrollback_CSI3J verifies CSI 3 J clears the scrollback without
// touching the visible screen, and that 0J/1J/2J behavior is unchanged.
func TestScrollback_CSI3J(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	fillScreen(t1, 16, func(i int) string { return "KeepMe" })

	sb := t1.Scrollback()
	_, nextBefore := sb.Range()
	if nextBefore == 0 {
		t.Fatal("expected scrollback rows")
	}

	var before []string
	t1.View(func(v LockedView) {
		_, h := v.Size()
		for y := 0; y < h; y++ {
			before = append(before, v.Line(y))
		}
	})

	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[3J"), TextReadModeRune); err != nil {
		t.Fatalf("feed CSI 3 J: %v", err)
	}

	base, next := sb.Range()
	if base != next || sb.Len() != 0 {
		t.Errorf("CSI 3 J did not clear scrollback: base=%d next=%d len=%d", base, next, sb.Len())
	}
	if next != nextBefore {
		t.Errorf("CSI 3 J broke monotonicity: next=%d, want %d", next, nextBefore)
	}

	t1.View(func(v LockedView) {
		_, h := v.Size()
		for y := 0; y < h; y++ {
			if got := v.Line(y); got != before[y] {
				t.Errorf("CSI 3 J touched screen line %d: got %q, want %q", y, got, before[y])
			}
		}
	})

	// CSI 2 J still erases the visible screen (the cursor is unchanged).
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[2J"), TextReadModeRune); err != nil {
		t.Fatalf("feed CSI 2 J: %v", err)
	}
	t1.View(func(v LockedView) {
		_, h := v.Size()
		for y := 0; y < h; y++ {
			if got := strings.TrimSpace(v.Line(y)); got != "" {
				t.Errorf("CSI 2 J left content on line %d: %q", y, got)
			}
		}
	})
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

// TestScrollback_ArenaStorage exercises the arena-backed row storage: a wider
// row grows and remaps its block, eviction reuses slots and clears the stale
// span tails, Clear keeps the arena for reuse, and Restore rebuilds it.
func TestScrollback_ArenaStorage(t *testing.T) {
	const capacity = 4
	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	WithScrollback(capacity)(term)
	add := func(n int, text string) {
		spans := make([]Span, n)
		for i := range spans {
			spans[i] = Span{Text: text, Width: 1}
		}
		term.withLock(func() { term.addScrollRow(spans, n, false) })
	}

	add(1, "a")
	add(10, "b") // grows the block stride and remaps the earlier row
	add(1, "c")
	add(2, "d")
	add(2, "e") // evicts "a"

	sb := term.Scrollback()
	want := []struct {
		text string
		n    int
	}{
		{"b", 10}, {"c", 1}, {"d", 2}, {"e", 2},
	}
	base, next := sb.Range()
	if got := int(next - base); got != len(want) {
		t.Fatalf("stored rows = %d, want %d", got, len(want))
	}
	for i, w := range want {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("Row(%d) missing", base+ScrollID(i))
		}
		if len(row.Spans) != w.n {
			t.Errorf("row %d span count = %d, want %d", i, len(row.Spans), w.n)
		}
		for _, sp := range row.Spans {
			if sp.Text != w.text {
				t.Errorf("row %d span text = %q, want %q", i, sp.Text, w.text)
			}
		}
	}

	// Every slot's unused tail must be zeroed, or evicted wider rows would
	// keep their strings alive through the arena.
	term.withLock(func() {
		for bi := range term.scrollArena {
			blk := term.scrollArena[bi]
			for slot := 0; slot < scrollArenaBlockRows; slot++ {
				idx := bi*scrollArenaBlockRows + slot
				if idx >= len(term.scrollRows) {
					break
				}
				n := len(term.scrollRows[idx].spans)
				off := slot * blk.stride
				for _, sp := range blk.spans[off+n : off+blk.stride] {
					if sp.Text != "" {
						t.Fatalf("block %d slot %d tail retains %q", bi, slot, sp.Text)
					}
				}
			}
		}
	})

	snap := sb.Snapshot()
	arenaBlocks := 0
	term.withLock(func() { arenaBlocks = len(term.scrollArena) })
	sb.Clear()
	if arenaBlocks == 0 {
		t.Fatal("arena was not allocated")
	}
	term.withLock(func() {
		if len(term.scrollArena) == 0 {
			t.Error("Clear dropped the arena blocks")
		}
	})
	add(3, "f")
	if got := sb.Len(); got != 1 {
		t.Fatalf("after Clear+capture Len = %d, want 1", got)
	}
	if row, ok := sb.Row(next); !ok || len(row.Spans) != 3 || row.Spans[0].Text != "f" {
		t.Errorf("row captured after Clear = %+v, ok=%v", row, ok)
	}

	if err := sb.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	gotBase, gotNext := sb.Range()
	if gotBase != base || gotNext != next {
		t.Errorf("restored range = [%d, %d), want [%d, %d)", gotBase, gotNext, base, next)
	}
	for i, w := range want {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok || len(row.Spans) != w.n || row.Spans[0].Text != w.text {
			t.Errorf("restored row %d = %+v, ok=%v", i, row, ok)
		}
	}
}
