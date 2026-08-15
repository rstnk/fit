package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/encode"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/plan"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/solve"
	"github.com/rstnk/fit/internal/ui"
)

// pending is one input that survived planning and is ready to encode.
type pending struct {
	target    *plan.Target
	fp        string
	record    ui.Record
	videoPlan solve.VideoPlan
}

func cmdRun(ctx context.Context, o options, cfg *config.Config, presetName string, files []string) int {
	enc := encode.New()
	if err := enc.CheckDeps(); err != nil {
		return fail(err)
	}
	enc.DryRun = o.dryRun
	enc.Verbose = o.verbose

	overrides, err := flagOverrides(o)
	if err != nil {
		return fail(err)
	}
	preset, _ := cfg.Get(presetName)
	tag := presetName
	if tag == "" {
		tag = "fit"
	}

	infos := probeAll(ctx, files, o.jobs)

	var targets []*plan.Target
	var jobs []*pending
	var early []ui.Record

	for _, in := range infos {
		rec := ui.Record{Input: displayName(in.Path), Kind: string(in.Kind), InputSize: in.Size}

		if in.Kind == probe.KindUnknown {
			// A path that could not even be opened is a failed input, exit
			// code 1. A path that was read fine but isn't media fit
			// recognises, such as a stray text file swept up by a glob, is a
			// legitimate skip.
			if in.Unreadable {
				rec.Status = ui.StatusFail
			} else {
				rec.Status = ui.StatusSkip
			}
			rec.Note = orDefault(in.Note, "not a media file")
			early = append(early, rec)
			continue
		}

		cons := config.Resolve(preset, in.Kind, overrides)
		spec, err := config.LookupFormat(in.Kind, cons.Format)
		if err != nil {
			rec.Status = ui.StatusFail
			rec.Note = err.Error()
			early = append(early, rec)
			continue
		}
		spec, err = spec.AudioCodecOverride(cons.AudioCodec)
		if err != nil {
			rec.Status = ui.StatusFail
			rec.Note = err.Error()
			early = append(early, rec)
			continue
		}

		if ok, why := plan.Satisfied(in, cons, spec); ok {
			rec.Status = ui.StatusSkip
			rec.Note = why
			early = append(early, rec)
			continue
		}

		t := &plan.Target{Input: in, Cons: cons, Spec: spec, Tag: tag, OutDir: o.outDir}
		p := &pending{target: t, record: rec}

		switch in.Kind {
		case probe.KindVideo:
			vp, err := solve.SolveVideo(videoInput(in), videoConstraints(cons, spec, in))
			if err != nil {
				rec.Status = ui.StatusFail
				rec.Note = solverHeadline(err)
				rec.Detail = solverDetail(err, presetName)
				early = append(early, rec)
				continue
			}
			if o.verbose || o.asJSON {
				p.record.Constraints = &cons
				p.record.Detail = vp.Reasoning
			}
			t.Width, t.Height = vp.Width, vp.Height
			jobs = append(jobs, p)
			targets = append(targets, t)
			p.videoPlan = vp
		case probe.KindImage:
			t.Width, t.Height = solve.FitWithinExact(in.Width, in.Height, cons.Width, cons.Height)
			jobs = append(jobs, p)
			targets = append(targets, t)
		case probe.KindAudio:
			if !in.HasAudio {
				rec.Status = ui.StatusSkip
				rec.Note = "no audio stream"
				early = append(early, rec)
				continue
			}
			jobs = append(jobs, p)
			targets = append(targets, t)
		}
	}

	// Plan every output path before running anything, and refuse the whole
	// batch rather than half-write it.
	if err := plan.Resolve(targets); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitUsage
	}

	width := labelWidth(early, jobs)
	rep := ui.New(os.Stdout, o.asJSON, width)
	enc.Print = rep.Print

	for _, rec := range early {
		rep.Emit(rec)
	}

	var runnable []*pending
	for _, j := range jobs {
		t := j.target
		j.fp = fingerprint.Compute(t.Cons, t.Input.Size, t.Input.ModTime)
		_, statErr := os.Stat(t.Out)
		decision, why := plan.Decide(t.Out, j.fp, statErr == nil, o.force)
		switch decision {
		case plan.SkipCurrent:
			rep.Emit(ui.Record{Input: displayName(t.Input.Path), Kind: string(t.Input.Kind),
				Status: ui.StatusSkip, Output: displayName(t.Out), Note: why})
		case plan.Refuse:
			rep.Emit(ui.Record{Input: displayName(t.Input.Path), Kind: string(t.Input.Kind),
				Status: ui.StatusFail, Output: displayName(t.Out), Note: why})
		default:
			runnable = append(runnable, j)
		}
	}

	written := encodeAll(ctx, enc, rep, o, runnable)

	// Written whenever a real run completes, even an empty list: otherwise a
	// run that skipped everything leaves the previous run's list in place,
	// and a later `fit undo` would trash outputs from two runs ago.
	if !o.dryRun {
		if err := writeLastRun(written); err != nil {
			fmt.Fprintln(os.Stderr, "Warning:", err)
		}
	}
	if ctx.Err() != nil {
		return exitFail
	}
	if rep.Failed() {
		return exitFail
	}
	return exitOK
}

