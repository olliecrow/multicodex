package editor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

func TestTmuxAttachArgsSeparateCreatedAndAdoptedSessions(t *testing.T) {
	windowID := "111111111111111111111111"
	created := Window{ID: windowID, Session: "mce-" + windowID}
	got, err := tmuxAttachArgs(testInstanceID, created)
	want := []string{"-L", "mce-" + testInstanceID[:12], "attach-session", "-t", "=" + created.Session + ":"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("created attach args = %v, %v; want %v", got, err, want)
	}
	adopted := Window{ID: windowID, Session: "existing-session", TmuxSessionID: "$7", Adopted: true}
	got, err = tmuxAttachArgs(testInstanceID, adopted)
	want = []string{"-L", "default", "attach-session", "-t", adopted.TmuxSessionID}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("adopted attach args = %v, %v; want %v", got, err, want)
	}
}

func TestRemoteTmuxArgumentsAreShellQuoted(t *testing.T) {
	if got, want := quoteRemoteShellArg("$7; touch nope"), "'$7; touch nope'"; got != want {
		t.Fatalf("quoted remote argument = %q, want %q", got, want)
	}
	if got, want := quoteRemoteShellArg("a'b"), "'a'\\''b'"; got != want {
		t.Fatalf("quoted remote apostrophe = %q, want %q", got, want)
	}
}

func TestModifiedKeySequencesPreserveImportantModifiers(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"shift enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "\x1b[13;2u"},
		{"ctrl enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}, "\x1b[13;5u"},
		{"shift tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z"},
		{"alt left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, "\x1b[1;3D"},
		{"ctrl down", tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl}, "\x1b[1;5B"},
		{"shift ctrl text", tea.KeyPressMsg{Code: 'c', Text: "C", Mod: tea.ModShift | tea.ModCtrl}, "\x1b[99;6u"},
		{"meta text", tea.KeyPressMsg{Code: 'x', Text: "x", Mod: tea.ModMeta}, "\x1b[120;9u"},
		{"shift f1", tea.KeyPressMsg{Code: tea.KeyF1, Mod: tea.ModShift}, "\x1b[1;2P"},
		{"ctrl f12", tea.KeyPressMsg{Code: tea.KeyF12, Mod: tea.ModCtrl}, "\x1b[24;5~"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := modifiedKeySequence(test.key)
			if !ok || got != test.want {
				t.Fatalf("sequence = %q, %v; want %q", got, ok, test.want)
			}
		})
	}
}

func TestSafeTerminalRenderDoesNotEmitChildOSCSequences(t *testing.T) {
	term := newTerminalEmulator(24, 4)
	defer term.Close()
	_, _ = term.Write([]byte("\x1b]52;c;private\a\x1b]8;;https://invalid.test\aSAFE\x1b]8;;\a"))
	rendered := safeTerminalRender(term, 24, 4)
	if !strings.Contains(rendered, "SAFE") {
		t.Fatalf("missing safe text: %q", rendered)
	}
	for _, forbidden := range []string{"\x1b]52", "\x1b]8", "private", "https://invalid.test"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered child control data %q: %q", forbidden, rendered)
		}
	}
}

func TestTerminalDiscardsHyperlinkMetadataBeforeCellsRetainIt(t *testing.T) {
	const width = 20
	term := newTerminalEmulator(width, 2)
	defer term.Close()
	large := strings.Repeat("x", 64<<10)
	for index := 0; index < width; index++ {
		_, _ = term.Write([]byte(fmt.Sprintf("\x1b]8;;https://invalid.test/%s/%d\aX", large, index)))
	}
	for x := 0; x < width; x++ {
		cell := term.CellAt(x, 0)
		if cell == nil || cell.Content != "X" {
			t.Fatalf("cell %d content = %+v", x, cell)
		}
		if cell.Link.URL != "" || cell.Link.Params != "" {
			t.Fatalf("cell %d retained hyperlink metadata", x)
		}
	}
}

func TestAllBidiControlsAreRejectedOrRemovedFromEditorDisplay(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		value := "left" + string(r) + "right"
		if err := validateName(value, "name"); err == nil {
			t.Fatalf("validateName accepted bidi control U+%04X", r)
		}
		for label, got := range map[string]string{
			"client": safeClientText(value, 100),
			"view":   plainDisplayText(value),
		} {
			if strings.ContainsRune(got, r) {
				t.Fatalf("%s retained bidi control U+%04X", label, r)
			}
		}
		term := newTerminalEmulator(16, 2)
		_, _ = term.Write([]byte(value))
		rendered := safeTerminalRender(term, 16, 2)
		_ = term.Close()
		if strings.ContainsRune(rendered, r) {
			t.Fatalf("terminal render retained bidi control U+%04X", r)
		}
	}
}

