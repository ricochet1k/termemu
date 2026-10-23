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
}

const graphemeReadBufferSize = 4096

// GraphemeState is the user-space tokenizer/parser state needed to resume a
// GraphemeReader in another process. Pending holds the bytes already read from
// the source but not yet consumed (data[start:end]).
type GraphemeState struct {
	Pending        []byte
	State          int
	ForceMergeNext bool
	LastWasRI      bool
	Mode           TextReadMode
}

func NewGraphemeReader(src io.Reader) *GraphemeReader {
	return NewGraphemeReaderWithMode(src, TextReadModeGrapheme)
}

func NewGraphemeReaderWithMode(src io.Reader, mode TextReadMode) *GraphemeReader {
	return &GraphemeReader{src: src, state: -1, mode: mode}
}

// NewGraphemeReaderFromState creates a reader that resumes from st, including
// any buffered bytes and tokenizer state. The src is the new underlying stream.
func NewGraphemeReaderFromState(src io.Reader, st GraphemeState) *GraphemeReader {
	r := &GraphemeReader{
		src:            src,
		state:          st.State,
		forceMergeNext: st.ForceMergeNext,
		lastWasRI:      st.LastWasRI,
		mode:           st.Mode,
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
	return b, nil
}

func (r *GraphemeReader) Buffered() int {
	return r.end - r.start
}

// ReadPrintableBytes reads a contiguous run of printable bytes from the stream.
// It stops before control bytes and leaves them for ReadByte. maxWidth limits
// the total cell width returned; pass <=0 for unlimited. merge is true when the
// returned text should be merged into the previous cell.
func (r *GraphemeReader) ReadPrintableBytes(maxWidth int) (string, int, bool, error) {
	if r.Buffered() == 0 {
		err := r.fill()
		if err != nil && r.Buffered() == 0 {
			return "", 0, false, err
		}
	}
	if r.Buffered() == 0 || !isPrintableByte(r.data[r.start]) {
		return "", 0, false, nil
	}

	runStart := r.start
	widthUsed := 0
	mergeRun := false

	for {
		if r.start >= r.end {
			if runStart == r.start {
				err := r.fill()
				runStart = r.start
				if err != nil && r.Buffered() == 0 {
					return "", 0, false, err
				}
				if r.Buffered() == 0 || !isPrintableByte(r.data[r.start]) {
					return "", 0, false, nil
				}
				continue
			}
			break
		}
		if !isPrintableByte(r.data[r.start]) {
			break
		}

		cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok := r.nextTokenInfo(r.data[r.start:r.end])
		if !ok {
			if r.start == runStart {
				before := r.Buffered()
				err := r.fill()
				runStart = r.start
				if r.Buffered() == before {
					if err != nil {
						return "", 0, false, err
					}
					return "", 0, false, io.EOF
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
		return "", 0, false, nil
	}
	out := string(r.data[runStart:r.start])
	return out, widthUsed, mergeRun, nil
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
	cluster, rest, boundaries, newState := uniseg.Step(buf, state)
	if cluster == nil {
		return nil, 0, 0, state, false
	}
	width := boundaries >> uniseg.ShiftWidth
	width = emojiPresentationWidth(cluster, width)
	consumed := len(buf) - len(rest)
	return cluster, consumed, width, newState, true
}

// emojiPresentationWidth adjusts a grapheme cluster's width for emoji
// presentation. uniseg reports keycap sequences such as "1\ufe0f\u20e3" as one
// cell, but terminals render them as two. VS16 and the combining enclosing
// keycap only widen clusters whose computed width is exactly one, so ordinary
// text (including e + combining acute) keeps its normal width.
func emojiPresentationWidth(cluster []byte, width int) int {
	if width != 1 {
		return width
	}
	for len(cluster) > 0 {
		r, size := utf8.DecodeRune(cluster)
		if r == '\ufe0f' || r == '\u20e3' {
			return 2
		}
		cluster = cluster[size:]
	}
	return width
}

// nextRuneIsEmojiPresentation reports whether the next complete rune is an
// emoji presentation selector or combining enclosing keycap.
func nextRuneIsEmojiPresentation(buf []byte) bool {
	if len(buf) == 0 || !utf8.FullRune(buf) {
		return false
	}
	r, _ := utf8.DecodeRune(buf)
	return r == '\ufe0f' || r == '\u20e3'
}

func (r *GraphemeReader) nextTokenInfo(buf []byte) ([]byte, int, int, bool, int, bool, bool, bool) {
	if r.mode == TextReadModeGrapheme {
		return nextGraphemeTokenInfo(buf, r.state, r.forceMergeNext, r.lastWasRI)
	}
	return nextRuneTokenInfo(buf, r.state, r.forceMergeNext, r.lastWasRI)
}

func nextRuneTokenInfo(buf []byte, state int, forceMergeNext bool, lastWasRI bool) ([]byte, int, int, bool, int, bool, bool, bool) {
	cluster, consumed, width, newState, ok := stepRuneCluster(buf, state)
	if !ok {
		return nil, 0, 0, false, state, forceMergeNext, lastWasRI, false
	}
	// Rune mode does not cluster whole sequences, but zero-width runes
	// (combining marks, VS16, ZWJ, modifiers, and the second half of a
	// regional-indicator pair) attach to the previous cell.
	merge := width == 0
	return cluster, consumed, width, merge, newState, forceMergeNext, lastWasRI, true
}

func nextGraphemeTokenInfo(buf []byte, state int, forceMergeNext bool, lastWasRI bool) ([]byte, int, int, bool, int, bool, bool, bool) {
	cluster, consumed, width, newState, ok := stepGraphemeCluster(buf, state)
	if !ok {
		return nil, 0, 0, false, state, forceMergeNext, lastWasRI, false
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

	if merge {
		width = 0
	}
	return cluster, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, true
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