func encodeAll(ctx context.Context, enc *encode.Encoder, rep *ui.Reporter, o options, jobs []*pending) []string {
	// Video defaults to one job because x264 already saturates the cores, and
	// because two-pass logs and memory pressure make parallel video a poor
	// trade. Images run wide.
	imageJobs := o.jobs
	heavyJobs := 1
	if o.jobsSet {
		heavyJobs = o.jobs
	}

	var mu sync.Mutex
	var written []string
	reached := map[string]bool{}

	emit := func(j *pending, res encode.Result, err error) {
		mu.Lock()
		reached[j.target.Input.Path] = true
		mu.Unlock()

		rec := j.record
		rec.Output = displayName(j.target.Out)
		if o.verbose || o.dryRun {
			for _, c := range res.Commands {
				rec.Commands = append(rec.Commands, encode.Quote(c))
			}
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			rec.Status = ui.StatusFail
			rec.Note = err.Error()
			rep.Emit(rec)
			return
		}
		if o.dryRun {
			// Nothing was written, so there is no size or achieved bitrate to
			// report honestly.
			rec.Status = ui.StatusSkip
			rec.Note = "dry run"
			rep.Emit(rec)
			return
		}
		rec.Status = ui.StatusOK
		rec.OutputSize = res.Size
		rec.Width, rec.Height = res.Width, res.Height
		rec.Bitrate = res.Bitrate
		rec.Quality = res.Quality
		rep.Emit(rec)

		mu.Lock()
		written = append(written, j.target.Out)
		mu.Unlock()
	}

	runOne := func(j *pending) {
		job := encode.Job{Target: j.target, Video: j.videoPlan, FP: j.fp}
		var res encode.Result
		var err error
		switch j.target.Input.Kind {
		case probe.KindImage:
			res, err = enc.EncodeImage(ctx, job)
		case probe.KindVideo:
			res, err = enc.EncodeVideo(ctx, job)
		case probe.KindAudio:
			res, err = enc.EncodeAudio(ctx, job)
		}
		emit(j, res, err)
	}

	var images, heavy []*pending
	for _, j := range jobs {
		if j.target.Input.Kind == probe.KindImage {
			images = append(images, j)
		} else {
			heavy = append(heavy, j)
		}
	}

	pool(ctx, images, imageJobs, runOne)
	pool(ctx, heavy, heavyJobs, runOne)

	if ctx.Err() != nil {
		var missed []string
		for _, j := range jobs {
			if !reached[j.target.Input.Path] {
				missed = append(missed, displayName(j.target.Input.Path))
			}
		}
		if len(missed) > 0 {
			fmt.Fprintf(os.Stderr, "\nInterrupted, never reached: %s\n", strings.Join(missed, ", "))
		} else {
			fmt.Fprintln(os.Stderr, "\nInterrupted.")
		}
	}
	return written
}

func pool[T any](ctx context.Context, items []T, workers int, fn func(T)) {
	if len(items) == 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	ch := make(chan T)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for it := range ch {
				// Keep draining rather than returning early: the sender
				// below has its own ctx.Done() escape hatch today, but a
				// worker that stops reading is a latent deadlock for
				// whatever sends into ch next.
				if ctx.Err() != nil {
					continue
				}
				fn(it)
			}
		})
	}
