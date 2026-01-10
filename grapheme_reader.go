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
// When Merge is true, the token should be merged into the previous cell.
type GraphemeToken struct {
	Text  string
	Width int
	Merge bool
}

// GraphemeReader buffers bytes and emits grapheme tokens for printable runs.
type GraphemeReader struct {
	src   io.Reader
	data  []byte
	start int
	end   int
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

// ReadPrintableTokens reads a contiguous run of printable bytes from the stream.
// It stops before control bytes and leaves them for ReadByte.
func (r *GraphemeReader) ReadPrintableTokens() ([]GraphemeToken, error) {
	var out []GraphemeToken
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

		segment := r.data[r.start:r.end]
		token, consumed, newState, ok := r.nextToken(segment)
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

		out = append(out, token)
		r.start += consumed
		r.state = newState
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

func (r *GraphemeReader) nextToken(buf []byte) (GraphemeToken, int, int, bool) {
	if r.mode == TextReadModeGrapheme {
		return nextGraphemeToken(buf, r.state, &r.forceMergeNext, &r.lastWasRI)
	}
	return nextRuneToken(buf, r.state)
}

func nextRuneToken(buf []byte, state int) (GraphemeToken, int, int, bool) {
	if !utf8.FullRune(buf) {
		return GraphemeToken{}, 0, state, false
	}
	ru, size := utf8.DecodeRune(buf)
	if size == 0 {
		return GraphemeToken{}, 0, state, false
	}
	return GraphemeToken{Text: string(ru), Width: 1, Merge: false}, size, state, true
}

func nextGraphemeToken(buf []byte, state int, forceMergeNext *bool, lastWasRI *bool) (GraphemeToken, int, int, bool) {
	if !utf8.FullRune(buf) {
		return GraphemeToken{}, 0, state, false
	}
	cluster, rest, boundaries, newState := uniseg.Step(buf, state)
	if cluster == nil {
		return GraphemeToken{}, 0, state, false
	}

	text := string(cluster)
	width := boundaries >> uniseg.ShiftWidth
	merge := false

	if *forceMergeNext {
		merge = true
		*forceMergeNext = false
	}

	switch {
	case isCombiningOnly(text):
		merge = true
	case isZWJOnly(text):
		merge = true
		*forceMergeNext = true
	case isVariationSelectorOnly(text):
		merge = true
	case isRegionalIndicator(text):
		if *lastWasRI {
			merge = true
			*lastWasRI = false
		} else {
			*lastWasRI = true
		}
	default:
		*lastWasRI = false
	}

	consumed := len(buf) - len(rest)
	return GraphemeToken{Text: text, Width: width, Merge: merge}, consumed, newState, true
}

func isCombiningOnly(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Me, r) {
			return false
		}
	}
	return s != ""
}

func isZWJOnly(s string) bool {
	for _, r := range s {
		if r != '\u200d' {
			return false
		}
	}
	return s != ""
}

func isVariationSelectorOnly(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0xfe00 && r <= 0xfe0f:
		case r >= 0xe0100 && r <= 0xe01ef:
		default:
			return false
		}
	}
	return s != ""
}

func isRegionalIndicator(s string) bool {
	for _, r := range s {
		if r < 0x1f1e6 || r > 0x1f1ff {
			return false
		}
	}
	return s != ""
}
