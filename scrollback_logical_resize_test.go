package termemu

import (
	"strings"
	"testing"
)

// TestAutoWrapDefaultAndWrapping verifies DECAWM defaults to on and that
// writing past the last column wraps, recording the continuation flag.
func TestAutoWrapDefaultAndWrapping(t *testing.T) {
	s, _ := MakeScreenWithMock()
	if !s.AutoWrap() {
		t.Fatal("new screen must default to auto-wrap on (DECAWM)")
	}

	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(5, 2); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	feedText(t, term, "abcdefg")

	if got := term.Line(0); got != "abcde" {
		t.Errorf("line 0 = %q, want %q", got, "abcde")
	}
	if got := strings.TrimRight(term.Line(1), " "); got != "fg" {
		t.Errorf("line 1 = %q, want %q", got, "fg")
	}
	ss := term.mainScreen.(*spanScreen)
	if ss.wrapped[0] {
		t.Error("row 0 must not be marked as a continuation")
	}
	if !ss.wrapped[1] {
		t.Error("row 1 must be marked as a wrap continuation")
	}
	if got, want := term.CursorPos(), (Pos{X: 2, Y: 1}); got != want {
		t.Errorf("cursor = %+v, want %+v", got, want)
	}
}

// feedRows fills the screen with labels, adding newlines between rows but not
// after the last one, so the rows stay on screen instead of scrolling.
func feedRows(t *testing.T, term *terminal, labels []string) {
	t.Helper()
	for i, label := range labels {
		data := label
		if i < len(labels)-1 {
			data += "\n"
		}
		feedText(t, term, data)
	}
}

func logicalText(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.TrimRight(r.PlainTextString(), " "))
	}
	return b.String()
}

// TestScrollback_LogicalLine_WrappedRows covers a 50-cell line wrapped over
// three physical rows (20/20/10) at a 20-column width.
func TestScrollback_LogicalLine_WrappedRows(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	// Shrinking from the default 14 rows captured blank padding rows.
	sb.Clear()

	feedText(t, term, strings.Repeat("A", 50))
	for range 10 {
		feedText(t, term, "\n")
	}

	base, next := sb.Range()
	if got := int(next - base); got != 3 {
		t.Fatalf("scrollback rows = %d, want 3 (base=%d next=%d)", got, base, next)
	}

	// Stored rows must be 20 wide and flagged false/true/true.
	wantWrapped := []bool{false, true, true}
	for i, want := range wantWrapped {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("Row(%d) missing", base+ScrollID(i))
		}
		if row.Wrapped != want {
			t.Errorf("row %d Wrapped = %v, want %v", i, row.Wrapped, want)
		}
		if row.Width != 20 {
			t.Errorf("row %d Width = %d, want 20", i, row.Width)
		}
	}

	// The logical line must be whole from any of its physical rows.
	for i := range 3 {
		logical := sb.LogicalLine(base + ScrollID(i))
		if len(logical) != 3 {
			t.Fatalf("LogicalLine(base+%d) returned %d rows, want 3", i, len(logical))
		}
		wantWidths := []int{20, 20, 10}
		wantWrapped := []bool{true, true, false}
		for j, row := range logical {
			if row.Width != wantWidths[j] {
				t.Errorf("logical row %d Width = %d, want %d", j, row.Width, wantWidths[j])
			}
			if row.Wrapped != wantWrapped[j] {
				t.Errorf("logical row %d Wrapped = %v, want %v", j, row.Wrapped, wantWrapped[j])
			}
		}
		if got := logicalText(logical); got != strings.Repeat("A", 50) {
			t.Errorf("logical text = %q, want 50 A", got)
		}
	}
}

// TestScrollback_LogicalLine_SingleRow verifies an unwrapped row stays one row.
func TestScrollback_LogicalLine_SingleRow(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	sb.Clear()

	feedText(t, term, "solo")
	for range 10 {
		feedText(t, term, "\n")
	}

	base, next := sb.Range()
	if got := int(next - base); got != 1 {
		t.Fatalf("scrollback rows = %d, want 1", got)
	}
	logical := sb.LogicalLine(base)
	if len(logical) != 1 {
		t.Fatalf("LogicalLine returned %d rows, want 1", len(logical))
	}
	if got := strings.TrimRight(logical[0].PlainTextString(), " "); got != "solo" {
		t.Errorf("logical text = %q, want %q", got, "solo")
	}
	if logical[0].Wrapped {
		t.Error("single-row logical line must not be Wrapped")
	}
}

// TestScrollback_LogicalLine_RingWrap exercises a logical line whose physical
// rows straddle the ring buffer's end and start.
func TestScrollback_LogicalLine_RingWrap(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	term.scrollMaxRows = 5
	term.scrollRows = make([]scrollRow, 0, 5)
	term.scrollBaseIdx = 0

	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	sb.Clear()

	feedRows(t, term, []string{"N0", "N1", "N2", "N3"})
	feedText(t, term, "\n")
	feedText(t, term, strings.Repeat("A", 50))

	// Push exactly the seven content rows into scrollback.
	for range 7 {
		term.mainScreen.scroll(0, 9, -1)
	}

	base, next := sb.Range()
	if got := int(next - base); got != 5 {
		t.Fatalf("scrollback rows = %d, want 5 (base=%d next=%d)", got, base, next)
	}

	// Capacity 5 kept N2, N3, A0, A1, A2. A0..A2 wrap across the ring end.
	a0 := base + 2
	logical := sb.LogicalLine(a0)
	if len(logical) != 3 {
		t.Fatalf("LogicalLine(%d) returned %d rows, want 3", a0, len(logical))
	}
	for i, want := range []int{20, 20, 10} {
		if logical[i].Width != want {
			t.Errorf("logical row %d Width = %d, want %d", i, logical[i].Width, want)
		}
	}
	if got := logicalText(logical); got != strings.Repeat("A", 50) {
		t.Errorf("logical text = %q, want 50 A", got)
	}
}

