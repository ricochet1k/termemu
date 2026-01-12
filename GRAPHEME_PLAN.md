Grapheme-Aware Rendering Plan
==============================

Goals
-----
- Render wide/combining/ZWJ grapheme clusters correctly.
- Keep output responsive: render immediately, then merge/replace if a cluster continues.
- Avoid maintaining a custom width table; use github.com/rivo/uniseg.
- Preserve current external APIs as much as possible.

Constraints / Observations
--------------------------
- Current pipeline consumes runes via ReadRune and writes rune slices to screen.
- Screen storage and cursor advance are rune-based (1 rune == 1 cell).
- uniseg Step/StepString does not merge clusters across chunks; streaming needs buffering.
- We need to support late-arriving cluster parts without blocking display.

Design Overview
---------------
1) Introduce a grapheme-aware text ingest layer:
   - Convert the incoming byte stream into grapheme clusters using uniseg.Step.
   - Maintain a small pending buffer of bytes for incomplete clusters.
   - Emit clusters optimistically and allow in-place merging when continuation arrives.

2) Represent cells as:
   - Display cell content stored in chars[][] (string/rune slice) per cell, with width.
   - A cluster may span 1 or 2 cells; the first cell stores the cluster text and width,
     the following cell(s) are marked as "continuation".
   - For compatibility, keep the public Line/Text API producing runes for each cell.
     If a cell is a continuation, it renders as a space (or empty) for safety.

3) Update write path:
   - Replace screen.writeRunes with screen.writeGraphemes([]Grapheme).
   - A Grapheme includes: bytes/string, width (1 or 2), and a flag indicating whether
     it should merge into the previous cell (combining/ZWJ/VS/RI continuation).
   - Keep cursor advance based on grapheme width (cells).

4) Handle “late merge”:
   - Track last cell position and last grapheme text/width.
   - If a continuation arrives, recompute the cluster string and width,
     then rewrite the last cell region without advancing the cursor.
   - If width changes (e.g., 1 -> 2), update cursor and clear/shift as needed.

5) Update rendering:
   - StyledLine should consider cell width. For continuation cells, span width should be 1
     but text should not double-render the grapheme.
   - TTYFrontend uses StyledLine; ensure it renders clusters only at the leading cell.

6) Tests (unit + integration-like):
   - Grapheme width tests: combining mark, emoji, ZWJ sequence, regional indicator pair.
   - Continuation behavior: render base glyph, then append combining mark and confirm
     cell content updates in-place.
   - Cursor movement: ensure cursor advance matches grapheme width.
   - Deletion/backspace with wide/combining: cursor and delete behavior stays aligned.
   - TTYFrontend render: ensure output does not drift for powerline/emoji prompts.

Implementation Steps
--------------------
1) Add uniseg dependency to go.mod.
2) Add grapheme parser:
   - A new type that accepts bytes and yields Grapheme objects.
   - It keeps a pending buffer and uses uniseg.Step with state to advance.
3) Update ptyReadLoop to feed bytes to the grapheme parser instead of ReadRune for
   printable sequences. Non-printable control bytes remain byte-driven.
4) Update screen data model:
   - Introduce a per-cell "continuation" marker and store leading cell text/width.
   - Provide helper to render a line to []rune for legacy APIs.
5) Replace writeRunes path with writeGraphemes:
   - Insert grapheme at cursor, handle width 2, clear continuation cells as needed.
   - Add “late merge” rewrite of the last cell when a continuation arrives.
6) Update StyledLine to collapse continuation cells.
7) Add tests for all corner cases; make the tests explicit about cursor positions.
8) Validate with examples/simple + examples/tty using zsh prompt.
