// Command fit gets media files to fit a target. You name a destination, it
// works out the encoding.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/rstnk/fit/internal/config"
)

// Version is stamped at build time.
var Version = "dev"

// Exit codes: 0 when everything succeeded or was skipped, 1 when any input
// failed, 2 for usage and configuration errors.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

type options struct {
	target   string
	outDir   string
	dryRun   bool
	force    bool
	jobs     int
	jobsSet  bool
	asJSON   bool
	verbose  bool
	confPath string
	noPreset bool

	under      string
	width      int
	height     int
	quality    int
	qualitySet bool
	format     string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		usage(os.Stdout)
		return exitUsage
	}

	flagArgs, pos, err := splitArgs(argv)
	if err != nil {
		return fail(err)
	}

	var o options
	fs := flag.NewFlagSet("fit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(os.Stderr) }

	fs.StringVar(&o.target, "t", "", "preset name")
	fs.StringVar(&o.target, "target", "", "preset name")
	fs.StringVar(&o.outDir, "o", "", "output directory")
	fs.StringVar(&o.outDir, "out-dir", "", "output directory")
	fs.BoolVar(&o.dryRun, "n", false, "print commands, write nothing")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print commands, write nothing")
	fs.BoolVar(&o.force, "f", false, "overwrite outputs")
	fs.BoolVar(&o.force, "force", false, "overwrite outputs")
	fs.IntVar(&o.jobs, "j", 0, "concurrency")
	fs.BoolVar(&o.asJSON, "json", false, "NDJSON on stdout")
	fs.BoolVar(&o.verbose, "v", false, "show the solver's reasoning")
	fs.BoolVar(&o.verbose, "verbose", false, "show the solver's reasoning")
	fs.StringVar(&o.confPath, "config", "", "presets file")
	fs.BoolVar(&o.noPreset, "no-preset", false, "ignore presets, including the default")
	fs.StringVar(&o.under, "under", "", "size cap, e.g. 8M")
	fs.IntVar(&o.width, "width", 0, "width ceiling")
	fs.IntVar(&o.height, "height", 0, "height ceiling")
	fs.IntVar(&o.quality, "q", 0, "quality 1-100")
	fs.IntVar(&o.quality, "quality", 0, "quality 1-100")
	fs.StringVar(&o.format, "format", "", "output format")

	if err := fs.Parse(flagArgs); err != nil {
		return exitUsage
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "j":
			o.jobsSet = true
		case "q", "quality":
			o.qualitySet = true
		}
	})
	if o.jobs <= 0 {
		o.jobs = runtime.NumCPU()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	verb := ""
	if len(pos) > 0 {
		verb = pos[0]
	}

	switch verb {
	case "help":
		usage(os.Stdout)
		return exitOK
	case "version":
		fmt.Println("fit", Version)
		return exitOK
	case "ls":
		return cmdLs(o)
	case "info":
		return cmdInfo(ctx, o, pos[1:])
	case "undo":
		return cmdUndo(o)
	case "cut":
		return cmdCut(ctx, o, pos[1:])
	case "still":
		return cmdStill(ctx, o, pos[1:])
	}

	cfg, err := config.Load(o.confPath)
	if err != nil {
		return fail(err)
	}

	preset, files, err := resolvePreset(cfg, o.target, o.noPreset, pos)
	if err != nil {
		return fail(err)
	}
	if preset != "" {
		if _, ok := cfg.Get(preset); !ok {
			return fail(fmt.Errorf("unknown preset %q, try one of: %s",
				preset, strings.Join(cfg.Names(), ", ")))
		}
	}
	if len(files) == 0 {
		usage(os.Stderr)
		return exitUsage
	}
	if preset == "" && o.under == "" && o.width == 0 && o.height == 0 &&
		o.quality == 0 && o.format == "" {
		return fail(fmt.Errorf("no preset and no constraints; try `fit ls` or pass --under"))
	}

	return cmdRun(ctx, o, cfg, preset, files)
}

// resolvePreset decides which preset a run uses and which positionals are
// files. An explicit -t wins, then a preset named in the first positional
// slot, then one called "default" if the config defines it.
//
// The default is applied the way an AWS profile is: it is not a separate mode
// but the preset you get when you name none, and constraint flags still layer
// over it as overrides. A config without a "default" preset behaves exactly as
// before, so this only ever adds a fallback where one was written down.
//
// noPreset turns all of that off and takes every positional as a file, which
// is the only way back to the built-in defaults once a default preset exists.
func resolvePreset(cfg *config.Config, target string, noPreset bool, pos []string) (string, []string, error) {
	if noPreset {
		if target != "" {
			return "", nil, fmt.Errorf("--no-preset contradicts -t %s, pick one", target)
		}
		return "", pos, nil
	}
	if target != "" {
		return target, pos, nil
	}
	if len(pos) > 0 {
		if _, ok := cfg.Get(pos[0]); ok {
			return pos[0], pos[1:], nil
		}
	}
	if _, ok := cfg.Get(config.DefaultPreset); ok {
		return config.DefaultPreset, pos, nil
	}
	return "", pos, nil
}

// fail is for usage and configuration mistakes: a bad flag, an unknown
// preset, a malformed presets file. The command line itself was the problem.
func fail(err error) int {
	fmt.Fprintln(os.Stderr, "Error:", err)
	return exitUsage
}

// failRun is for runtime failures: a missing input, a probe or encode that
// errored, a filesystem operation that failed mid-run. The command line was
// fine; carrying it out was not.
func failRun(err error) int {
	fmt.Fprintln(os.Stderr, "Error:", err)
	return exitFail
}

func usage(w *os.File) {
	fmt.Fprint(w, `fit — get media files to fit a target

  fit <preset> [files...] [overrides]   apply a named preset
  fit [files...]                        apply the [default] preset, if defined
  fit [files...] --under 8M --width 1080  apply bare constraints
  fit info [files...]                   probe and report
  fit ls                                list presets
  fit undo                              move the last run's outputs to the trash
  fit cut <file> <range>                trim by stream copy, e.g. 00:10-01:30
  fit still <file> [@time]              extract one frame

Flags
  -t, --target <name>   preset, when the positional slot is ambiguous
      --no-preset       ignore presets, including [default]
  -o, --out-dir <dir>   output directory, default is alongside each input
  -n, --dry-run         print the exact ffmpeg and magick invocations
  -f, --force           overwrite outputs, including ones fit did not produce
  -j <n>                concurrency, default core count for images, 1 for video
      --under <size>    size cap, read as binary units: 10M, 10MB, 10MiB
      --width <n>       width ceiling
      --height <n>      height ceiling
  -q, --quality <1-100> quality
      --format <fmt>    output format
      --json            NDJSON, one record per input
  -v, --verbose         show the solver's reasoning per file
      --config <path>   presets file, default `+config.DefaultPath()+`
`)
}