func TestReplaceEnvironmentRemovesDuplicateValue(t *testing.T) {
	got := replaceEnvironment([]string{"A=1", "TERM=old", "TERM=older"}, "TERM", "xterm-256color")
	joined := strings.Join(got, "|")
	if strings.Count(joined, "TERM=") != 1 || !strings.Contains(joined, "TERM=xterm-256color") {
		t.Fatalf("unexpected environment: %v", got)
	}
}

func TestDirectTextAcceptsShiftedUnicodeButNotCommandModifiers(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'N', Text: "N", Mod: tea.ModShift},
		{Code: '界', Text: "界"},
	} {
		if !isDirectTextKey(key) {
			t.Fatalf("expected direct text key: %+v", key)
		}
	}
	for _, key := range []tea.KeyPressMsg{
		{Code: 'c', Text: "c", Mod: tea.ModCtrl},
		{Code: 'x', Text: "x", Mod: tea.ModAlt},
	} {
		if isDirectTextKey(key) {
			t.Fatalf("did not expect direct text key: %+v", key)
		}
	}
}

func TestTerminalInputQueueIsBoundedAndNonBlocking(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	if err := attachment.Paste("first"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := attachment.Paste("second"); err == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("full terminal input queue = %v after %s", err, time.Since(started))
	}
	if err := attachment.Paste(strings.Repeat("x", maxTerminalPaste+1)); err == nil {
		t.Fatal("oversized terminal paste was accepted")
	}
}

func TestTerminalMouseSequencesPreserveCoordinatesButtonsAndModifiers(t *testing.T) {
	tests := []struct {
		name  string
		event tea.MouseMsg
		x, y  int
		want  string
	}{
		{"left click", tea.MouseClickMsg{Button: tea.MouseLeft}, 4, 2, "\x1b[<0;5;3M"},
		{"right release", tea.MouseReleaseMsg{Button: tea.MouseRight}, 0, 0, "\x1b[<2;1;1m"},
		{"shift wheel up", tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModShift}, 9, 7, "\x1b[<68;10;8M"},
		{"ctrl wheel down", tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModCtrl}, 1, 1, "\x1b[<81;2;2M"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := terminalMouseSequence(test.event, test.x, test.y)
			if !ok || got != test.want {
				t.Fatalf("mouse sequence = %q, %v; want %q", got, ok, test.want)
			}
		})
	}
	if _, ok := terminalMouseSequence(tea.MouseClickMsg{Button: tea.MouseLeft}, -1, 0); ok {
		t.Fatal("negative mouse coordinate was accepted")
	}
}

func TestTerminalPasteRemovesInjectedControlSequences(t *testing.T) {
	attachment := &Attachment{inputQueue: make(chan terminalInput, 1)}
	input := "safe\n\x1b[201~printf injected\n\u009b31mend\t"
	if err := attachment.Paste(input); err != nil {
		t.Fatal(err)
	}
	queued := <-attachment.inputQueue
	if queued.kind != "paste" || queued.text != "safe\n[201~printf injected\n31mend\t" {
		t.Fatalf("sanitized paste = %q", queued.text)
	}
	if strings.ContainsAny(queued.text, "\x1b\u009b") {
		t.Fatalf("paste retained terminal controls: %q", queued.text)
	}
}
