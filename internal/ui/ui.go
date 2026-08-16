// Package ui writes one line per input, or NDJSON when a batch is being
// scripted.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/rstnk/fit/internal/config"
)

// Status is the outcome for one input.
type Status string

const (
	StatusOK   Status = "ok"
	StatusSkip Status = "skip"
	StatusFail Status = "fail"
)

// Symbols are plain text rather than emoji so they land the same way in every
// terminal and in a pipe.
const (
	symOK   = "✓"
	symSkip = "⊘"
	symFail = "✕"
)

// Record is one input's outcome, and the unit of NDJSON output.
type Record struct {
	Input  string `json:"input"`
	Kind   string `json:"kind"`
	Status Status `json:"status"`
	Output string `json:"output,omitempty"`

	InputSize  int64 `json:"input_size,omitempty"`
	OutputSize int64 `json:"output_size,omitempty"`

	Width   int `json:"width,omitempty"`
	Height  int `json:"height,omitempty"`
	Bitrate int `json:"bitrate,omitempty"`
	Quality int `json:"quality,omitempty"`

	Note   string   `json:"note,omitempty"`
	Detail []string `json:"detail,omitempty"`

	Constraints *config.Constraints `json:"constraints,omitempty"`
	Commands    []string            `json:"commands,omitempty"`
}

// Reporter serialises output from concurrent workers.
type Reporter struct {
	out       io.Writer
	json      bool
	tty       bool
	nameWidth int
	mu        sync.Mutex
	failed    bool
}

// New returns a Reporter. width is the widest input label in the batch, used
// to line the columns up.
func New(out io.Writer, asJSON bool, nameWidth int) *Reporter {
	f, isFile := out.(*os.File)
	tty := isFile && wantsColour(f)
	return &Reporter{out: out, json: asJSON, tty: tty, nameWidth: nameWidth}
}

// Failed reports whether any input failed.
func (r *Reporter) Failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

// Emit writes one record.
func (r *Reporter) Emit(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.Status == StatusFail {
		r.failed = true
	}
	if r.json {
		enc := json.NewEncoder(r.out)
		_ = enc.Encode(rec)
		return
	}
	fmt.Fprintln(r.out, r.line(rec))
	for _, d := range rec.Detail {
		fmt.Fprintf(r.out, "    %s\n", d)
	}
}

// Print writes a free-form line, for dry runs and verbose notes.
func (r *Reporter) Print(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json {
		return
	}
	fmt.Fprintln(r.out, s)
}

func (r *Reporter) line(rec Record) string {
	label := rec.Input
	sym := symSkip
	switch rec.Status {
	case StatusOK:
		sym = symOK
		label = rec.Input + " → " + rec.Output
	case StatusFail:
		sym = symFail
		// The note on a failure is often about the output, not the input:
		// "exists and was not made by fit" reads as a plain untruth against an
		// input's name when it is the output that is in the way.
		if rec.Output != "" {
			label = rec.Input + " → " + rec.Output
		}
	case StatusSkip:
		switch {
		case rec.Note == "already current":
			label = rec.Output
		case rec.Output != "":
			label = rec.Input + " → " + rec.Output
		}
	}

	var right string
	switch rec.Status {
	case StatusOK:
		right = fmt.Sprintf("%s → %s", config.FormatSize(rec.InputSize), config.FormatSize(rec.OutputSize))
		var extra []string
		if rec.Width > 0 && rec.Height > 0 {
			extra = append(extra, fmt.Sprintf("%dx%d", rec.Width, rec.Height))
		}
		if rec.Bitrate > 0 {
			extra = append(extra, config.FormatBitrate(rec.Bitrate))
		} else if rec.Quality > 0 {
			extra = append(extra, fmt.Sprintf("q%d", rec.Quality))
		}
		// fit's whole purpose is making files smaller; an output that grew is
		// worth a flag rather than passing as an unremarkable success.
		if rec.OutputSize > rec.InputSize {
			extra = append(extra, "grew")
		}
		if len(extra) > 0 {
			right += "   " + strings.Join(extra, ", ")
		}
	default:
		right = rec.Note
	}

	width := max(r.nameWidth, 0)
	return fmt.Sprintf("%s %s %s", r.colour(sym, rec.Status), pad(label, width), right)
}

func (r *Reporter) colour(s string, st Status) string {
	if !r.tty {
		return s
	}
	switch st {
	case StatusOK:
		return "\x1b[32m" + s + "\x1b[0m"
	case StatusFail:
		return "\x1b[31m" + s + "\x1b[0m"
	default:
		return "\x1b[2m" + s + "\x1b[0m"
	}
}

func pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}
