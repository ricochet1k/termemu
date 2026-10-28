package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoadCast_v2(t *testing.T) {
	input := `{"version": 2, "width": 80, "height": 24}
[0.1, "o", "hello "]
[0.2, "o", "world"]
[0.3, "i", "ignored input"]
`
	hdr, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	if hdr.Version != 2 || hdr.Width != 80 || hdr.Height != 24 {
		t.Fatalf("header = %+v, want version 2 80x24", hdr)
	}
	if got := string(out); got != "hello world" {
		t.Fatalf("output = %q, want %q", got, "hello world")
	}
}

func TestLoadCast_v3(t *testing.T) {
	input := `{"version": 3, "width": 100, "height": 30}
{"time": 0.1, "type": "o", "data": "foo\r\n"}
{"time": 0.2, "type": "m", "data": "ignored marker"}
{"time": 0.3, "type": "o", "data": "bar"}
`
	hdr, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	if hdr.Version != 3 || hdr.Width != 100 || hdr.Height != 30 {
		t.Fatalf("header = %+v, want version 3 100x30", hdr)
	}
	if got := string(out); got != "foo\r\nbar" {
		t.Fatalf("output = %q, want %q", got, "foo\r\nbar")
	}
}

func TestLoadCast_crlf(t *testing.T) {
	input := "{\"version\": 2, \"width\": 10, \"height\": 10}\r\n[0.0, \"o\", \"a\"]\r\n[0.1, \"o\", \"b\"]\r\n"
	hdr, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	if hdr.Width != 10 || hdr.Height != 10 {
		t.Fatalf("header = %+v", hdr)
	}
	if got := string(out); got != "ab" {
		t.Fatalf("output = %q, want %q", got, "ab")
	}
}

func TestLoadCast_resizeIgnored(t *testing.T) {
	input := `{"version": 2, "width": 80, "height": 24}
[0.0, "o", "x"]
[0.1, "r", "160x48"]
[0.2, "o", "y"]
`
	hdr, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	if hdr.Width != 80 || hdr.Height != 24 {
		t.Fatalf("header size changed to %dx%d", hdr.Width, hdr.Height)
	}
	if got := string(out); got != "xy" {
		t.Fatalf("output = %q, want %q", got, "xy")
	}
}

func TestLoadCast_emptyOutput(t *testing.T) {
	input := `{"version": 2, "width": 10, "height": 10}
`
	hdr, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	if hdr.Width != 10 || hdr.Height != 10 {
		t.Fatalf("header = %+v", hdr)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %q", string(out))
	}
}

func TestLoadCast_invalidVersion(t *testing.T) {
	input := `{"version": 1, "width": 80, "height": 24}
`
	_, _, err := LoadCast(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for version 1")
	}
}

func TestLoadCast_binaryEscapes(t *testing.T) {
	// JSON string unescaping should turn \\x1b into a real ESC byte.
	input := `{"version": 2, "width": 10, "height": 10}
[0.0, "o", "\u001b[31mred\u001b[0m"]
`
	_, out, err := LoadCast(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadCast: %v", err)
	}
	want := []byte{0x1b, '[', '3', '1', 'm', 'r', 'e', 'd', 0x1b, '[', '0', 'm'}
	if !bytes.Equal(out, want) {
		t.Fatalf("output = %q, want %q", out, want)
	}
}
