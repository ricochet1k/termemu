package termemu

import (
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// TextReadMode controls how printable text is tokenized from the input stream.
type TextReadMode int

const (
	// TextReadModeRune emits one token per rune. Zero-width runes (combining
	// marks, VS16, ZWJ, emoji modifiers) merge into the previous cell, but
	// rune mode does not cluster whole ZWJ sequences: a ZWJ family such as
	// "👨\u200d👩\u200d👧\u200d👦" occupies 8 cells (each person is width 2)
	// while the same sequence is 2 cells in grapheme mode.
	TextReadModeRune TextReadMode = iota
	// TextReadModeGrapheme emits one token per grapheme cluster.
	TextReadModeGrapheme
)

// GraphemeToken represents a grapheme cluster or a merge fragment.
// Bytes is a copy of the input bytes to keep tokens safe across buffer refills.
// When Merge is true, the token should be merged into the previous cell.
type GraphemeToken struct {
	Bytes []byte
	Width int
	Merge bool
}

// printableRunMeta records facts the tokenizer establishes while it walks a
// printable run, so the screen does not have to rescan the text afterwards.
type printableRunMeta struct {
	// validUTF8 reports that the emitted bytes are valid UTF-8. Invalid input
	// bytes are tokenized as U+FFFD but emitted raw, so the screen still has
	// to sanitize a run when this is false.
	validUTF8 bool
	// allSpaces reports that the run is exactly one or more ASCII spaces.
	// DEC Special Graphics maps only 0x5F-0x7E, so this stays true after the
	// charset mapping and can be trusted by the blank-span canonicalization.
	allSpaces bool
	// lastRune is the final graphic code point of the run as the tokenizer
	// saw it; invalid bytes count as U+FFFD. REP records it before the
	// charset mapping is applied.
	lastRune rune
}

// GraphemeReader buffers bytes and emits grapheme tokens for printable runs.
type GraphemeReader struct {
	src            io.Reader
	data           []byte
	start          int
	end            int
	state          int
	forceMergeNext bool
	lastWasRI      bool
	mode           TextReadMode
	// standaloneWidth is true when the carried uniseg state comes from a
	// cluster that ended at a read boundary. Its property can make the next
	// cluster's width chunk-dependent, so the first cluster of the new read
	// is measured standalone.
	standaloneWidth bool
}

const graphemeReadBufferSize = 4096

// GraphemeState is the user-space tokenizer/parser state needed to resume a
// GraphemeReader in another process. Pending holds the bytes already read from
// the source but not yet consumed (data[start:end]).
type GraphemeState struct {
	Pending []byte
	State   int
	// Initial is true when State has not been set yet; the reader then
	// starts from the tokenizer's initial state (-1) instead of state 0,
	// which would incorrectly continue a previous grapheme.
	Initial        bool
	ForceMergeNext bool
	LastWasRI      bool
	Mode           TextReadMode
}

func NewGraphemeReader(src io.Reader) *GraphemeReader {
	return NewGraphemeReaderWithMode(src, TextReadModeGrapheme)
}

func NewGraphemeReaderWithMode(src io.Reader, mode TextReadMode) *GraphemeReader {
	return &GraphemeReader{src: src, state: -1, mode: mode, standaloneWidth: true}
}

// NewGraphemeReaderFromState creates a reader that resumes from st, including
// any buffered bytes and tokenizer state. The src is the new underlying stream.
func NewGraphemeReaderFromState(src io.Reader, st GraphemeState) *GraphemeReader {
	state := st.State
	if st.Initial {
		state = -1
	}
	r := &GraphemeReader{
		src:             src,
		state:           state,
		forceMergeNext:  st.ForceMergeNext,
		lastWasRI:       st.LastWasRI,
		mode:            st.Mode,
		standaloneWidth: true,
	}
	if len(st.Pending) > 0 {
		r.data = append([]byte(nil), st.Pending...)
		r.end = len(r.data)
	}
	return r
}

// SnapshotState returns a deep copy of the reader's buffered bytes and
// tokenizer state. The receiver is not modified.
func (r *GraphemeReader) SnapshotState() GraphemeState {
	pending := append([]byte(nil), r.data[r.start:r.end]...)
	return GraphemeState{
		Pending:        pending,
		State:          r.state,
		Initial:        r.state == -1,
		ForceMergeNext: r.forceMergeNext,
		LastWasRI:      r.lastWasRI,
		Mode:           r.mode,
	}
}

func (r *GraphemeReader) ReadByte() (byte, error) {
	for r.Buffered() == 0 {
		err := r.fill()
		if err != nil {
			if r.Buffered() == 0 {
				return 0, err
			}
			break
		}
	}
	if r.Buffered() == 0 {
		return 0, io.EOF
	}
	b := r.data[r.start]
	r.start++
	// Control bytes and escape sequences are not part of the grapheme
	// stream. Reset the tokenizer state so the next printable byte starts a
	// fresh grapheme; otherwise the state can encode boundary context for
	// bytes that were consumed by the escape parser instead of the next
	// text byte.
	if b < 0x20 || b == 0x7f || (b >= 0x80 && b <= 0x9f) {
		r.state = -1
		r.forceMergeNext = false
		r.lastWasRI = false
	}
	return b, nil
}

func (r *GraphemeReader) Buffered() int {
	return r.end - r.start
}

// peekBuffered returns unread buffered bytes without consuming them. The slice
// is only valid until the reader advances or refills; the escape parser treats
// it as read-only.
func (r *GraphemeReader) peekBuffered() []byte {
	return r.data[r.start:r.end]
}

// discardBuffered consumes n unread buffered bytes on behalf of the escape
// parser. A whole escape command is consumed at once, so the tokenizer starts
// fresh afterwards; this matches the state ReadByte leaves after the ESC that
// introduced the command.
func (r *GraphemeReader) discardBuffered(n int) {
	r.start += n
	r.state = -1
	r.forceMergeNext = false
	r.lastWasRI = false
}

// ReadPrintableBytes reads a contiguous run of printable bytes from the stream.
// It stops before control bytes and leaves them for ReadByte. maxWidth limits
// the total cell width returned; pass <=0 for unlimited. merge is true when the
// returned text should be merged into the previous cell.
func (r *GraphemeReader) ReadPrintableBytes(maxWidth int) (string, int, bool, error) {
	text, width, merge, _, err := r.readPrintableRun(maxWidth)
	return text, width, merge, err
}

// readPrintableRun is ReadPrintableBytes plus the facts the tokenizer already
// established about the emitted run. Callers that hand the run to the screen
// can then skip re-validating the UTF-8, rescanning for spaces, and decoding
// the final rune.
func (r *GraphemeReader) readPrintableRun(maxWidth int) (string, int, bool, printableRunMeta, error) {
	return r.readPrintableRunFill(maxWidth, true)
}

// readPrintableRunBuffered is readPrintableRun for bytes that are already
// buffered. It never reads from the source: an incomplete trailing cluster or
// an exhausted buffer returns no data so the caller can decide (outside any
// terminal lock) whether to block for more input.
func (r *GraphemeReader) readPrintableRunBuffered(maxWidth int) (string, int, bool, printableRunMeta, error) {
	return r.readPrintableRunFill(maxWidth, false)
}

func (r *GraphemeReader) readPrintableRunFill(maxWidth int, fill bool) (string, int, bool, printableRunMeta, error) {
	if r.Buffered() == 0 {
		if !fill {
			return "", 0, false, printableRunMeta{}, nil
		}
		err := r.fill()
		if err != nil && r.Buffered() == 0 {
			return "", 0, false, printableRunMeta{}, err
		}
	}
	if r.Buffered() == 0 || !isPrintableByte(r.data[r.start]) {
		return "", 0, false, printableRunMeta{}, nil
	}

	meta := printableRunMeta{validUTF8: true, allSpaces: true}
	runStart := r.start
	widthUsed := 0
	mergeRun := false

	for {
		if r.start >= r.end {
			if runStart == r.start {
				if !fill {
					return "", 0, false, printableRunMeta{}, nil
				}
				err := r.fill()
				runStart = r.start
				if err != nil && r.Buffered() == 0 {
					return "", 0, false, printableRunMeta{}, err
				}
				if r.Buffered() == 0 || !isPrintableByte(r.data[r.start]) {
					return "", 0, false, printableRunMeta{}, nil
				}
				continue
			}
			break
		}
		if !isPrintableByte(r.data[r.start]) {
			break
		}

		// Consume a whole printable-ASCII run minus its final byte in one
		// step. A byte followed by another printable ASCII byte cannot be
		// widened by an emoji-presentation selector, so every byte before the
		// run's end is a plain one-cell token in both modes. The final byte
		// and the width budget boundary still go through the tokenizer.
		// Grapheme mode additionally requires that the run does not continue a
		// ZWJ or regional-indicator cluster.
		if !mergeRun && !r.forceMergeNext && !(r.mode == TextReadModeGrapheme && r.lastWasRI) {
			limit := r.end - r.start
			if maxWidth > 0 && maxWidth-widthUsed < limit {
				limit = maxWidth - widthUsed
			}
			k := 0
			for k < limit {
				b := r.data[r.start+k]
				if b < 0x20 || b >= 0x7f {
					break
				}
				k++
			}
			if k > 1 {
				consume := k - 1
				if meta.allSpaces {
					for i := 0; i < consume; i++ {
						if r.data[r.start+i] != ' ' {
							meta.allSpaces = false
							break
						}
					}
				}
				r.start += consume
				if r.mode == TextReadModeGrapheme {
					r.state = -1
					r.lastWasRI = false
				} else {
					r.state = 0
				}
				widthUsed += consume
				continue
			}
		}

		cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok := r.nextTokenInfo(r.data[r.start:r.end])
		if !ok {
			// The remaining bytes do not form a complete cluster yet. The
			// next emitted cluster may need a standalone width because the
			// state no longer has a complete lookahead.
			r.standaloneWidth = true
			if r.start == runStart {
				if !fill {
					return "", 0, false, printableRunMeta{}, nil
				}
				before := r.Buffered()
				err := r.fill()
				runStart = r.start
				if r.Buffered() == before {
					if err != nil {
						return "", 0, false, printableRunMeta{}, err
					}
					return "", 0, false, printableRunMeta{}, io.EOF
				}
				continue
			}
			break
		}
		if isC1ControlCluster(cluster) {
			// C1 controls (U+0080-U+009F) have no display representation.
			// If text is already buffered, stop here so the emitted run does
			// not contain the skipped bytes; the next call consumes them.
			if widthUsed > 0 {
				break
			}
			// Consume the rune without emitting it; this also keeps the
			// following text in the same contiguous run.
			r.start += consumed
			r.state = newState
			r.forceMergeNext = nextForceMergeNext
			r.lastWasRI = nextLastWasRI
			runStart = r.start
			mergeRun = false
			continue
		}
		if mergeRun && !merge {
			break
		}

		tokenWidth := width
		if merge {
			tokenWidth = 0
		}
		if maxWidth > 0 && widthUsed+tokenWidth > maxWidth && widthUsed > 0 {
			break
		}

		// The tokenizer replaces invalid bytes with U+FFFD in the token, so a
		// cluster whose length differs from the bytes it consumed means the
		// raw run is not valid UTF-8.
		if len(cluster) != consumed {
			meta.validUTF8 = false
		}
		if meta.allSpaces && (merge || consumed != 1 || cluster[0] != ' ') {
			meta.allSpaces = false
		}
		r.start += consumed
		r.state = newState
		r.forceMergeNext = nextForceMergeNext
		r.lastWasRI = nextLastWasRI
		widthUsed += tokenWidth
		if merge && widthUsed == 0 {
			mergeRun = true
		}
	}

	if r.start == runStart {
		return "", 0, false, printableRunMeta{}, nil
	}
	out := string(r.data[runStart:r.start])
	// An invalid final byte decodes as RuneError both here and after
	// sanitization, so the raw output yields the same REP rune as the sanitized
	// text the screen stores.
	meta.lastRune, _ = utf8.DecodeLastRuneInString(out)
	return out, widthUsed, mergeRun, meta, nil
}

// ReadPrintableTokens reads a contiguous run of printable bytes from the stream.
// It stops before control bytes and leaves them for ReadByte. maxWidth limits
// the total cell width returned; pass <=0 for unlimited.
func (r *GraphemeReader) ReadPrintableTokens(maxWidth int) ([]GraphemeToken, error) {
	return r.ReadPrintableTokensInto(maxWidth, nil)
}

// ReadPrintableTokensInto is like ReadPrintableTokens but reuses dst when provided.
// TODO: allow a token sink for direct screen writes without slice allocation.
func (r *GraphemeReader) ReadPrintableTokensInto(maxWidth int, dst []GraphemeToken) ([]GraphemeToken, error) {
	out := dst[:0]
	widthUsed := 0
	for {
		if r.Buffered() == 0 {
			if len(out) > 0 {
				return out, nil
			}
			err := r.fill()
			if err != nil {
				if r.Buffered() == 0 {
					return nil, err
				}
			}
		}
		if r.Buffered() == 0 {
			return out, nil
		}
		if !isPrintableByte(r.data[r.start]) {
			return out, nil
		}

		cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok := r.nextTokenInfo(r.data[r.start:r.end])
		if !ok {
			r.standaloneWidth = true
			if len(out) > 0 {
				return out, nil
			}
			before := r.Buffered()
			err := r.fill()
			if r.Buffered() == before {
				if err != nil {
					return nil, err
				}
				return nil, io.EOF
			}
			continue
		}
		if isC1ControlCluster(cluster) {
			// C1 controls (U+0080-U+009F) are not rendered.
			r.start += consumed
			r.state = newState
			r.forceMergeNext = nextForceMergeNext
			r.lastWasRI = nextLastWasRI
			continue
		}

		tokenWidth := width
		if merge && tokenWidth > 0 {
			tokenWidth = 0
		}
		if maxWidth > 0 && widthUsed+tokenWidth > maxWidth && len(out) > 0 {
			return out, nil
		}
		tokenBytes := append([]byte(nil), cluster...)
		out = append(out, GraphemeToken{Bytes: tokenBytes, Width: width, Merge: merge})
		widthUsed += tokenWidth
		r.start += consumed
		r.state = newState
		r.forceMergeNext = nextForceMergeNext
		r.lastWasRI = nextLastWasRI
	}
}

func (r *GraphemeReader) fill() error {
	if r.data == nil {
		r.data = make([]byte, graphemeReadBufferSize)
	}
	if r.start > 0 {
		if r.start == r.end {
			r.start = 0
			r.end = 0
		} else {
			copy(r.data, r.data[r.start:r.end])
			r.end -= r.start
			r.start = 0
		}
	}
	if r.end == len(r.data) {
		newBuf := make([]byte, len(r.data)*2)
		copy(newBuf, r.data[:r.end])
		r.data = newBuf
	}
	n, err := r.src.Read(r.data[r.end:])
	if n > 0 {
		r.end += n
	}
	return err
}

func isPrintableByte(b byte) bool {
	// Bytes 0x80-0x9F are C1 controls when they appear standalone (that is,
	// not as part of a valid UTF-8 sequence). A valid multi-byte rune is
	// always consumed as a whole by the tokenizer, so excluding these byte
	// values here only drops invalid standalone C1 bytes.
	return b >= 32 && b != 127 && (b < 0x80 || b > 0x9f)
}

// isC1ControlCluster reports whether the first rune of cluster is a C1 control
// rune (U+0080-U+009F), which must not be rendered.
func isC1ControlCluster(cluster []byte) bool {
	if len(cluster) == 0 || cluster[0] < 0x80 {
		return false
	}
	r, _ := utf8.DecodeRune(cluster)
	return r >= 0x80 && r <= 0x9f
}

func stepTextCluster(buf []byte, state int, mode TextReadMode) ([]byte, int, int, int, bool) {
	if mode == TextReadModeGrapheme {
		return stepGraphemeCluster(buf, state)
	}
	return stepRuneCluster(buf, state)
}

// runeStateLastRegionalIndicator marks that stepRuneCluster just emitted the
// first half of a regional-indicator pair. Rune mode does not cluster grapheme
// sequences, but flag pairs still share one cell.
const runeStateLastRegionalIndicator = 1

func stepRuneCluster(buf []byte, state int) ([]byte, int, int, int, bool) {
	if !utf8.FullRune(buf) {
		return nil, 0, 0, state, false
	}
	r, size := utf8.DecodeRune(buf)
	if size == 0 {
		return nil, 0, 0, state, false
	}
	if r == utf8.RuneError && size == 1 {
		// Invalid UTF-8 is rendered as a replacement character rather than
		// stored as raw bytes. It still participates in emoji-presentation
		// widening so a following VS16/keycap/modifier behaves the same as
		// when the replacement arrives in its own read.
		width := 1
		if nextRuneIsEmojiPresentation(buf[size:]) {
			width = 2
		}
		return []byte("\ufffd"), size, width, state, true
	}
	// Calculate the actual display width (e.g., emoji = 2, normal char = 1).
	// Zero-width runes stay zero so the reader can merge them into the
	// previous cell instead of giving them a cell of their own.
	width := uniseg.StringWidth(string(r))
	if width < 0 {
		width = 0
	}

	newState := 0
	if isRegionalIndicator(buf[:size]) {
		if state == runeStateLastRegionalIndicator {
			width = 0
		} else {
			newState = runeStateLastRegionalIndicator
		}
	} else if width == 1 && nextRuneIsEmojiPresentation(buf[size:]) {
		// Emoji presentation selectors (VS16) and the combining enclosing
		// keycap widen the preceding character even though rune mode emits
		// that character on its own.
		width = 2
	}
	return buf[:size], size, width, newState, true
}

func stepGraphemeCluster(buf []byte, state int) ([]byte, int, int, int, bool) {
	if !utf8.FullRune(buf) {
		return nil, 0, 0, state, false
	}
	if r, size := utf8.DecodeRune(buf); r == utf8.RuneError && size == 1 {
		// Treat an invalid byte like rune mode does: one replacement cell,
		// with the tokenizer reset so the next rune starts from a clean
		// state. Feeding the raw invalid byte to uniseg makes clustering
		// depend on read boundaries (e.g. "\xf9" + U+1936).
		width := 1
		consumed := 1
		cluster := []byte("\ufffd")
		if nextRuneIsEmojiPresentation(buf[1:]) {
			// Consume the widening mark with the replacement cell so the
			// result is the same whether the invalid byte and the mark
			// arrive in one read or separately.
			_, size := utf8.DecodeRune(buf[1:])
			cluster = append(cluster, buf[1:1+size]...)
			consumed += size
			width = 2
		}
		return cluster, consumed, width, -1, true
	}
	cluster, rest, boundaries, newState := uniseg.Step(buf, state)
	if cluster == nil {
		return nil, 0, 0, state, false
	}
	width := boundaries >> uniseg.ShiftWidth
	width = emojiPresentationWidth(cluster, width)
	consumed := len(buf) - len(rest)
	return cluster, consumed, width, newState, true
}

func standaloneGraphemeWidth(cluster []byte) int {
	if len(cluster) == 0 {
		return 0
	}
	_, _, boundaries, _ := uniseg.Step(cluster, -1)
	return boundaries >> uniseg.ShiftWidth
}

// emojiPresentationWidth adjusts a grapheme cluster's width for emoji
// presentation. uniseg reports keycap sequences such as "1\ufe0f\u20e3" as one
// cell, but terminals render them as two. VS16, the combining enclosing
// keycap, and emoji skin-tone modifiers only widen clusters whose computed
// width is exactly one, so ordinary text (including e + combining acute) keeps
// its normal width.
func emojiPresentationWidth(cluster []byte, width int) int {
	if width != 1 {
		return width
	}
	for len(cluster) > 0 {
		r, size := utf8.DecodeRune(cluster)
		switch {
		case r == '\ufe0f', r == '\u20e3':
			return 2
		case r >= 0x1f3fb && r <= 0x1f3ff: // emoji skin-tone modifier
			return 2
		}
		cluster = cluster[size:]
	}
	return width
}

// nextRuneIsEmojiPresentation reports whether the next complete rune widens a
// one-cell base: an emoji presentation selector, combining enclosing keycap, or
// skin-tone modifier.
func nextRuneIsEmojiPresentation(buf []byte) bool {
	if len(buf) == 0 || !utf8.FullRune(buf) {
		return false
	}
	r, _ := utf8.DecodeRune(buf)
	return r == '\ufe0f' || r == '\u20e3' || (r >= 0x1f3fb && r <= 0x1f3ff)
}

func (r *GraphemeReader) nextTokenInfo(buf []byte) (cluster []byte, consumed int, width int, merge bool, newState int, nextForceMergeNext bool, nextLastWasRI bool, ok bool) {
	if r.mode == TextReadModeGrapheme {
		cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok = nextGraphemeTokenInfo(buf, r.state, r.forceMergeNext, r.lastWasRI, r.standaloneWidth)
	} else {
		cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok = nextRuneTokenInfo(buf, r.state, r.forceMergeNext, r.lastWasRI)
	}
	if ok {
		// A token that consumes the whole buffer ends at a read boundary;
		// the next read's first cluster width may need a standalone measure.
		r.standaloneWidth = consumed == len(buf)
	}
	return
}

func nextRuneTokenInfo(buf []byte, state int, forceMergeNext bool, lastWasRI bool) ([]byte, int, int, bool, int, bool, bool, bool) {
	// Printable ASCII is always one cell wide and never cluster-carrying.
	// Skipping uniseg here matches the grapheme-mode fast path and avoids a
	// per-rune string allocation in the common case.
	if !forceMergeNext && len(buf) > 0 && buf[0] >= 0x20 && buf[0] < 0x7f {
		width := 1
		if nextRuneIsEmojiPresentation(buf[1:]) {
			width = 2
		}
		return buf[:1], 1, width, false, 0, false, lastWasRI, true
	}
	cluster, consumed, width, newState, ok := stepRuneCluster(buf, state)
	if !ok {
		return nil, 0, 0, false, state, forceMergeNext, lastWasRI, false
	}
	// Rune mode does not cluster whole sequences, but zero-width runes
	// (combining marks, VS16, ZWJ, modifiers, and the second half of a
	// regional-indicator pair) attach to the previous cell.
	merge := width == 0
	nextForceMergeNext := forceMergeNext
	if nextForceMergeNext {
		merge = true
		width = 0
		nextForceMergeNext = false
	}
	if len(cluster) > 0 {
		r, _ := utf8.DecodeRune(cluster)
		switch {
		case r == '\u200d':
			// The joiner itself is zero-width and glues the next rune to
			// the same cell, so a ZWJ family stays 2 cells in rune mode.
			merge = true
			width = 0
			nextForceMergeNext = true
		case r == '\ufe0f' || r == '\u20e3' || (r >= 0x1f3fb && r <= 0x1f3ff):
			merge = true
			width = 0
		}
	}
	return cluster, consumed, width, merge, newState, nextForceMergeNext, lastWasRI, true
}

// extendEmojiPresentation coalesces a one-cell base with a following
// VS16/keycap/skin-tone modifier that uniseg may report as a separate cluster
// (for example "0🏽"). It matches the streaming merge path, which widens the
// previous cell when the mark arrives in its own read.
func extendEmojiPresentation(buf, cluster []byte, consumed, width, newState int) ([]byte, int, int, int) {
	if width != 1 || consumed >= len(buf) || startsWithGraphemeContinuation(cluster) {
		return cluster, consumed, width, newState
	}
	if !nextRuneIsEmojiPresentation(buf[consumed:]) {
		return cluster, consumed, width, newState
	}
	_, size := utf8.DecodeRune(buf[consumed:])
	return buf[:consumed+size], consumed + size, 2, -1
}

func nextGraphemeTokenInfo(buf []byte, state int, forceMergeNext bool, lastWasRI bool, standaloneWidth bool) ([]byte, int, int, bool, int, bool, bool, bool) {
	// Printable ASCII is always its own one-cell cluster. Skipping uniseg
	// here is the common case for terminal output and keeps grapheme mode
	// close to rune mode's throughput. Continuation state (after a ZWJ or a
	// regional indicator) must still go through the full tokenizer.
	if !forceMergeNext && !lastWasRI && len(buf) > 0 && buf[0] >= 0x20 && buf[0] < 0x7f {
		cluster, consumed, width, newState := extendEmojiPresentation(buf, buf[:1], 1, 1, -1)
		return cluster, consumed, width, false, newState, false, false, true
	}
	cluster, consumed, width, newState, ok := stepGraphemeCluster(buf, state)
	if !ok {
		return nil, 0, 0, false, state, forceMergeNext, lastWasRI, false
	}
	cluster, consumed, width, newState = extendEmojiPresentation(buf, cluster, consumed, width, newState)
	if standaloneWidth || lastWasRI {
		// The carried state came from a cluster that ended at the previous
		// read boundary. uniseg's width for this cluster can then depend on
		// the previous rune's property, so measure it on its own.
		width = standaloneGraphemeWidth(cluster)
		width = emojiPresentationWidth(cluster, width)
	}
	merge := false
	nextForceMergeNext := forceMergeNext
	nextLastWasRI := lastWasRI

	if nextForceMergeNext {
		merge = true
		nextForceMergeNext = false
	}

	switch {
	case isCombiningOnly(cluster):
		merge = true
	case isZWJOnly(cluster):
		merge = true
		nextForceMergeNext = true
	case isVariationSelectorOnly(cluster):
		merge = true
	case isRegionalIndicator(cluster):
		if nextLastWasRI {
			merge = true
			nextLastWasRI = false
		} else {
			nextLastWasRI = true
		}
	default:
		nextLastWasRI = false
	}

	// A cluster that ends in ZWJ glues the next cluster to this cell. Track it
	// explicitly so the next chunk merges regardless of what uniseg's carried
	// state reports.
	if endsWithZWJ(cluster) {
		nextForceMergeNext = true
	}

	// A cluster with no advance and any cluster that starts with a
	// continuation rune (ZWJ, variation selector, combining mark, emoji
	// modifier, keycap, tag) is part of the preceding grapheme, which can
	// happen when a cluster is split across reads or a pause/resume. Merge
	// it into the previous cell instead of giving it a cell of its own.
	if width == 0 || startsWithGraphemeContinuation(cluster) {
		merge = true
	}
	if merge {
		width = 0
	}
	return cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, true
}

func endsWithZWJ(cluster []byte) bool {
	for len(cluster) > 0 {
		r, size := utf8.DecodeRune(cluster)
		cluster = cluster[size:]
		if len(cluster) == 0 {
			return r == '\u200d'
		}
	}
	return false
}

// startsWithGraphemeContinuation reports whether the first rune of cluster is
// a code point that can extend the preceding grapheme.
func startsWithGraphemeContinuation(cluster []byte) bool {
	if len(cluster) == 0 {
		return false
	}
	if cluster[0] < 0x80 {
		// ASCII is never a grapheme continuation; skip the unicode tables.
		return false
	}
	r, _ := utf8.DecodeRune(cluster)
	switch {
	case r == '\u200d': // ZWJ
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // VS1-16
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff: // emoji modifiers
		return true
	case r == 0x20e3: // combining enclosing keycap
		return true
	case r >= 0xe0020 && r <= 0xe007f: // emoji tag sequences
		return true
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
		// Non-spacing/enclosing marks attach to the preceding cell. Spacing
		// combining marks (Mc) can occupy their own cell width, so they are
		// not force-merged; uniseg still clusters them when they arrive in
		// the same read, and Line.Equivalent compares both forms by cells.
		return true
	}
	return false
}

func isCombiningOnly(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Me, r) {
			return false
		}
		b = b[size:]
	}
	return true
}

func isZWJOnly(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r != '\u200d' {
			return false
		}
		b = b[size:]
	}
	return true
}

func isVariationSelectorOnly(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		switch {
		case r >= 0xfe00 && r <= 0xfe0f:
		case r >= 0xe0100 && r <= 0xe01ef:
		default:
			return false
		}
		b = b[size:]
	}
	return true
}

func isRegionalIndicator(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r < 0x1f1e6 || r > 0x1f1ff {
			return false
		}
		b = b[size:]
	}
	return true
}