sendLoop:
	for _, it := range items {
		select {
		case ch <- it:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(ch)
	wg.Wait()
}

func probeAll(ctx context.Context, files []string, workers int) []probe.Info {
	pr := probe.New()
	out := make([]probe.Info, len(files))
	type item struct {
		i    int
		path string
	}
	items := make([]item, len(files))
	for i, f := range files {
		items[i] = item{i, f}
	}
	pool(ctx, items, workers, func(it item) {
		// Probe already fills in Note (and Unreadable, for a path that
		// couldn't be opened at all) on every error path, so its result
		// stands on its own without reconstructing it here.
		info, _ := pr.Probe(ctx, it.path)
		out[it.i] = info
	})
	return out
}

func videoInput(in probe.Info) solve.VideoInput {
	return solve.VideoInput{
		Width: in.Width, Height: in.Height,
		FPS:           in.AvgFPS,
		Duration:      in.Duration,
		HasAudio:      in.HasAudio,
		ScreenCapture: in.ScreenCapture,
	}
}

func videoConstraints(c config.Constraints, spec config.FormatSpec, in probe.Info) solve.VideoConstraints {
	audioBitrate := c.AudioBitrate
	if !in.HasAudio {
		audioBitrate = 0
	}
	return solve.VideoConstraints{
		Under:        c.Under,
		MaxWidth:     c.Width,
		MaxHeight:    c.Height,
		MaxFPS:       c.FPS,
		Quality:      c.Quality,
		AllowFPSDrop: c.AllowFPSDrop,
		BppFloor:     c.BppFloor,
		Codec:        spec.VideoCodec,
		AudioBitrate: audioBitrate,
		CopyVideo:    c.CopyVideo,
	}
}

func solverHeadline(err error) string {
	if vf, ok := errors.AsType[*solve.VideoFailure](err); ok {
		return fmt.Sprintf("cannot reach %s over %s",
			config.FormatSize(vf.Under), solve.Duration(vf.Duration))
	}
	return err.Error()
}

// solverDetail says what the solver tried and where it stopped.
func solverDetail(err error, presetName string) []string {
	var vf *solve.VideoFailure
	if !errors.As(err, &vf) {
		return nil
	}
	fps := "fps"
	if !vf.FPSAllowed {
		where := "the constraints"
		if presetName != "" {
			where = fmt.Sprintf("preset %q", presetName)
		}
		fps = fmt.Sprintf("fps (fps not permitted by %s)", where)
	}
	return []string{
		vf.Error(),
		// Video under a cap has no quality variable to sacrifice: the budget
		// fixes the bitrate, and the solver only ever gives up resolution and
		// then fps. That is unlike the image path, which trades quality first.
		"order of sacrifice: resolution → " + fps,
		"raise the cap, set allow_fps_drop, or lower bpp_floor",
	}
}

func flagOverrides(o options) (config.Set, error) {
	var s config.Set
	if o.under != "" {
		n, err := config.ParseSize(o.under)
		if err != nil {
			return s, err
		}
		s.Under = &n
	}
	if o.width > 0 {
		s.Width = &o.width
	}
	if o.height > 0 {
		s.Height = &o.height
	}
	if o.qualitySet {
		if o.quality < 1 || o.quality > 100 {
			return s, fmt.Errorf("quality must be between 1 and 100, got %d", o.quality)
		}
		s.Quality = &o.quality
	}
	if o.format != "" {
		s.Format = &o.format
	}
	return s, nil
}

func labelWidth(early []ui.Record, jobs []*pending) int {
	w := 0
	for _, r := range early {
		w = max(w, len([]rune(r.Input)))
	}
	for _, j := range jobs {
		label := displayName(j.target.Input.Path) + " → " + displayName(j.target.Out)
		w = max(w, len([]rune(label)))
	}
	return min(w, 72)
}

func displayName(p string) string {
	if filepath.Dir(p) == "." {
		return filepath.Base(p)
	}
	return p
}

func orDefault(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
