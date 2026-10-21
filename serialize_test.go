package termemu

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"testing"
)

func jsonRoundTrip[T any](t *testing.T, in T) T {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", data, err)
	}
	return out
}

func gobRoundTrip[T any](t *testing.T, in T) T {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("gob.Encode: %v", err)
	}
	var out T
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("gob.Decode: %v", err)
	}
	return out
}

func styleWith(component ColorComponent, color Color, modes ...Mode) Style {
	s := NewStyle()
	if err := s.SetColor(component, color); err != nil {
		panic(err)
	}
	s.SetMode(modes...)
	return s
}

func TestStyleSerialization(t *testing.T) {
	cases := []struct {
		name  string
		style Style
	}{
		{"zero", Style{}},
		{"default", NewStyle()},
		{"256-color", styleWith(ComponentFG, Color256(42))},
		{"bright", styleWith(ComponentFG, BrightColor(3))},
		{"rgb", styleWith(ComponentFG, RGB(1, 2, 3))},
		{"bold", styleWith(ComponentFG, Color256(1), ModeBold)},
		{"dim", styleWith(ComponentFG, Color256(1), ModeDim)},
		{"italic", styleWith(ComponentFG, Color256(1), ModeItalic)},
		{"underline", styleWith(ComponentFG, Color256(1), ModeUnderline)},
		{"blink", styleWith(ComponentFG, Color256(1), ModeBlink)},
		{"reverse", styleWith(ComponentFG, Color256(1), ModeReverse)},
		{"invisible", styleWith(ComponentFG, Color256(1), ModeInvisible)},
		{"strike", styleWith(ComponentFG, Color256(1), ModeStrike)},
		{"overline", styleWith(ComponentFG, Color256(1), ModeOverline)},
		{"double-underline", styleWith(ComponentFG, Color256(1), ModeDoubleUnderline)},
		{"framed", styleWith(ComponentFG, Color256(1), ModeFramed)},
		{"encircled", styleWith(ComponentFG, Color256(1), ModeEncircled)},
		{"rapid-blink", styleWith(ComponentFG, Color256(1), ModeRapidBlink)},
		{"underline-color", styleWith(ComponentUnderline, RGB(9, 8, 7))},
		{"all-combined", func() Style {
			s := NewStyle()
			_ = s.SetColor(ComponentFG, RGB(255, 128, 0))
			_ = s.SetColor(ComponentBG, Color256(200))
			_ = s.SetColor(ComponentUnderline, BrightColor(5))
			s.SetMode(ModeBold, ModeDim, ModeItalic, ModeUnderline, ModeBlink,
				ModeReverse, ModeInvisible, ModeStrike, ModeOverline,
				ModeDoubleUnderline, ModeFramed, ModeEncircled, ModeRapidBlink)
			return s
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.style)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if string(jsonData) == "{}" {
				t.Fatalf("Style JSON is empty object: %s", jsonData)
			}
			if got := jsonRoundTrip(t, tc.style); got != tc.style {
				t.Fatalf("JSON round-trip mismatch: got %#v want %#v", got, tc.style)
			}
			if got := gobRoundTrip(t, tc.style); got != tc.style {
				t.Fatalf("gob round-trip mismatch: got %#v want %#v", got, tc.style)
			}
		})
	}
}

func TestStyleJSONNullNoOp(t *testing.T) {
	s := styleWith(ComponentFG, RGB(1, 2, 3), ModeBold)
	want := s
	if err := json.Unmarshal([]byte("null"), &s); err != nil {
		t.Fatalf("json.Unmarshal(null): %v", err)
	}
	if s != want {
		t.Fatalf("null changed style: got %#v want %#v", s, want)
	}
}

