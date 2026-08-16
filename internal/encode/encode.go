// Package encode builds and runs the magick and ffmpeg command lines. Every
// encode writes to a temporary file beside its destination and is renamed into
// place only on success, so an interrupted run leaves the destination as it was.
package encode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/rstnk/fit/internal/plan"
	"github.com/rstnk/fit/internal/solve"
)

// Encoder runs the external tools.
type Encoder struct {
	FFmpeg  string
	FFprobe string
	Magick  string

	DryRun  bool
	Verbose bool

	// Print receives dry-run command lines and verbose notes.
	Print func(string)

	zscaleOnce sync.Once
	zscale     bool
}

// New returns an Encoder using the tools from PATH.
func New() *Encoder {
	return &Encoder{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Magick: "magick", Print: func(string) {}}
}

// CheckDeps names whichever external binary is missing.
func (e *Encoder) CheckDeps() error {
	var missing []string
	for _, bin := range []string{e.FFmpeg, e.FFprobe, e.Magick} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required %s: %s (install ffmpeg and ImageMagick 7)",
		pluralise("binary", "binaries", len(missing)), strings.Join(missing, ", "))
}

// HasZscale reports whether ffmpeg was built with libzimg, which the tonemap
// chain needs. Without it the chain would silently emit grey video.
func (e *Encoder) HasZscale() bool {
	e.zscaleOnce.Do(func() {
		out, err := exec.Command(e.FFmpeg, "-hide_banner", "-filters").Output()
		e.zscale = err == nil && strings.Contains(string(out), "zscale")
	})
	return e.zscale
}

// Job is one input on its way to one output.
type Job struct {
	Target *plan.Target
	Video  solve.VideoPlan
	FP     string
}

// Result reports what actually came out. It is read by the caller building a
// ui.Record and never serialised itself, so it carries no struct tags.
type Result struct {
	Size          int64
	Width, Height int
	Bitrate       int
	Quality       int
	Commands      [][]string
}

// workspace is a temporary directory beside the destination, so the final
// rename never crosses a filesystem.
type workspace struct {
	dir string
	dry bool
}

// cmdline records a command with the binary that runs it, so -n and --json
// print something a shell would accept verbatim.
func cmdline(bin string, args []string) []string {
	return append([]string{bin}, args...)
}

// workspace creates the scratch directory, except under -n, which writes
// nothing at all and only needs plausible paths to print.
func (e *Encoder) workspace(out string) (*workspace, error) {
	if e.DryRun {
		return &workspace{dir: filepath.Join(filepath.Dir(out), ".fit-dryrun"), dry: true}, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(out), ".fit-")
	if err != nil {
		return nil, err
	}
	return &workspace{dir: dir}, nil
}

func (w *workspace) path(name string) string { return filepath.Join(w.dir, name) }

func (w *workspace) close() {
	if w != nil && !w.dry {
		os.RemoveAll(w.dir)
	}
}

// publish moves a finished temporary file onto the destination.
func publish(tmp, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// Run executes one ffmpeg invocation, honouring dry-run. It is what the
// stream-copy commands use, since they have nothing to solve.
func (e *Encoder) Run(ctx context.Context, args []string) error {
	return e.exec(ctx, e.FFmpeg, args...)
}

func (e *Encoder) exec(ctx context.Context, name string, args ...string) error {
	line := Quote(append([]string{name}, args...))
	if e.DryRun {
		e.Print(line)
		return nil
	}
	if e.Verbose {
		e.Print(line)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %s", filepath.Base(name), lastMeaningfulLine(stderr.String()))
	}
	return nil
}

// Quote renders a command line the way a shell would accept it, which is what
// -n prints and how the encoder gets reviewed by eye.
func Quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'\\$&|<>()*?[]{}!#;`~") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func lastMeaningfulLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, line := range slices.Backward(lines) {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, "frame=") && !strings.HasPrefix(l, "size=") {
			return l
		}
	}
	return "failed"
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func pluralise(one, many string, n int) string {
	if n == 1 {
		return one
	}
	return many
}
