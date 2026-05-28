package tui

import "testing"

func TestSanitize_DropsBareCRAndCRLF(t *testing.T) {
	got := sanitizePtyForViewport([]byte("hello\rworld\r\nnext"))
	want := "helloworld\nnext"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSanitize_DropsBEL(t *testing.T) {
	got := sanitizePtyForViewport([]byte("ding\x07dong"))
	if got != "dingdong" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitize_KeepsSGRColours(t *testing.T) {
	// Standard bash prompt fragment: green foreground, then reset.
	in := []byte("\x1b[32mlee@host\x1b[0m:~$ ")
	got := sanitizePtyForViewport(in)
	if got != string(in) {
		t.Fatalf("SGR should pass through unchanged; got %q", got)
	}
}

func TestSanitize_DropsCSICursorAndClear(t *testing.T) {
	// \e[K clear-EOL, \e[2J clear-screen, \e[H cursor-home, \e[?2004h bracketed-paste,
	// \e[10;5H absolute cursor — all should disappear.
	in := []byte("a\x1b[Kb\x1b[2Jc\x1b[Hd\x1b[?2004he\x1b[10;5Hf")
	got := sanitizePtyForViewport(in)
	if got != "abcdef" {
		t.Fatalf("got %q, want %q", got, "abcdef")
	}
}

func TestSanitize_DropsOSCWindowTitle(t *testing.T) {
	// \e]0;title\x07 — typical OSC window-title sequence
	in := []byte("before\x1b]0;new title\x07after")
	got := sanitizePtyForViewport(in)
	if got != "beforeafter" {
		t.Fatalf("got %q", got)
	}
	// \e]0;title\e\\ — ST-terminated variant
	in = []byte("before\x1b]0;new title\x1b\\after")
	got = sanitizePtyForViewport(in)
	if got != "beforeafter" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitize_RealisticBashPromptLine(t *testing.T) {
	// What bash actually emits at a fresh prompt — bracketed-paste enable,
	// SGR colour codes, prompt text, and the dread \r that started this bug.
	in := []byte("\r\x1b[?2004h\x1b[01;32mlee@lee-dev\x1b[00m:\x1b[01;34m~/lee-grade\x1b[00m$ ")
	got := sanitizePtyForViewport(in)
	// \r dropped, \e[?2004h dropped, SGR kept verbatim
	want := "\x1b[01;32mlee@lee-dev\x1b[00m:\x1b[01;34m~/lee-grade\x1b[00m$ "
	if got != want {
		t.Fatalf("\ngot:  %q\nwant: %q", got, want)
	}
}
