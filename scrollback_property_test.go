package termemu

import (
	"fmt"
	"math/rand"
	"testing"
)

type scrollModelRow struct {
	id      ScrollID
	text    string
	wrapped bool
}

// TestScrollbackRandomOps checks the ring buffer against a simple model under
// random appends, evictions, clears, snapshots, and logical-line queries.
func TestScrollbackRandomOps(t *testing.T) {
	const capacity = 8
	for seed := int64(1); seed <= 10; seed++ {
		term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
		WithScrollback(capacity)(term)
		sb := term.Scrollback()
		rng := rand.New(rand.NewSource(seed))

		var model []scrollModelRow
		nextID := ScrollID(1)
		for step := 0; step < 300; step++ {
			context := fmt.Sprintf("seed %d step %d", seed, step)
			switch rng.Intn(20) {
			case 0:
				sb.Clear()
				model = nil
			case 1:
				if len(model) > 0 {
					// Snapshot and restore must round-trip exactly.
					snap := sb.Snapshot()
					if len(snap.Rows) != len(model) {
						t.Fatalf("%s: snapshot has %d rows, want %d", context, len(snap.Rows), len(model))
					}
					for i, want := range model {
						got := snap.Rows[i]
						if got.ID != want.id || got.PlainTextString() != want.text || got.Wrapped != want.wrapped {
							t.Fatalf("%s: snapshot row %d = {%d %q %v}, want {%d %q %v}",
								context, i, got.ID, got.PlainTextString(), got.Wrapped, want.id, want.text, want.wrapped)
						}
					}
					if err := sb.Restore(snap); err != nil {
						t.Fatalf("%s: restore: %v", context, err)
					}
				}
			default:
				text := fmt.Sprintf("row-%d", nextID)
				wrapped := rng.Intn(2) == 0
				row := scrollModelRow{id: nextID, text: text, wrapped: wrapped}
				term.withLock(func() {
					term.addScrollRow([]Span{{Text: text, Width: len(text)}}, len(text), wrapped)
				})
				if len(model) == capacity {
					model = model[1:]
				}
				model = append(model, row)
				nextID++
			}

			base, next := sb.Range()
			if next != nextID {
				t.Fatalf("%s: next ID = %d, want %d", context, next, nextID)
			}
			wantBase := nextID
			if len(model) > 0 {
				wantBase = model[0].id
			}
			if base != wantBase {
				t.Fatalf("%s: base ID = %d, want %d", context, base, wantBase)
			}
			if got := sb.Len(); got != len(model) {
				t.Fatalf("%s: Len = %d, want %d", context, got, len(model))
			}
			for _, want := range model {
				got, ok := sb.Row(want.id)
				if !ok {
					t.Fatalf("%s: row %d missing", context, want.id)
				}
				if got.PlainTextString() != want.text || got.Wrapped != want.wrapped {
					t.Fatalf("%s: row %d = {%q %v}, want {%q %v}",
						context, want.id, got.PlainTextString(), got.Wrapped, want.text, want.wrapped)
				}
			}

			// LogicalLine and MovePos must stay within the valid ID range and
			// never return zero IDs.
			if len(model) > 0 {
				start := model[rng.Intn(len(model))]
				rows := sb.LogicalLine(start.id)
				if len(rows) == 0 {
					t.Fatalf("%s: LogicalLine(%d) returned no rows", context, start.id)
				}
				last := ScrollID(0)
				for i, row := range rows {
					if row.ID == 0 || row.ID < last {
						t.Fatalf("%s: LogicalLine row %d has bad ID %d (previous %d)", context, i, row.ID, last)
					}
					last = row.ID
				}
				moved := sb.MovePos(ScrollPos{ID: start.id}, rng.Intn(21)-10)
				if moved.ID < base || moved.ID >= next {
					t.Fatalf("%s: MovePos returned %d outside [%d,%d)", context, moved.ID, base, next)
				}
			}
		}
	}
}

// TestScrollbackRestoreOverflowKeepsNewestRows pins the capacity-overflow
// behavior independently of the random model test.
func TestScrollbackRestoreOverflowKeepsNewestRows(t *testing.T) {
	term := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	WithScrollback(3)(term)
	sb := term.Scrollback()

	snap := ScrollbackSnapshot{
		Base: 10,
		Next: 16,
		Rows: []Row{
			{Spans: []Span{{Text: "a", Width: 1}}, Width: 1, ID: 10},
			{Spans: []Span{{Text: "b", Width: 1}}, Width: 1, ID: 11},
			{Spans: []Span{{Text: "c", Width: 1}}, Width: 1, ID: 12},
			{Spans: []Span{{Text: "d", Width: 1}}, Width: 1, ID: 13},
			{Spans: []Span{{Text: "e", Width: 1}}, Width: 1, ID: 14},
			{Spans: []Span{{Text: "f", Width: 1}}, Width: 1, ID: 15},
		},
	}
	if err := sb.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := sb.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	base, next := sb.Range()
	if base != 13 || next != 16 {
		t.Fatalf("Range = %d..%d, want 13..16", base, next)
	}
	for id, want := range map[ScrollID]string{13: "d", 14: "e", 15: "f"} {
		row, ok := sb.Row(id)
		if !ok || row.PlainTextString() != want {
			t.Fatalf("Row(%d) = %q, %v; want %q", id, row.PlainTextString(), ok, want)
		}
	}
}
