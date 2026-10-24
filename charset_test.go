package termemu

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// charsetLine returns row y of the terminal with the blank padding trimmed.
func charsetLine(t *testing.T, t1 *terminal, y int) string {
	t.Helper()
	return strings.TrimRight(t1.Line(y), " ")
}

// decGraphicsLine writes the 0x5F-0x7E range through the terminal and returns
// the rendered line.
func decGraphicsLine(t *testing.T, t1 *terminal, prefix string) string {
	t.Helper()
	var b strings.Builder
	for c := byte(0x5f); c <= 0x7e; c++ {
		b.WriteByte(c)
	}
	feedText(t, t1, prefix+b.String())
	return charsetLine(t, t1, 0)
}

func TestDECGraphics_DefaultIsASCII(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "lqkxjm")
	if got, want := charsetLine(t, t1, 0), "lqkxjm"; got != want {
		t.Fatalf("default charset rendered %q, want %q", got, want)
	}
}

func TestDECGraphics_G0Designation(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(0")
	feedText(t, t1, "lqkxjm")
	if got, want := charsetLine(t, t1, 0), "┌─┐│┘└"; got != want {
		t.Fatalf("DEC graphics rendered %q, want %q", got, want)
	}
}

func TestDECGraphics_FullMappedRange(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	line := decGraphicsLine(t, t1, "\x1b(0")
	got := []rune(line)
	if len(got) != len(decSpecialGraphics) {
		t.Fatalf("rendered %d cells, want %d: %q", len(got), len(decSpecialGraphics), line)
	}
	for i, want := range decSpecialGraphics {
		if got[i] != want {
			t.Fatalf("cell %d (0x%02x) = %#U, want %#U", i, 0x5f+i, got[i], want)
		}
	}
}

func TestDECGraphics_UnmappedRangeIsUntouched(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(0")
	feedText(t, t1, "ABC 019!?")
	if got, want := charsetLine(t, t1, 0), "ABC 019!?"; got != want {
		t.Fatalf("unmapped range rendered %q, want %q", got, want)
	}
}

func TestDECGraphics_UTF8PassesThrough(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeGrapheme)
	feedText(t, t1, "\x1b(0")
	feedText(t, t1, "qÜq日本q")
	if got, want := charsetLine(t, t1, 0), "─Ü─日本─"; got != want {
		t.Fatalf("mixed UTF-8 rendered %q, want %q", got, want)
	}
}

func TestCharsets_SHIFTINOut(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b)0") // G1 = DEC graphics
	feedText(t, t1, "q")      // GL is still G0
	if got, want := charsetLine(t, t1, 0), "q"; got != want {
		t.Fatalf("before SO rendered %q, want %q", got, want)
	}
	feedText(t, t1, "\x0eq") // SO invokes G1
	if got, want := charsetLine(t, t1, 0), "q─"; got != want {
		t.Fatalf("after SO rendered %q, want %q", got, want)
	}
	feedText(t, t1, "\x0fq") // SI invokes G0 again
	if got, want := charsetLine(t, t1, 0), "q─q"; got != want {
		t.Fatalf("after SI rendered %q, want %q", got, want)
	}
}

func TestCharsets_G2G3(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b*0Q") // G2 = DEC graphics, invoke via ESC n
	feedText(t, t1, "\x1bnq")
	if got, want := charsetLine(t, t1, 0), "Q─"; got != want {
		t.Fatalf("LS2 rendered %q, want %q", got, want)
	}

	_, t2, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t2, "\x1b+0R") // G3 = DEC graphics, invoke via ESC o
	feedText(t, t2, "\x1boq")
	if got, want := charsetLine(t, t2, 0), "R─"; got != want {
		t.Fatalf("LS3 rendered %q, want %q", got, want)
	}

	// ESC n/o must not change the G0/G1 designations.
	_, t3, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t3, "\x1b*0\x1bn\x0fq")
	if got, want := charsetLine(t, t3, 0), "q"; got != want {
		t.Fatalf("SI after LS2 rendered %q, want %q", got, want)
	}
}