func TestSpanLineRegionPosSerialization(t *testing.T) {
	style := styleWith(ComponentFG, RGB(4, 5, 6), ModeBold, ModeUnderline)
	span := Span{Style: style, Text: "hi", Width: 2, Link: "https://example.com"}
	blank := Span{Style: style, Width: 3, Link: "https://example.com"}
	wide := Span{Style: style, Text: "界", Width: 2}
	line := Line{Spans: []Span{span, blank, wide}, Width: 7}

	for _, tc := range []struct {
		name string
		in   any
		out  any
	}{
		{"Span", span, new(Span)},
		{"Line", line, new(Line)},
		{"Region", Region{X: 1, Y: 2, X2: 3, Y2: 4}, new(Region)},
		{"Pos", Pos{X: 5, Y: 6}, new(Pos)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if err := json.Unmarshal(jsonData, tc.out); err != nil {
				t.Fatalf("json.Unmarshal(%s): %v", jsonData, err)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.out).Elem().Interface(), tc.in) {
				t.Fatalf("JSON mismatch: got %#v want %#v", tc.out, tc.in)
			}

			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(tc.in); err != nil {
				t.Fatalf("gob.Encode: %v", err)
			}
			if err := gob.NewDecoder(&buf).Decode(tc.out); err != nil {
				t.Fatalf("gob.Decode: %v", err)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.out).Elem().Interface(), tc.in) {
				t.Fatalf("gob mismatch: got %#v want %#v", tc.out, tc.in)
			}
		})
	}
}

func TestScrollbackSnapshotSerialization(t *testing.T) {
	style := styleWith(ComponentFG, Color256(99), ModeItalic)
	snap := ScrollbackSnapshot{
		Rows: []Row{
			{Spans: []Span{{Style: style, Text: "abc", Width: 3}}, Width: 3, ID: 10},
			{Spans: []Span{{Style: style, Width: 4, Link: "https://x"}}, Width: 4, Wrapped: true, ID: 11},
		},
		Base: 10,
		Next: 12,
	}

	if got := jsonRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("JSON mismatch: got %#v want %#v", got, snap)
	}
	if got := gobRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("gob mismatch: got %#v want %#v", got, snap)
	}
}

func TestSnapshotSerialization(t *testing.T) {
	contentStyle := styleWith(ComponentFG, RGB(10, 20, 30), ModeBold)
	bgStyle := styleWith(ComponentBG, Color256(17), ModeUnderline)

	mainScreen := ScreenState{
		Width: 5, Height: 3,
		Lines: []Line{
			{Spans: []Span{{Style: contentStyle, Text: "main", Width: 4, Link: "https://link"}}, Width: 5},
			{Spans: []Span{{Style: bgStyle, Width: 5}}, Width: 5},
			{Spans: []Span{{Style: contentStyle, Text: "界", Width: 2}, {Style: contentStyle, Width: 3, Link: "https://link"}}, Width: 5},
		},
		Wrapped:      []bool{false, true, false},
		Cursor:       Pos{X: 2, Y: 1},
		SavedCursor:  Pos{X: 4, Y: 2},
		Style:        contentStyle,
		TopMargin:    1,
		BottomMargin: 2,
		AutoWrap:     true,
	}
	altScreen := ScreenState{
		Width: 5, Height: 3,
		Lines: []Line{
			{Spans: []Span{{Style: bgStyle, Text: "alt", Width: 3}}, Width: 5},
			{Spans: []Span{{Style: contentStyle, Width: 5}}, Width: 5},
			{Spans: []Span{{Style: bgStyle, Width: 5}}, Width: 5},
		},
		Wrapped:      []bool{false, false, true},
		Cursor:       Pos{X: 1, Y: 0},
		SavedCursor:  Pos{X: 0, Y: 0},
		Style:        bgStyle,
		TopMargin:    0,
		BottomMargin: 2,
		AutoWrap:     false,
	}

	flags := make([]bool, int(viewFlagCount))
	flags[VFShowCursor] = true
	flags[VFSynchronizedOutput] = true
	ints := make([]int, int(viewIntCount))
	ints[VIMouseMode] = MMPressReleaseMove
	ints[VIMouseEncoding] = MESGR
	ints[VIModifyOtherKeys] = 2
	strs := make([]string, int(viewStringCount))
	strs[VSWindowTitle] = "serialization"
	strs[VSCurrentDirectory] = "file:///tmp"

	snap := Snapshot{
		Main:         mainScreen,
		Alt:          altScreen,
		OnAltScreen:  true,
		ViewFlags:    flags,
		ViewInts:     ints,
		ViewStrings:  strs,
		KeyboardMain: keyboardMode{flags: 3, stack: []int{0, 1}},
		KeyboardAlt:  keyboardMode{flags: 1, stack: []int{0}},
		Link:         "https://link",
	}
	original := cloneSnapshotForTest(snap)

	if got := jsonRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("JSON mismatch:\n got %#v\nwant %#v", got, snap)
	}
	if !reflect.DeepEqual(snap, original) {
		t.Fatalf("JSON round-trip mutated original")
	}

	if got := gobRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("gob mismatch:\n got %#v\nwant %#v", got, snap)
	}
	if !reflect.DeepEqual(snap, original) {
		t.Fatalf("gob round-trip mutated original")
	}
}

