package termemu

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

type panicReader struct {
	data  []byte
	reads int
}

func (r *panicReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		panic("unexpected extra read")
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type appendReader struct {
	chunks [][]byte
	idx    int
	off    int
}

func (r *appendReader) Append(b []byte) {
	r.chunks = append(r.chunks, b)
}

func (r *appendReader) Read(p []byte) (int, error) {
	for r.idx < len(r.chunks) && r.off >= len(r.chunks[r.idx]) {
		r.idx++
		r.off = 0
	}
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.idx][r.off:]
	n := copy(p, chunk)
	r.off += n
	return n, nil
}

func tokenString(tok GraphemeToken) string {
	return string(tok.Bytes)
}

func TestGraphemeReader_CombiningMarkMerges(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("e"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected base token, got %#v", out)
	}

	r.Append([]byte("\u0301"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected combining mark to merge, got %#v", out)
	}
}

func TestGraphemeReader_ImmediateSingleToken(t *testing.T) {
	r := &panicReader{data: []byte("a")}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || tokenString(out[0]) != "a" || out[0].Merge {
		t.Fatalf("expected single base token, got %#v", out)
	}
}

func TestGraphemeReader_PartialTrailingDoesNotBlock(t *testing.T) {
	r := &appendReader{}
	r.Append([]byte{'a', 0xe2, 0x82})
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || tokenString(out[0]) != "a" {
		t.Fatalf("expected leading token, got %#v", out)
	}

	out, err = gr.ReadPrintableTokens(0)
	if err != io.EOF {
		t.Fatalf("expected EOF for trailing partial, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no tokens for trailing partial, got %#v", out)
	}
}

func TestGraphemeReader_ZWJForcesMergeNext(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("\U0001f468"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected base token, got %#v", out)
	}

	r.Append([]byte("\u200d"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected ZWJ to merge, got %#v", out)
	}

	r.Append([]byte("\U0001f469"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected post-ZWJ token to merge, got %#v", out)
	}
}

func TestGraphemeReader_RegionalIndicatorPairs(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("\U0001f1fa"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected first RI to be base, got %#v", out)
	}

	r.Append([]byte("\U0001f1f8"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected second RI to merge, got %#v", out)
	}
}

func TestGraphemeReader_VariationSelectorMerges(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("☂"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected base token, got %#v", out)
	}

	r.Append([]byte("\ufe0f"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected variation selector to merge, got %#v", out)
	}
}

func TestGraphemeReader_PartialUTF8Buffers(t *testing.T) {
	r := &appendReader{}
	r.Append([]byte{0xe2, 0x82})
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	out, err := gr.ReadPrintableTokens(0)
	if err != io.EOF {
		t.Fatalf("expected EOF for partial input, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no tokens for partial rune, got %#v", out)
	}

	r.Append([]byte{0xac})
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || tokenString(out[0]) != "€" {
		t.Fatalf("expected euro sign token, got %#v", out)
	}
}

func TestGraphemeReader_RegionalIndicatorTripletResets(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("\U0001f1fa"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected first RI to be base, got %#v", out)
	}

	r.Append([]byte("\U0001f1f8"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || !out[0].Merge {
		t.Fatalf("expected second RI to merge, got %#v", out)
	}

	r.Append([]byte("\U0001f1e8"))
	out, err = gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Merge {
		t.Fatalf("expected third RI to start new pair, got %#v", out)
	}
}

func TestGraphemeReader_WidthForEmoji(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("😀"))
	out, err := gr.ReadPrintableTokens(0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableTokens error: %v", err)
	}
	if len(out) != 1 || out[0].Width != 2 {
		t.Fatalf("expected emoji width 2, got %#v", out)
	}
}

func TestGraphemeReader_ReadPrintableBytesMaxWidth(t *testing.T) {
	r := &appendReader{}
	gr := NewGraphemeReaderWithMode(r, TextReadModeGrapheme)

	r.Append([]byte("a\u0301b"))
	out, width, merge, err := gr.ReadPrintableBytes(1)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableBytes error: %v", err)
	}
	if out != "a\u0301" || width != 1 || merge {
		t.Fatalf("expected merged width 1, got %q width %d merge %v", out, width, merge)
	}

	out, width, merge, err = gr.ReadPrintableBytes(1)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadPrintableBytes error: %v", err)
	}
	if out != "b" || width != 1 || merge {
		t.Fatalf("expected trailing token, got %q width %d merge %v", out, width, merge)
	}
}

func benchmarkGraphemeRead(b *testing.B, text string) {
	data := []byte(text)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gr := NewGraphemeReaderWithMode(bytes.NewReader(data), TextReadModeGrapheme)
		for {
			text, _, _, err := gr.ReadPrintableBytes(0)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			if text == "" {
				if _, err := gr.ReadByte(); err == io.EOF {
					break
				} else if err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

func BenchmarkGraphemeReadASCII(b *testing.B) {
	benchmarkGraphemeRead(b, strings.Repeat("the quick brown fox jumps over the lazy dog 0123456789\n", 400))
}

func BenchmarkGraphemeReadEmoji(b *testing.B) {
	benchmarkGraphemeRead(b, strings.Repeat("👨\u200d👩\u200d👧\u200d👦 e\u0301 1\ufe0f\u20e3 🇺🇸👍🏽 ", 200))
}

// TestReadPrintableRunInvalidMetadata feeds raw invalid bytes through the
// reader. The metadata must report invalid UTF-8 (so the screen sanitizes) and
// still supply the run's final rune; the rendered screen must keep matching the
// sanitized text.
func TestReadPrintableRunInvalidMetadata(t *testing.T) {
	data := []byte{'a', 0xff, 'b', 0xc0, 0xfe, 'c'}
	gr := NewGraphemeReaderWithMode(bytes.NewReader(data), TextReadModeGrapheme)
	text, width, merge, meta, err := gr.readPrintableRun(0)
	if err != nil {
		t.Fatalf("readPrintableRun: %v", err)
	}
	if merge {
		t.Fatalf("unexpected merge run")
	}
	if meta.validUTF8 {
		t.Fatalf("metadata reports valid UTF-8 for %q", text)
	}
	if meta.allSpaces {
		t.Fatalf("metadata reports an all-space run for %q", text)
	}
	if meta.lastRune != 'c' {
		t.Fatalf("lastRune = %q, want 'c'", meta.lastRune)
	}
	want := sanitizeUTF8(text)
	if want == text {
		t.Fatalf("test data was already valid UTF-8: %q", text)
	}
	if got := width; got != utf8.RuneCountInString(want) {
		t.Fatalf("width = %d, want %d for %q", got, utf8.RuneCountInString(want), want)
	}

	backend := NewNoPTYBackend(&byteAtATimeReader{data: data}, io.Discard)
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
	if err := term.Resize(40, 3); err != nil {
		t.Fatalf("resize: %v", err)
	}
	defer func() { _ = term.Close() }()
	<-term.Done()
	if got := strings.TrimRight(term.Line(0), " "); got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if got := term.CursorPos().X; got != width {
		t.Fatalf("cursor x = %d, want %d", got, width)
	}
}

// TestPrintableRunMetadataMatchesText checks that the metadata the reader
// reports for a run agrees with what a rescan of the emitted text would find.
// It covers every one- and two-byte input plus malformed multi-byte sequences
// and a deterministic random corpus.
func TestPrintableRunMetadataMatchesText(t *testing.T) {
	checkData := func(mode TextReadMode, data []byte) {
		t.Helper()
		gr := NewGraphemeReaderWithMode(bytes.NewReader(data), mode)
		for {
			text, _, _, meta, err := gr.readPrintableRun(0)
			if err != nil {
				break
			}
			if text == "" {
				if _, err := gr.ReadByte(); err != nil {
					break
				}
				continue
			}
			want := analyzePrintableRun(text)
			if meta.validUTF8 != want.validUTF8 || meta.allSpaces != want.allSpaces || meta.lastRune != want.lastRune {
				t.Fatalf("mode %d data %q: metadata = %+v, rescan = %+v", mode, data, meta, want)
			}
		}
	}
	corpus := [][]byte{
		[]byte("\u00e9\xff"), []byte("\xe2\x82\xac\xff"),
		[]byte("a\xf0\x9f"), []byte("\xf0\x9f\x98\x80\xff"),
		[]byte("\xff\ufe0f"), []byte("a\xc0\x80b"),
	}
	seed := uint32(1)
	for i := 0; i < 2000; i++ {
		seed = seed*1664525 + 1013904223
		data := make([]byte, len(corpus[0])+int(seed>>28))
		for j := range data {
			seed = seed*1664525 + 1013904223
			data[j] = byte(seed >> 24)
		}
		corpus = append(corpus, data)
	}
	for _, mode := range []TextReadMode{TextReadModeRune, TextReadModeGrapheme} {
		for b := 0; b < 256; b++ {
			checkData(mode, []byte{byte(b)})
		}
		for a := 0; a < 256; a++ {
			for b := 0; b < 256; b++ {
				checkData(mode, []byte{byte(a), byte(b)})
			}
		}
		for _, data := range corpus {
			checkData(mode, data)
		}
	}
}