func TestCharsets_ReDesignationResetsToASCII(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(0q")
	feedText(t, t1, "\x1b(Bq")
	if got, want := charsetLine(t, t1, 0), "─q"; got != want {
		t.Fatalf("re-designation rendered %q, want %q", got, want)
	}
}

func TestCharsets_NationalReplacementFallsBackToASCII(t *testing.T) {
	// ESC ( A designates the British set, which is accepted but renders as
	// ASCII (a UTF-8 terminal does not need '\u00a3' for '#').
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(A#q")
	if got, want := charsetLine(t, t1, 0), "#q"; got != want {
		t.Fatalf("national designation rendered %q, want %q", got, want)
	}
}

func TestCharsets_DECSCRestoresDesignation(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(0\x1b7")      // DECSC saves DEC graphics
	feedText(t, t1, "\x1b(B\x1b)0\x0e") // back to ASCII, G1 graphics invoked
	feedText(t, t1, "\x1b8")            // DECRC restores G0 graphics / GL G0
	feedText(t, t1, "q")
	if got, want := charsetLine(t, t1, 0), "─"; got != want {
		t.Fatalf("DECRC charset rendered %q, want %q", got, want)
	}
}

func TestCharsets_RISAndDECSTRReset(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"RIS", "\x1bc"},
		{"DECSTR", "\x1b[!p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
			feedText(t, t1, "\x1b(0")
			feedText(t, t1, tc.seq)
			feedText(t, t1, "q")
			if got, want := charsetLine(t, t1, 0), "q"; got != want {
				t.Fatalf("after %s rendered %q, want %q", tc.name, got, want)
			}
		})
	}
}

func TestCharsets_SharedAcrossScreenSwitch(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b(0q") // main screen in DEC graphics
	t1.mustHandleCommand(t, "[?1049h")
	feedText(t, t1, "\rq")
	if got, want := charsetLine(t, t1, 0), "─"; got != want {
		t.Fatalf("alt screen inherited charset rendered %q, want %q", got, want)
	}
	t1.mustHandleCommand(t, "[?1049l")
	feedText(t, t1, "q")
	if got, want := charsetLine(t, t1, 0), "──"; got != want {
		t.Fatalf("main screen after switch rendered %q, want %q", got, want)
	}
}

func TestCharsets_SnapshotRestore(t *testing.T) {
	_, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t1, "\x1b)0\x0eq")

	snap := t1.Snapshot()
	if got, want := snap.Main.Charsets.gl(), charsetDECGraphics; got != want {
		t.Fatalf("snapshot GL charset = %v, want %v", got, want)
	}

	_, t2, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := t2.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	feedText(t, t2, "q")
	if got, want := charsetLine(t, t2, 0), "──"; got != want {
		t.Fatalf("restored screen rendered %q, want %q", got, want)
	}

	// DECSC state must survive the round-trip too.
	_, t3, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, t3, "\x1b(0\x1b7")
	t3snap := t3.Snapshot()
	feedText(t, t3, "\x1b(B")
	_, t4, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := t4.Restore(t3snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	feedText(t, t4, "\x1b8q")
	if got, want := charsetLine(t, t4, 0), "─"; got != want {
		t.Fatalf("restored saved charset rendered %q, want %q", got, want)
	}
}

func TestCharsetState_JSONGobRoundTrip(t *testing.T) {
	state := charsetState{
		designations: [charsetSlotCount]charset{charsetASCII, charsetDECGraphics, charsetASCII, charsetDECGraphics},
		glSlot:       3,
	}
	if got := jsonRoundTrip(t, state); !reflect.DeepEqual(got, state) {
		t.Fatalf("charsetState JSON round-trip = %#v, want %#v", got, state)
	}
	if got := gobRoundTrip(t, state); !reflect.DeepEqual(got, state) {
		t.Fatalf("charsetState gob round-trip = %#v, want %#v", got, state)
	}

	// Unknown designations and out-of-range GL slots decode to the safe
	// defaults instead of corrupting the state.
	var decoded charsetState
	if err := json.Unmarshal([]byte(`{"designations":[7,0,0,0],"gl":9}`), &decoded); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if want := (charsetState{}); !reflect.DeepEqual(decoded, want) {
		t.Fatalf("unknown charset decoded to %#v, want %#v", decoded, want)
	}
}

func TestMapDECGraphics_AllocFreeWhenUnmapped(t *testing.T) {
	in := "PLAIN ASCII 019!?"
	if got := mapDECGraphics(in); got != in {
		t.Fatalf("mapDECGraphics(%q) = %q", in, got)
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = mapDECGraphics(in) }); allocs != 0 {
		t.Fatalf("mapDECGraphics allocated %v times for unmapped text", allocs)
	}
}

func TestMapDECGraphics_PreservesWidth(t *testing.T) {
	for _, in := range []string{"q", "lqk", "_~", "qabc", "éq"} {
		out := mapDECGraphics(in)
		if got, want := len([]rune(out)), len([]rune(in)); got != want {
			t.Fatalf("mapDECGraphics(%q) = %q has %d runes, want %d", in, out, got, want)
		}
	}
}
