package termemu

import (
	"strings"
	"testing"
)

// markRecorder is a frontend that implements the optional OSC extension
// interfaces as well as the base Frontend.
type markRecorder struct {
	EmptyFrontend
	marks    []PromptMark
	notifies []struct{ title, body string }
}

func (m *markRecorder) PromptMark(v LockedView, mark PromptMark) {
	m.marks = append(m.marks, mark)
}

func (m *markRecorder) Notify(v LockedView, title, body string) {
	m.notifies = append(m.notifies, struct{ title, body string }{title, body})
}

func markRecorderTerm(t *testing.T) (*terminal, *markRecorder) {
	t.Helper()
	mf := &markRecorder{}
	term := newTerminal(mf, NewNoPTYBackend(nil, nil), TextReadModeRune)
	return term, mf
}

func TestOSC133_PromptMarks(t *testing.T) {
	term, mf := markRecorderTerm(t)

	feedText(t, term, "\x1b]133;A\x07")
	feedText(t, term, "\x1b]133;B\x1b\\")
	feedText(t, term, "\x1b]133;C\x07")
	feedText(t, term, "\x1b]133;D;0\x07")
	feedText(t, term, "\x1b]133;D;130\x07")
	feedText(t, term, "\x1b]133;D\x07")

	want := []PromptMark{
		{Kind: PromptMarkStart, ExitCode: -1},
		{Kind: PromptMarkInput, ExitCode: -1},
		{Kind: PromptMarkCommand, ExitCode: -1},
		{Kind: PromptMarkDone, ExitCode: 0, Raw: "0"},
		{Kind: PromptMarkDone, ExitCode: 130, Raw: "130"},
		{Kind: PromptMarkDone, ExitCode: -1},
	}
	if len(mf.marks) != len(want) {
		t.Fatalf("recorded %d marks, want %d: %+v", len(mf.marks), len(want), mf.marks)
	}
	for i := range want {
		if mf.marks[i] != want[i] {
			t.Fatalf("mark %d = %+v, want %+v", i, mf.marks[i], want[i])
		}
	}
	if got := strings.TrimSpace(term.Line(0)); got != "" {
		t.Fatalf("OSC 133 left text on screen: %q", got)
	}
}

func TestOSC133_KeepsRawParameters(t *testing.T) {
	term, mf := markRecorderTerm(t)
	feedText(t, term, "\x1b]133;D;7;extradata\x07")
	if len(mf.marks) != 1 {
		t.Fatalf("recorded %d marks, want 1", len(mf.marks))
	}
	got := mf.marks[0]
	if got.Kind != PromptMarkDone || got.ExitCode != 7 || got.Raw != "7;extradata" {
		t.Fatalf("mark = %+v, want done/7/\"7;extradata\"", got)
	}
}

func TestOSC133_UnknownMarksIgnored(t *testing.T) {
	term, mf := markRecorderTerm(t)
	for _, seq := range []string{
		"\x1b]133;P;k=v\x07", // prompt property: not a mark we decode
		"\x1b]133;Z\x07",
		"\x1b]133;\x07",
		"\x1b]133\x07",
	} {
		feedText(t, term, seq)
	}
	if len(mf.marks) != 0 {
		t.Fatalf("unknown marks produced %+v", mf.marks)
	}
	if got := strings.TrimSpace(term.Line(0)); got != "" {
		t.Fatalf("unknown marks left text on screen: %q", got)
	}
}

func TestOSC133_WithoutShellIntegrationFrontend(t *testing.T) {
	// EmptyFrontend does not implement ShellIntegration; the sequence must be
	// consumed silently.
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]133;A\x07rest")
	if got := strings.TrimSpace(term.Line(0)); got != "rest" {
		t.Fatalf("line = %q, want %q", got, "rest")
	}
}

func TestOSC9_Notification(t *testing.T) {
	term, mf := markRecorderTerm(t)
	feedText(t, term, "\x1b]9;Build finished\x07")
	if len(mf.notifies) != 1 {
		t.Fatalf("recorded %d notifications, want 1", len(mf.notifies))
	}
	if got := mf.notifies[0]; got.title != "" || got.body != "Build finished" {
		t.Fatalf("notification = %+v, want empty title and body %q", got, "Build finished")
	}
	if got := strings.TrimSpace(term.Line(0)); got != "" {
		t.Fatalf("OSC 9 left text on screen: %q", got)
	}
}

func TestOSC777_Notification(t *testing.T) {
	term, mf := markRecorderTerm(t)
	feedText(t, term, "\x1b]777;notify;Title;Body text\x07")
	feedText(t, term, "\x1b]777;notify;OnlyTitle\x07")
	feedText(t, term, "\x1b]777;somethingelse;x;y\x07")

	if len(mf.notifies) != 2 {
		t.Fatalf("recorded %d notifications, want 2: %+v", len(mf.notifies), mf.notifies)
	}
	if got := mf.notifies[0]; got.title != "Title" || got.body != "Body text" {
		t.Fatalf("notification 0 = %+v", got)
	}
	if got := mf.notifies[1]; got.title != "OnlyTitle" || got.body != "" {
		t.Fatalf("notification 1 = %+v", got)
	}
}

func TestOSC9_WithoutNotificationSink(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b]9;hello\x07\x1b]777;notify;a;b\x07after")
	if got := strings.TrimSpace(term.Line(0)); got != "after" {
		t.Fatalf("line = %q, want %q", got, "after")
	}
}

func TestOSC133_MarksDuringAltScreen(t *testing.T) {
	term, mf := markRecorderTerm(t)
	feedText(t, term, "\x1b[?1049h\x1b]133;A\x07\x1b[?1049l\x1b]133;D;1\x07")
	if len(mf.marks) != 2 {
		t.Fatalf("recorded %d marks, want 2", len(mf.marks))
	}
	if mf.marks[0].Kind != PromptMarkStart || mf.marks[1].Kind != PromptMarkDone || mf.marks[1].ExitCode != 1 {
		t.Fatalf("marks = %+v", mf.marks)
	}
}
