package termemu

// ScrollID is a monotonically increasing identifier for a physical row
// in the scrollback buffer. IDs are globally unique per terminal and
// stable even as new lines are added or old lines are evicted.
// Use Range to discover the set of valid IDs.
type ScrollID uint64

// ScrollPos identifies a position within the scrollback by its physical row ID.
type ScrollPos struct {
	ID ScrollID
}

// Row represents a single physical row from the scrollback buffer.
type Row struct {
	Spans   []Span
	Width   int
	Wrapped bool
	ID      ScrollID
}

// PlainTextString returns the row's text with styling dropped.
func (r Row) PlainTextString() string {
	return Line{Spans: r.Spans, Width: r.Width}.PlainTextString()
}

// DefaultScrollbackRows is the default capacity of the scrollback ring buffer.
const DefaultScrollbackRows = 1000

// scrollRow is the internal storage for a physical row in the scrollback.
type scrollRow struct {
	id      ScrollID
	spans   []Span
	width   int
	wrapped bool
}

// scrollRowsLen returns the number of populated rows (equivalent to len(s.t.scrollRows)).
// Always use this instead of cap() for ring-buffer index arithmetic to handle
// the pre-full growing-slice case correctly.
func (s *Scrollback) scrollRowsLen() int { return len(s.t.scrollRows) }

// Scrollback provides read access to the terminal's scrollback buffer.
// All methods handle their own locking and are safe to call from any goroutine.
type Scrollback struct {
	t *terminal
}

// Range returns the IDs of the oldest and next-to-be-assigned rows.
// Valid IDs satisfy base <= id < next.
func (s *Scrollback) Range() (base, next ScrollID) {
	s.t.WithLock(func() {
		base = s.scrollBaseIDLocked()
		next = s.t.scrollNextID
	})
	return
}

// Row returns the physical row with the given ID, or false if it has been evicted.
func (s *Scrollback) Row(id ScrollID) (Row, bool) {
	var row Row
	var ok bool
	s.t.WithLock(func() {
		row, ok = s.rowLocked(id)
	})
	return row, ok
}

// LogicalLine returns all physical rows that form the logical line
// containing the given row ID. The rows are reflown at the terminal's
// current width, so the number and sizes of returned rows may differ
// from the captured storage. Returns nil if the ID has been evicted.
func (s *Scrollback) LogicalLine(id ScrollID) []Row {
	var result []Row
	s.t.WithLock(func() {
		result = s.logicalLineLocked(id)
	})
	return result
}

// logicalLineLocked implements LogicalLine with t.Lock held.
func (s *Scrollback) logicalLineLocked(id ScrollID) []Row {
	_, idx, found := s.findRowLocked(id)
	if !found {
		return nil
	}

	n := s.scrollRowsLen()
	if n == 0 {
		return nil
	}

	// Walk backward to find the start of the logical line.
	start := idx
	for {
		prevIdx := (start - 1 + n) % n
		prev := s.t.scrollRows[prevIdx]
		if prev.wrapped {
			start = prevIdx
		} else {
			break
		}
		if start == idx {
			break // wrapped all the way around
		}
	}

	// Walk forward collecting all raw rows in the logical line.
	var rawRows []scrollRow
	for i := start; ; i = (i + 1) % n {
		r := s.t.scrollRows[i]
		if r.id == 0 {
			break
		}
		rawRows = append(rawRows, r)
		if !r.wrapped {
			break
		}
		if (i+1)%n == start {
			break
		}
	}

	if len(rawRows) == 0 {
		return nil
	}

	// Compute total content width for the logical line.
	totalWidth := 0
	for _, r := range rawRows {
		totalWidth += r.width
	}

	// Get the current terminal width for reflow.
	termW, _ := s.t.Size()

	// If terminal width matches the stored rows and there's nothing
	// unusual, return as-is to avoid unnecessary work.
	allSameWidth := true
	for _, r := range rawRows {
		if r.width != termW {
			allSameWidth = false
			break
		}
	}
	if allSameWidth && termW > 0 {
		// No reflow needed.
		result := make([]Row, len(rawRows))
		for i, r := range rawRows {
			result[i] = Row{Spans: r.spans, Width: r.width, Wrapped: i < len(rawRows)-1, ID: r.id}
		}
		return result
	}

	// Reflow: concatenate all spans, re-split at termW.
	allSpans := make([]Span, 0, totalWidth/2)
	for _, r := range rawRows {
		allSpans = append(allSpans, r.spans...)
	}

	return reflowRows(allSpans, totalWidth, termW)
}

