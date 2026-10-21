package termemu

import (
	"errors"
	"strings"
	"testing"
)

func fillScrollbackForSnapshot(t *testing.T, term *terminal, n int) *Scrollback {
	t.Helper()
	fillScreen(term, n, func(i int) string {
		return "row" + strings.Repeat("x", i%7)
	})
	sb := term.Scrollback()
	if sb.Len() == 0 {
		t.Fatal("expected scrollback rows")
	}
	return sb
}

func TestScrollbackSnapshotRoundTrip(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	sb := fillScrollbackForSnapshot(t, term, 20)

	base, next := sb.Range()
	snap := sb.Snapshot()
	if snap.Base != base || snap.Next != next {
		t.Fatalf("snapshot range = %d/%d, want %d/%d", snap.Base, snap.Next, base, next)
	}
	if got, want := len(snap.Rows), int(next-base); got != want {
		t.Fatalf("snapshot rows = %d, want %d", got, want)
	}

	want := make([]Row, len(snap.Rows))
	for i := range snap.Rows {
		row, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("Row(%d) missing before snapshot", base+ScrollID(i))
		}
		want[i] = row
		if snap.Rows[i].ID != row.ID || snap.Rows[i].Width != row.Width || snap.Rows[i].Wrapped != row.Wrapped {
			t.Fatalf("snapshot row %d metadata = %+v, want %+v", i, snap.Rows[i], row)
		}
	}

	sb.Clear()
	if sb.Len() != 0 {
		t.Fatal("Clear did not empty scrollback")
	}
	if err := sb.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotBase, gotNext := sb.Range()
	if gotBase != base || gotNext != next {
		t.Errorf("range after restore = %d/%d, want %d/%d", gotBase, gotNext, base, next)
	}
	if got := sb.Len(); got != len(want) {
		t.Errorf("Len after restore = %d, want %d", got, len(want))
	}
	for i, wantRow := range want {
		got, ok := sb.Row(base + ScrollID(i))
		if !ok {
			t.Fatalf("Row(%d) missing after restore", base+ScrollID(i))
		}
		if !got.Equivalent(wantRow) || got.ID != wantRow.ID || got.Width != wantRow.Width || got.Wrapped != wantRow.Wrapped {
			t.Errorf("restored row %d = %+v, want %+v", i, got, wantRow)
		}
	}
}

func TestScrollbackSnapshotDeepCopy(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	sb := fillScrollbackForSnapshot(t, term, 20)
	base, _ := sb.Range()

	original, ok := sb.Row(base)
	if !ok {
		t.Fatalf("Row(%d) missing", base)
	}
	originalText := original.PlainTextString()

	snap := sb.Snapshot()
	for i := range snap.Rows[0].Spans {
		snap.Rows[0].Spans[i].Text = "MUTATED"
	}

	stored, ok := sb.Row(base)
	if !ok {
		t.Fatalf("Row(%d) missing after mutating snapshot", base)
	}
	if got := stored.PlainTextString(); got != originalText {
		t.Errorf("snapshot mutation leaked into scrollback: got %q, want %q", got, originalText)
	}

	// The snapshot must survive later buffer mutations too.
	sb.Clear()
	if len(snap.Rows) == 0 {
		t.Fatal("snapshot lost rows")
	}
	if len(snap.Rows[0].Spans) == 0 || snap.Rows[0].Spans[0].Text != "MUTATED" {
		t.Errorf("snapshot row changed after buffer Clear: %+v", snap.Rows[0].Spans)
	}
}

func TestScrollbackRestoreCapacityOverflow(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	sb := fillScrollbackForSnapshot(t, term, 20)

	snap := sb.Snapshot()
	if len(snap.Rows) <= 3 {
		t.Fatalf("fixture needs more than 3 rows, got %d", len(snap.Rows))
	}
	term.scrollMaxRows = 3

	if err := sb.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	base, next := sb.Range()
	if got := int(next - base); got != 3 {
		t.Fatalf("restored rows = %d, want 3", got)
	}
	wantBase := snap.Base + ScrollID(len(snap.Rows)-3)
	if base != wantBase {
		t.Errorf("base after overflow restore = %d, want %d", base, wantBase)
	}
	if next != snap.Next {
		t.Errorf("next after overflow restore = %d, want %d", next, snap.Next)
	}
	if got := sb.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	for i, wantRow := range snap.Rows[len(snap.Rows)-3:] {
		got, ok := sb.Row(wantBase + ScrollID(i))
		if !ok {
			t.Fatalf("newest row %d missing", i)
		}
		if !got.Equivalent(wantRow) {
			t.Errorf("restored row %d does not match snapshot", i)
		}
	}
}

func TestScrollbackRestoreMalformed(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	sb := fillScrollbackForSnapshot(t, term, 20)
	valid := sb.Snapshot()
	if len(valid.Rows) < 2 {
		t.Fatalf("fixture needs at least 2 rows, got %d", len(valid.Rows))
	}
	base, _ := sb.Range()
	beforeLen := sb.Len()

	nonContiguous := valid
	nonContiguous.Rows = append([]Row(nil), valid.Rows...)
	nonContiguous.Rows[1].ID = nonContiguous.Rows[0].ID + 2

	cases := []struct {
		name string
		snap ScrollbackSnapshot
	}{
		{"non-contiguous IDs", nonContiguous},
		{"next before base", ScrollbackSnapshot{Base: 5, Next: 4}},
		{"length inconsistent", ScrollbackSnapshot{Rows: []Row{{ID: 5, Width: 1}}, Base: 5, Next: 5}},
		{"zero row ID", ScrollbackSnapshot{Rows: []Row{{ID: 0, Width: 1}}, Base: 0, Next: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sb.Restore(tc.snap)
			if !errors.Is(err, ErrScrollbackSnapshotInvalid) {
				t.Fatalf("Restore error = %v, want ErrScrollbackSnapshotInvalid", err)
			}
			if got := sb.Len(); got != beforeLen {
				t.Errorf("Len changed after failed restore: %d, want %d", got, beforeLen)
			}
			if got, _ := sb.Range(); got != base {
				t.Errorf("base changed after failed restore: %d, want %d", got, base)
			}
		})
	}
}
