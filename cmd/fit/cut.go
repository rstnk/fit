package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rstnk/fit/internal/encode"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/solve"
)

// setupOne builds the encoder and probes path for a subcommand that works on
// a single file, applying the dependency check and kind check that fit cut
// and fit still both need. A non-exitOK return is a fully reported failure;
// the caller should return it immediately without inspecting the other
// values.
func setupOne(ctx context.Context, o options, path string, want ...probe.Kind) (*encode.Encoder, probe.Info, int) {
	enc := encode.New()
	if err := enc.CheckDeps(); err != nil {
		return nil, probe.Info{}, fail(err)
	}
	enc.DryRun = o.dryRun
	enc.Verbose = o.verbose
	enc.Print = func(s string) { fmt.Println(s) }

	in, err := probe.New().Probe(ctx, path)
	if err != nil {
		return nil, probe.Info{}, failRun(err)
	}
	if slices.Contains(want, in.Kind) {
		return enc, in, exitOK
	}
	names := make([]string, len(want))
	for i, k := range want {
		names[i] = string(k)
	}
	return nil, probe.Info{}, failRun(fmt.Errorf("%s is %s, this command works on %s",
		displayName(path), in.Kind, strings.Join(names, " and ")))
}

// runToTemp runs an ffmpeg invocation into a temp file beside out, renaming
// into place only on success. Without this, an interrupted or failed fit cut
// or fit still would leave a truncated file sitting at out, since ffmpeg
// would otherwise be asked to write the destination directly.
func runToTemp(ctx context.Context, enc *encode.Encoder, o options, out string, buildArgs func(dst string) []string) error {
	if o.dryRun {
		return enc.Run(ctx, buildArgs(out))
	}
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fit-*"+filepath.Ext(out))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds
	// os.CreateTemp defaults to 0600. ffmpeg opens the path for writing
	// without resetting its mode, so without this the output would end up
	// more restrictive than every other output fit produces.
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}

	if err := enc.Run(ctx, buildArgs(tmpPath)); err != nil {
		return err
	}
	return os.Rename(tmpPath, out)
}

// cmdCut trims by stream copy, without re-encoding.
func cmdCut(ctx context.Context, o options, args []string) int {
	if len(args) != 2 {
		return fail(fmt.Errorf("cut needs a file and a range, e.g. `fit cut clip.mp4 00:10-01:30`"))
	}
	path, spec := args[0], args[1]

	start, dur, err := parseRange(spec)
	if err != nil {
		return fail(err)
	}

	enc, _, code := setupOne(ctx, o, path, probe.KindVideo, probe.KindAudio)
	if code != exitOK {
		return code
	}

	out := withSuffix(path, "cut", o.outDir)
	if _, err := os.Stat(out); err == nil && !o.force {
		return failRun(fmt.Errorf("%s exists, pass -f to overwrite", displayName(out)))
	}

	buildArgs := func(dst string) []string {
		a := []string{"-hide_banner", "-nostdin", "-y",
			"-ss", trimSeconds(start), "-i", path}
		if dur > 0 {
			a = append(a, "-t", trimSeconds(dur))
		}
		// -map 0 -c copy carries the display matrix side data through
		// untouched, which is what players use for rotation; ffmpeg does not
		// offer a way to write the legacy rotate tag into an mp4/mov output
		// at all, so there is nothing to carry forward by hand.
		a = append(a, "-map", "0", "-c", "copy")
		if strings.HasSuffix(strings.ToLower(dst), ".mp4") || strings.HasSuffix(strings.ToLower(dst), ".mov") {
			a = append(a, "-movflags", "+faststart")
		}
		return append(a, dst)
	}

	if err := runToTemp(ctx, enc, o, out, buildArgs); err != nil {
		return failRun(err)
	}
	if o.dryRun {
		return exitOK
	}
	fmt.Printf("✓ %s → %s\n", displayName(path), displayName(out))
	return exitOK
}

// cmdStill extracts one frame, defaulting to 10% into the duration.
func cmdStill(ctx context.Context, o options, args []string) int {
	if len(args) < 1 || len(args) > 2 {
		return fail(fmt.Errorf("still needs a file and an optional @time, e.g. `fit still clip.mp4 @00:30`"))
	}
	path := args[0]

	enc, in, code := setupOne(ctx, o, path, probe.KindVideo)
	if code != exitOK {
		return code
	}

	at := in.Duration * 0.10
	if len(args) == 2 {
		t := strings.TrimPrefix(args[1], "@")
		var err error
		at, err = parseTime(t)
		if err != nil {
			return fail(err)
		}
	}

	out := withSuffix(replaceExt(path, "jpg"), "still", o.outDir)
	if _, err := os.Stat(out); err == nil && !o.force {
		return failRun(fmt.Errorf("%s exists, pass -f to overwrite", displayName(out)))
	}

	buildArgs := func(dst string) []string {
		return []string{"-hide_banner", "-nostdin", "-y",
			"-ss", trimSeconds(at), "-i", path,
			"-frames:v", "1", "-q:v", "2", dst}
	}

	if err := runToTemp(ctx, enc, o, out, buildArgs); err != nil {
		return failRun(err)
	}
	if o.dryRun {
		return exitOK
	}
	fmt.Printf("✓ %s → %s  at %s\n", displayName(path), displayName(out), trimSeconds(at))
	return exitOK
}

// parseRange reads "00:10-01:30" as a start and end, or "00:10+45s" as a start
// and a duration. It returns the start and the duration in seconds.
func parseRange(s string) (start, dur float64, err error) {
	if before, after, ok := strings.Cut(s, "+"); ok {
		if start, err = parseTime(before); err != nil {
			return 0, 0, err
		}
		dur, err = parseDuration(after)
		return start, dur, err
	}
	// The separator is the first dash that is not part of a leading sign.
	i := strings.Index(s, "-")
	if i <= 0 {
		return 0, 0, fmt.Errorf("range %q must look like 00:10-01:30 or 00:10+45s", s)
	}
	if start, err = parseTime(s[:i]); err != nil {
		return 0, 0, err
	}
	end, err := parseTime(s[i+1:])
	if err != nil {
		return 0, 0, err
	}
	if end <= start {
		return 0, 0, fmt.Errorf("range %q ends before it starts", s)
	}
	return start, end - start, nil
}

// parseTime reads SS, MM:SS or HH:MM:SS, with optional fractional seconds.
func parseTime(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty time")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("time %q has too many parts", s)
	}
	total := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, fmt.Errorf("time %q: %w", s, err)
		}
		total = total*60 + v
	}
	return total, nil
}

// parseDuration reads "45s", "2m" or a bare number of seconds.
func parseDuration(s string) (float64, error) {
	s = strings.TrimSpace(s)
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "ms"):
		s, mult = strings.TrimSuffix(s, "ms"), 0.001
	case strings.HasSuffix(s, "s"):
		s = strings.TrimSuffix(s, "s")
	case strings.HasSuffix(s, "m"):
		s, mult = strings.TrimSuffix(s, "m"), 60
	case strings.HasSuffix(s, "h"):
		s, mult = strings.TrimSuffix(s, "h"), 3600
	}
	v, err := parseTime(s)
	if err != nil {
		return 0, err
	}
	return v * mult, nil
}

func trimSeconds(sec float64) string {
	return solve.TrimFloat(sec)
}

func withSuffix(path, suffix, outDir string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	dir := outDir
	if dir == "" {
		dir = filepath.Dir(path)
	}
	return filepath.Join(dir, stem+"."+suffix+ext)
}

func replaceExt(path, ext string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + "." + ext
}