// reflowRows splits a contiguous span list into physical rows at targetWidth.
// The last row has Wrapped=false; all others have Wrapped=true.
func reflowRows(spans []Span, totalWidth, targetWidth int) []Row {
	if targetWidth <= 0 || totalWidth <= 0 {
		return nil
	}

	if targetWidth >= totalWidth {
		// Fits in one row.
		return []Row{{
			Spans:   spans,
			Width:   totalWidth,
			Wrapped: false,
		}}
	}

	var rows []Row
	pos := 0
	si := 0 // span index
	so := 0 // cell offset within current span

	// Advance past zero-width leading spans.
	for si < len(spans) && spans[si].Width <= 0 {
		si++
	}

	for pos < totalWidth {
		remaining := targetWidth
		if pos+remaining > totalWidth {
			remaining = totalWidth - pos
		}

		var rowSpans []Span
		need := remaining

		for need > 0 && si < len(spans) {
			sp := spans[si]
			avail := sp.Width - so
			if avail <= 0 {
				si++
				so = 0
				continue
			}
			take := avail
			if take > need {
				take = need
			}

			if so == 0 && take == sp.Width {
				// Can take the whole span.
				rowSpans = append(rowSpans, sp)
			} else {
				// Need a sub-span. splitSpan returns (left, right, _).
				if so == 0 {
					// Split at the start.
					left, _, _ := splitSpan(sp, take, TextReadModeRune)
					rowSpans = append(rowSpans, left)
				} else {
					// Skip 'so' cells, then split at 'take'.
					_, afterSkip, _ := splitSpan(sp, so, TextReadModeRune)
					if take < afterSkip.Width {
						part, _, _ := splitSpan(afterSkip, take, TextReadModeRune)
						rowSpans = append(rowSpans, part)
					} else {
						rowSpans = append(rowSpans, afterSkip)
					}
				}
			}

			need -= take
			so += take
			if so >= sp.Width {
				so = 0
				si++
			}
		}

		wrapped := remaining >= targetWidth && pos+targetWidth < totalWidth
		rows = append(rows, Row{
			Spans:   rowSpans,
			Width:   remaining,
			Wrapped: wrapped,
		})
		pos += remaining
	}
	if len(rows) > 0 {
		rows[len(rows)-1].Wrapped = false
	}
	return rows
}

// MovePos returns a new ScrollPos offset by delta physical rows from pos.
// Negative delta moves toward older rows; positive moves toward newer ones.
// If delta would go out of bounds, the result is clamped to the valid range.
// Note: MovePos operates on the stored physical row count, not on reflown
// rows. Use LogicalLine to discover how a stored row expands at the current width.
func (s *Scrollback) MovePos(pos ScrollPos, delta int) ScrollPos {
	var result ScrollPos
	s.t.WithLock(func() {
		_, idx, found := s.findRowLocked(pos.ID)
		if !found {
			result = pos
			return
		}

		n := s.scrollRowsLen()
		if n == 0 {
			result = pos
			return
		}
		baseIdx := s.t.scrollBaseIdx
		count := len(s.t.scrollRows)
		lastIdx := (baseIdx + count - 1) % n

		target := idx + delta
		if target < baseIdx {
			target = baseIdx
		}
		if target > lastIdx {
			target = lastIdx
		}

		result.ID = s.t.scrollRows[target].id
	})
	return result
}

// --- internal helpers (caller must hold t.Lock) ---

func (s *Scrollback) scrollBaseIDLocked() ScrollID {
	if len(s.t.scrollRows) == 0 {
		return s.t.scrollNextID
	}
	return s.t.scrollRows[s.t.scrollBaseIdx].id
}

func (s *Scrollback) rowLocked(id ScrollID) (Row, bool) {
	_, idx, found := s.findRowLocked(id)
	if !found {
		return Row{}, false
	}
	r := s.t.scrollRows[idx]
	return Row{
		Spans:   r.spans,
		Width:   r.width,
		Wrapped: r.wrapped,
		ID:      r.id,
	}, true
}

func (s *Scrollback) findRowLocked(id ScrollID) (scrollRow, int, bool) {
	if id < s.scrollBaseIDLocked() || id >= s.t.scrollNextID {
		return scrollRow{}, 0, false
	}

	n := s.scrollRowsLen()
	if n == 0 {
		return scrollRow{}, 0, false
	}

	// Estimate position from IDs.
	baseID := s.t.scrollRows[s.t.scrollBaseIdx].id
	offset := int(id - baseID)
	idx := (s.t.scrollBaseIdx + offset) % n

	// Verify.
	row := s.t.scrollRows[idx]
	if row.id == id {
		return row, idx, true
	}

	// Fallback: linear scan (unlikely to be needed).
	count := len(s.t.scrollRows)
	for i := 0; i < count; i++ {
		idx := (s.t.scrollBaseIdx + i) % n
		row := s.t.scrollRows[idx]
		if row.id == id {
			return row, idx, true
		}
	}
	return scrollRow{}, 0, false
}