// cloneSnapshotForTest deep-copies enough of snap to detect accidental mutation.
func cloneSnapshotForTest(s Snapshot) Snapshot {
	out := s
	out.Main = cloneScreenForTest(s.Main)
	out.Alt = cloneScreenForTest(s.Alt)
	out.ViewFlags = append([]bool(nil), s.ViewFlags...)
	out.ViewInts = append([]int(nil), s.ViewInts...)
	out.ViewStrings = append([]string(nil), s.ViewStrings...)
	out.KeyboardMain = cloneKeyboardMode(s.KeyboardMain)
	out.KeyboardAlt = cloneKeyboardMode(s.KeyboardAlt)
	return out
}

func cloneScreenForTest(s ScreenState) ScreenState {
	out := s
	out.Lines = make([]Line, len(s.Lines))
	for i, line := range s.Lines {
		out.Lines[i] = Line{Spans: append([]Span(nil), line.Spans...), Width: line.Width}
	}
	out.Wrapped = append([]bool(nil), s.Wrapped...)
	return out
}

func TestSnapshotSerializationFromTerminal(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeGrapheme)

	// Main screen: styled text, a wrapped long line, margins, and an OSC 8 link.
	input := "\x1b[2;4r\x1b[1;31mred\x1b[0m " +
		"\x1b]8;;https://example.com\x1b\\linked\x1b]8;;\x1b\\\r\n" +
		"abcdefghijklmnopqrstuvwxyz0123456789\r\n"
	if err := term.testFeedTerminalInputFromBackend([]byte(input), TextReadModeGrapheme); err != nil {
		t.Fatalf("feeding main screen: %v", err)
	}
	term.pushKeyboardFlags(4)
	term.pushKeyboardFlags(1)
	term.updateKeyboardFlags(2, 2)

	// Switch to the alt screen and write different content there.
	altInput := "\x1b[?1049h\x1b[44malt screen\x1b[0m"
	if err := term.testFeedTerminalInputFromBackend([]byte(altInput), TextReadModeGrapheme); err != nil {
		t.Fatalf("feeding alt screen: %v", err)
	}
	term.pushKeyboardFlags(8)
	term.setViewFlag(VFShowCursor, true)
	term.setViewInt(VIMouseMode, MMPressReleaseMove)
	term.setViewString(VSWindowTitle, "terminal-snapshot")

	snap := term.Snapshot()
	if !snap.OnAltScreen {
		t.Fatal("expected alt screen active")
	}
	if len(snap.KeyboardMain.stack) == 0 || len(snap.KeyboardAlt.stack) == 0 {
		t.Fatalf("expected non-empty keyboard stacks: main=%#v alt=%#v",
			snap.KeyboardMain, snap.KeyboardAlt)
	}

	original := cloneSnapshotForTest(snap)
	if got := jsonRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("JSON mismatch:\n got %#v\nwant %#v", got, snap)
	}
	if !reflect.DeepEqual(snap, original) {
		t.Fatal("JSON round-trip mutated terminal snapshot")
	}
	if got := gobRoundTrip(t, snap); !reflect.DeepEqual(got, snap) {
		t.Fatalf("gob mismatch:\n got %#v\nwant %#v", got, snap)
	}
	if !reflect.DeepEqual(snap, original) {
		t.Fatal("gob round-trip mutated terminal snapshot")
	}
}
