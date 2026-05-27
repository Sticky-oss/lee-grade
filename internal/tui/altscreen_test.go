// Unit tests for the alt-screen Scanner. The cross-read-boundary
// behaviour is the load-bearing part — naive bytes.Index on each read
// misses sequences split between two reads, and that's exactly what
// happens when bash flushes its output buffer in the middle of vim's
// startup paint.
package tui

import (
	"bytes"
	"testing"
)

func TestScanner_DetectsEnterSequenceInSingleRead(t *testing.T) {
	s := NewScanner()
	before, found, after := s.ScanEnter([]byte("prompt-tail\x1b[?1049hvim-start"))
	if !found {
		t.Fatal("expected alt-screen-enter to be detected")
	}
	if string(before) != "prompt-tail" {
		t.Fatalf("before: got %q, want %q", before, "prompt-tail")
	}
	if string(after) != "vim-start" {
		t.Fatalf("after: got %q, want %q", after, "vim-start")
	}
	if !s.InAltScreen() {
		t.Fatal("expected InAltScreen=true after detection")
	}
}

func TestScanner_DetectsEnterSequenceAcrossTwoReads(t *testing.T) {
	// The 8-byte sequence \x1b[?1049h split as the trailing 5 bytes
	// of read 1 and the leading 3 bytes of read 2. Real-world: bash
	// flushes its 4 KiB write buffer mid-sequence.
	s := NewScanner()
	first := []byte("abc\x1b[?10") // 8 bytes including the sequence head
	before, found, _ := s.ScanEnter(first)
	if found {
		t.Fatal("first read shouldn't match — sequence isn't complete yet")
	}
	if !bytes.HasPrefix(first, before) || len(before) > len(first) {
		t.Fatalf("first-read drained bytes look wrong: got %q", before)
	}

	second := []byte("49htail")
	before2, found2, after2 := s.ScanEnter(second)
	if !found2 {
		t.Fatal("second read should complete the sequence and match")
	}
	// Total bytes before the sequence across both reads = "abc"
	allBefore := string(before) + string(before2)
	if allBefore != "abc" {
		t.Fatalf("aggregate before: got %q, want %q", allBefore, "abc")
	}
	if string(after2) != "tail" {
		t.Fatalf("after-tail: got %q, want %q", after2, "tail")
	}
}

func TestScanner_RecognisesAllThreeAltScreenVariants(t *testing.T) {
	cases := []struct {
		name string
		seq  string
	}{
		{"modern xterm 1049", "\x1b[?1049h"},
		{"intermediate 1047", "\x1b[?1047h"},
		{"oldest 47", "\x1b[?47h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewScanner()
			_, found, _ := s.ScanEnter([]byte("x" + c.seq + "y"))
			if !found {
				t.Fatalf("variant %q not detected", c.seq)
			}
		})
	}
}

func TestScanner_NoMatchKeepsLast7BytesAsCarry(t *testing.T) {
	// A long-ish read with no sequence at all should drain everything
	// except the trailing 7 bytes (the carry window).
	s := NewScanner()
	in := []byte("hello world, this is normal shell output")
	before, found, _ := s.ScanEnter(in)
	if found {
		t.Fatal("no sequence in input — should not match")
	}
	if len(before) != len(in)-7 {
		t.Fatalf("drained len: got %d, want %d", len(before), len(in)-7)
	}
	// Tail should be the last 7 bytes.
	if string(s.tail) != string(in[len(in)-7:]) {
		t.Fatalf("tail: got %q, want %q", s.tail, in[len(in)-7:])
	}
}

func TestScanner_ExitSequenceDetectionIncludesSequenceInOutput(t *testing.T) {
	// On exit we WANT the sequence bytes forwarded to the user's
	// terminal — the terminal needs them to restore the original
	// screen buffer.
	s := NewScanner()
	s.inAltScreen.Store(true)
	out, found, after := s.ScanExit([]byte("vim-tail\x1b[?1049lprompt-resume"))
	if !found {
		t.Fatal("exit sequence not detected")
	}
	if !bytes.HasSuffix(out, []byte("\x1b[?1049l")) {
		t.Fatalf("exit output should end with the sequence: %q", out)
	}
	if string(after) != "prompt-resume" {
		t.Fatalf("post-exit tail: got %q, want %q", after, "prompt-resume")
	}
	if s.InAltScreen() {
		t.Fatal("expected InAltScreen=false after exit detection")
	}
}

func TestScanner_ExitSequenceAcrossTwoReads(t *testing.T) {
	// Symmetric to the enter cross-read test.
	s := NewScanner()
	s.inAltScreen.Store(true)
	_, found1, _ := s.ScanExit([]byte("foo\x1b[?10"))
	if found1 {
		t.Fatal("first read shouldn't match — exit sequence incomplete")
	}
	out, found2, after := s.ScanExit([]byte("49lbar"))
	if !found2 {
		t.Fatal("second read should complete + match")
	}
	if !bytes.HasSuffix(out, []byte("\x1b[?1049l")) {
		t.Fatalf("expected sequence at tail of out: %q", out)
	}
	if string(after) != "bar" {
		t.Fatalf("after: got %q, want %q", after, "bar")
	}
}