// TestScrollback_LogicalLine_ReflowIDs verifies that reflown rows report the
// ScrollID of the stored row containing their first cell.
func TestScrollback_LogicalLine_ReflowIDs(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	sb.Clear()

	feedText(t, term, strings.Repeat("A", 50))
	for range 10 {
		feedText(t, term, "\n")
	}

	base, next := sb.Range()
	if got := int(next - base); got != 3 {
		t.Fatalf("scrollback rows = %d, want 3 (base=%d next=%d)", got, base, next)
	}
	stored := make([]ScrollID, 3)
	for i := range stored {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("Row(%d) missing", base+ScrollID(i))
		}
		stored[i] = row.ID
	}

	// Narrower than the captured 20 columns forces a reflow (50 cells become
	// seven 7-cell rows plus one 1-cell row).
	if err := term.Resize(7, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	logical := sb.LogicalLine(base + 1)
	if len(logical) != 8 {
		t.Fatalf("LogicalLine returned %d rows, want 8: %+v", len(logical), logical)
	}
	if got := logicalText(logical); got != strings.Repeat("A", 50) {
		t.Errorf("logical text = %q, want 50 A", got)
	}

	// Cell offsets 0..19 live in stored row 0, 20..39 in stored row 1, and
	// 40..49 in stored row 2. Reflowed rows start at 0,7,14,21,28,35,42,49.
	wantIDs := []ScrollID{
		stored[0], stored[0], stored[0],
		stored[1], stored[1], stored[1],
		stored[2], stored[2],
	}
	prev := ScrollID(0)
	for i, row := range logical {
		if row.ID == 0 {
			t.Errorf("logical row %d has zero ID", i)
		}
		if row.ID < prev {
			t.Errorf("logical row %d ID %d decreased from %d", i, row.ID, prev)
		}
		prev = row.ID
		if row.ID != wantIDs[i] {
			t.Errorf("logical row %d ID = %d, want %d", i, row.ID, wantIDs[i])
		}
		storedRow, ok := sb.Row(row.ID)
		if !ok {
			t.Errorf("Row(%d) for logical row %d not found", row.ID, i)
			continue
		}
		if storedRow.ID != row.ID {
			t.Errorf("Row(%d).ID = %d", row.ID, storedRow.ID)
		}
	}

	// The logical line always starts in the stored row that contains the
	// requested ID's line, and that row must itself be reachable.
	if logical[0].ID != stored[0] {
		t.Errorf("first logical ID = %d, want %d", logical[0].ID, stored[0])
	}
	if _, ok := sb.Row(stored[0]); !ok {
		t.Errorf("Row(%d) missing after reflow", stored[0])
	}
}

// TestResize_ShrinkFeedsScrollback verifies a height shrink pushes the lost
// top rows into scrollback and keeps the newest rows visible.
func TestResize_ShrinkFeedsScrollback(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	sb.Clear()

	labels := []string{"L0", "L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8", "L9"}
	feedRows(t, term, labels)

	regionsBefore := mf.RegionCount()
	if err := term.Resize(20, 5); err != nil {
		t.Fatalf("shrink Resize: %v", err)
	}
	if got := mf.RegionCount() - regionsBefore; got != 1 {
		t.Errorf("shrink emitted %d region changes, want 1 (CRResize)", got)
	}
	if got := mf.Regions[len(mf.Regions)-1].C; got != CRResize {
		t.Errorf("last region change reason = %v, want CRResize", got)
	}

	base, next := sb.Range()
	if got := int(next - base); got != 5 {
		t.Fatalf("scrollback rows after shrink = %d, want 5", got)
	}
	for i, want := range labels[:5] {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("scrollback row %d missing", i)
		}
		if got := strings.TrimRight(row.PlainTextString(), " "); got != want {
			t.Errorf("scrollback row %d = %q, want %q", i, got, want)
		}
	}
	for y, want := range labels[5:] {
		if got := strings.TrimRight(term.Line(y), " "); got != want {
			t.Errorf("screen line %d = %q, want %q", y, got, want)
		}
	}
	if got, want := term.CursorPos(), (Pos{X: 2, Y: 4}); got != want {
		t.Errorf("cursor after shrink = %+v, want %+v", got, want)
	}
}

// TestResize_ShrinkClampsCursor verifies the cursor moves up with the content
// and clamps at the top of the screen.
func TestResize_ShrinkClampsCursor(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	term.Scrollback().Clear()
	feedRows(t, term, []string{"L0", "L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8", "L9"})
	term.mustHandleCommand(t, "[3;1H")

	if err := term.Resize(20, 5); err != nil {
		t.Fatalf("shrink Resize: %v", err)
	}
	if got := term.CursorPos().Y; got != 0 {
		t.Errorf("cursor Y after shrink = %d, want 0", got)
	}
}

// TestResize_AltShrinkNoScrollback verifies resizing while the alt screen is
// active does not add rows to the main screen's scrollback.
func TestResize_AltShrinkNoScrollback(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(20, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	sb := term.Scrollback()
	sb.Clear()

	term.mustHandleCommand(t, "[?1049h")
	feedRows(t, term, []string{"A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9"})

	before := sb.Len()
	if err := term.Resize(20, 5); err != nil {
		t.Fatalf("alt shrink Resize: %v", err)
	}
	if got := sb.Len(); got != before {
		t.Errorf("alt-screen shrink added %d scrollback rows, want 0", got-before)
	}
}
