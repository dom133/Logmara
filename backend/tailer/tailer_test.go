package tailer

import (
	"bufio"
	"bytes"
	"os"
	"testing"
)

func TestLineSplitterCompleteLine(t *testing.T) {
	s := &lineSplitter{}
	data := []byte("hello world\nrest")
	advance, token, err := s.split(data, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(token) != "hello world" {
		t.Fatalf("token = %q, want %q", token, "hello world")
	}
	if advance != len("hello world\n") {
		t.Fatalf("advance = %d, want %d", advance, len("hello world\n"))
	}
	if s.lastAdvance != int64(advance) {
		t.Fatalf("lastAdvance = %d, want %d", s.lastAdvance, advance)
	}
}

func TestLineSplitterOversizedForcesCut(t *testing.T) {
	s := &lineSplitter{}
	data := bytes.Repeat([]byte("x"), maxLineSize+100) // no newline anywhere
	advance, token, err := s.split(data, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(token) != maxLineSize {
		t.Fatalf("token length = %d, want %d", len(token), maxLineSize)
	}
	if advance != maxLineSize {
		t.Fatalf("advance = %d, want %d", advance, maxLineSize)
	}
	if s.lastAdvance != maxLineSize {
		t.Fatalf("lastAdvance = %d, want %d", s.lastAdvance, maxLineSize)
	}
}

// TestLineSplitterIncompleteTailAtEOFWaits is the regression test for the
// splitting bug: a short, newline-less trailing chunk at atEOF must NOT be
// force-emitted as a token. If it were, a line that's still mid-write by
// rsyslog would get cut in half and fail json.Unmarshal in the caller.
func TestLineSplitterIncompleteTailAtEOFWaits(t *testing.T) {
	s := &lineSplitter{}
	data := []byte(`{"hostname":"foo"`) // well short of maxLineSize, no newline
	advance, token, err := s.split(data, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != nil {
		t.Fatalf("token = %q, want nil (should wait for more data)", token)
	}
	if advance != 0 {
		t.Fatalf("advance = %d, want 0", advance)
	}
}

// TestTailingSurvivesSplitWrite reproduces the real-world race: a writer
// (rsyslog) appends a line in two separate writes with the reader (the
// tailer) polling in between - exactly the scenario that used to split one
// well-formed record into two "[MALFORMED JSON]" halves. It exercises the
// same building blocks runIngestionLoop uses (reopen the file, seek to the
// last known-good position, scan with lineSplitter) without pulling in the
// rest of the ingestion pipeline (db/parser/alerts).
func TestDropTrailingIncompleteLine(t *testing.T) {
	// Complete lines only - nothing should be dropped.
	{
		data := []byte(`{"a":1}` + "\n" + `{"b":2}` + "\n")
		res := dropTrailingIncompleteLine(data)
		if len(res) != len(data) {
			t.Fatalf("complete lines: len = %d, want %d", len(res), len(data))
		}
	}

	// Trailing incomplete line - should be stripped.
	{
		data := []byte(`{"a":1}` + "\n" + `{"b":2`)
		res := dropTrailingIncompleteLine(data)
		if len(res) != 8 {
			t.Fatalf("trailing incomplete: len = %d, want 8", len(res))
		}
	}

	// Single incomplete line - should return empty.
	{
		data := []byte(`{"incomplete`)
		res := dropTrailingIncompleteLine(data)
		if len(res) != 0 {
			t.Fatalf("single incomplete: len = %d, want 0", len(res))
		}
	}

	// Empty input.
	{
		data := []byte{}
		res := dropTrailingIncompleteLine(data)
		if len(res) != 0 {
			t.Fatalf("empty: len = %d, want 0", len(res))
		}
	}

	// Only newlines - nothing to drop.
	{
		data := []byte("\n\n\n")
		res := dropTrailingIncompleteLine(data)
		if len(res) != len(data) {
			t.Fatalf("only newlines: len = %d, want %d", len(res), len(data))
		}
	}
}

func TestBackseekLimit(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "backseek-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	content := `{"a":1}` + "\n" + `{"b":2}` + "\n" + `{"c":3}`
	_, err = f.WriteString(content)
	if err != nil {
		t.Fatal(err)
	}
	f.Sync()

	result, hitLimit := safeBackseek(f.Name(), int64(len(content)))
	if hitLimit {
		t.Fatal("should not hit limit for short file")
	}
	if result == 0 {
		t.Fatal("should find newline position")
	}
}

func TestTailingSurvivesSplitWrite(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tailer-race-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()

	full := `{"timestamp":"2026-07-27T10:00:00Z","hostname":"fw1","message":"hello"}` + "\n"
	head := full[:30] // simulate rsyslog mid-write: only part of the line on disk so far

	if _, err := f.WriteString(head); err != nil {
		t.Fatalf("write head: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	filePos := int64(0)

	scanOnce := func() (lines []string, newPos int64) {
		if _, err := f.Seek(filePos, 0); err != nil {
			t.Fatalf("seek: %v", err)
		}
		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, maxLineSize)
		scanner.Buffer(buf, maxLineSize)
		splitter := &lineSplitter{}
		scanner.Split(splitter.split)

		curPos := filePos
		for scanner.Scan() {
			curPos += splitter.lastAdvance
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan error: %v", err)
		}
		return lines, curPos
	}

	// First poll: only the partial head is on disk. Must yield nothing, and
	// must NOT advance filePos past the incomplete bytes.
	lines, newPos := scanOnce()
	if len(lines) != 0 {
		t.Fatalf("first poll: got %d lines, want 0 (incomplete write should be skipped): %q", len(lines), lines)
	}
	filePos = newPos
	if filePos != 0 {
		t.Fatalf("first poll: filePos = %d, want 0 (nothing consumed yet)", filePos)
	}

	// rsyslog finishes the write.
	if _, err := f.WriteString(full[30:]); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Second poll: the complete line should now come back whole, as a
	// single entry - not as two malformed halves.
	lines, newPos = scanOnce()
	if len(lines) != 1 {
		t.Fatalf("second poll: got %d lines, want 1: %q", len(lines), lines)
	}
	want := full[:len(full)-1] // without the trailing newline, same as scanner.Text()
	if lines[0] != want {
		t.Fatalf("second poll: line = %q, want %q", lines[0], want)
	}
	filePos = newPos
	if filePos != int64(len(full)) {
		t.Fatalf("filePos = %d, want %d", filePos, len(full))
	}
}
