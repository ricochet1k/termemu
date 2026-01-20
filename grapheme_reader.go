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
	// TextReadModeRune emits one token per rune.
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

func NewGraphemeReader(src io.Reader) *GraphemeReader {
	return NewGraphemeReaderWithMode(src, TextReadModeRune)
}

func NewGraphemeReaderWithMode(src io.Reader, mode TextReadMode) *GraphemeReader {
	return &GraphemeReader{src: src, state: -1, mode: mode}
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
				if err != nil && r.Buffered() == 0 {
					return "", 0, false, err
				}
				if r.Buffered() == 0 || !isPrintableByte(r.data[r.start]) {
					return "", 0, false, nil
				}
				runStart = r.start
				continue
			}
			break
		}
		if !isPrintableByte(r.data[r.start]) {
			break
		}

		_, consumed, width, merge, newState, nextForceMergeNext, nextLastWasRI, ok := r.nextTokenInfo(r.data[r.start:r.end])
		if !ok {
			if r.start == runStart {
				before := r.Buffered()
				err := r.fill()
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
	return b >= 32 && b != 127
}

func stepTextCluster(buf []byte, state int, mode TextReadMode) ([]byte, int, int, int, bool) {
	if mode == TextReadModeGrapheme {
		return stepGraphemeCluster(buf, state)
	}
	return stepRuneCluster(buf, state)
}

func stepRuneCluster(buf []byte, state int) ([]byte, int, int, int, bool) {
	if !utf8.FullRune(buf) {
		return nil, 0, 0, state, false
	}
	r, size := utf8.DecodeRune(buf)
	if size == 0 {
		return nil, 0, 0, state, false
	}
	// Calculate actual display width (e.g., emoji = 2, normal char = 1)
	width := uniseg.StringWidth(string(r))
	if width <= 0 {
		width = 1
	}
	return buf[:size], size, width, state, true
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
	consumed := len(buf) - len(rest)
	return cluster, consumed, width, newState, true
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
	return cluster, consumed, width, false, newState, forceMergeNext, lastWasRI, true
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
