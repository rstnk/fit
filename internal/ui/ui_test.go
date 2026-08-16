package ui

import (
	"bytes"
	"strings"
	"testing"
)

func lineFor(t *testing.T, rec Record) string {
	t.Helper()
	var buf bytes.Buffer
	New(&buf, false, 0).Emit(rec)
	return strings.TrimRight(buf.String(), "\n")
}

// TestLine_FailureNamesTheOutput is the regression test for a refusal reading
// as a plain untruth. "exists and was not made by fit" is about the output, so
// a line carrying only the input's name said photo.png existed when the file in
// the way was photo.jpg.
func TestLine_FailureNamesTheOutput(t *testing.T) {
	got := lineFor(t, Record{
		Input:  "photo.png",
		Output: "photo.jpg",
		Status: StatusFail,
		Note:   "exists and was not made by fit, pass -f to overwrite",
	})
	if !strings.Contains(got, "photo.png → photo.jpg") {
		t.Errorf("line = %q, want both the input and the output it cannot write", got)
	}
}

func TestLine_FailureWithoutAnOutputNamesTheInput(t *testing.T) {
	got := lineFor(t, Record{Input: "notes.txt", Status: StatusFail, Note: "not a media file"})
	if !strings.Contains(got, "notes.txt") || strings.Contains(got, "→") {
		t.Errorf("line = %q, want just the input when no output was ever planned", got)
	}
}

func TestFailed_TracksAnyFailure(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, false, 0)
	r.Emit(Record{Input: "a.jpg", Status: StatusSkip})
	if r.Failed() {
		t.Error("Failed() is true after a skip")
	}
	r.Emit(Record{Input: "b.jpg", Status: StatusFail})
	if !r.Failed() {
		t.Error("Failed() is false after a failure")
	}
}
