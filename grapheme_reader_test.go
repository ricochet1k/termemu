package termemu

import (
	"io"
	"testing"
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
