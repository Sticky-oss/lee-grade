package main

import "testing"

func TestTruncate_runeAware(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},          // under limit: unchanged
		{"exactlyten", 10, "exactlyten"}, // at limit: unchanged
		{"abcdefghijk", 5, "abcd…"},      // clipped to n runes incl. ellipsis
		{"héllo wörld", 6, "héllo…"},     // multibyte: counts runes, never splits a byte
		{"日本語テスト", 3, "日本…"},          // CJK: 3 runes out, not 3 bytes
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
		// Result must never exceed n runes.
		if r := []rune(truncate(c.in, c.n)); len(r) > c.n {
			t.Errorf("truncate(%q,%d) produced %d runes, want <= %d", c.in, c.n, len(r), c.n)
		}
	}
}

func TestSanitize_stripsControlChars(t *testing.T) {
	// Newlines, tabs, the ESC that starts an ANSI sequence, and DEL all go;
	// printable text (including the leftover "[31m") stays.
	if got := sanitize("a\nb\tc\x1b[31md\x7f"); got != "abc[31md" {
		t.Errorf("sanitize = %q, want %q", got, "abc[31md")
	}
	if got := sanitize("clean text"); got != "clean text" {
		t.Errorf("sanitize altered clean text: %q", got)
	}
}
