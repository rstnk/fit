# fit: build plan

Ordered milestones for implementing [DESIGN.md](DESIGN.md). Each milestone ends with something runnable and tested. Land them one at a time.

The design doc is the authority on behaviour. This file is only the order of work.

## 1. Skeleton and probing

Module setup, dependency check for `ffmpeg`, `ffprobe` and `magick` at startup with a message naming whichever is missing.

`internal/probe` wrapping `ffprobe -v error -print_format json -show_format -show_streams`, with the classification table from the design doc returning `image` / `video` / `audio` / `unknown`. Extract duration, dimensions, `avg_frame_rate`, audio presence, color transfer and primaries, and the screen-capture heuristic.

`fit info [files...]` printing an aligned table across mixed kinds, plus `--json`.

Tests: table-driven classification against generated fixtures, including an animated GIF, a single-frame PNG, an audio-only m4a, and a text file that is not media at all. Assert `avg_frame_rate` is what gets read, and that a VFR fixture does not report its timebase.

## 2. Config

`internal/config`: TOML loading from `$XDG_CONFIG_HOME/fit/presets.toml`, per-kind sub-table merging, defaults, and reserved-name validation with the file and line in the error.

Preset resolution produces one flat struct of effective constraints for a given kind, which is the same value the fingerprint later hashes. Get that struct right here, everything downstream depends on its shape.

`fit ls`. A set of built-in presets shipped in the binary and written out on first run: `chat`, `mail`, `web`, `voice`.

Tests: merge precedence, reserved-name rejection, flag-over-preset override.

## 3. The solvers

`internal/solve`, with no process execution anywhere in the package.

Video: the bpp arithmetic, the resolution ladder, the screen-capture floor, the fps drop when permitted, the CRF mapping for the uncapped path, and the bitrate rescale after a measurement miss.

Image: the quality binary search bounds and the 0.8 rescale loop, expressed as a sequence of attempts a caller executes.

Tests are the point of this milestone. Table-driven cases covering: a short clip that fits at full resolution, a long lecture that cannot fit at any rung, a 60 fps capture with and without `allow_fps_drop`, a screen recording that must not be downscaled under the lower floor, a file already under its cap, and a silent input where the audio budget is zero. Verify every CRF mapping row in the design doc.

## 4. Planning and safety

`internal/plan`: name templates, tag-dropping rule, collision detection across the batch, output-equals-input detection, case-insensitive path comparison.

Refuse the whole batch before any work with the exact error format from the design doc.

Tests: two inputs colliding on one output, an output landing on an input, `Photo.JPG` against `photo.jpg`.

## 5. Encoding

`internal/encode`: command construction for magick and ffmpeg, temp file beside destination, atomic rename on success, child process cancellation and temp cleanup on Ctrl-C.

Two-pass video with a per-job `-passlogfile` under the job's temp directory. This is required from the first commit, not added later when `-j` is raised.

`-n` printing the exact constructed commands, which is also how the encoder gets reviewed by eye.

The image path decodes and scales once into a temporary intermediate before the quality search runs against it.

Milestone ends with `fit chat clip.mp4` producing a correctly sized file.

## 6. Metadata correctness

The part most likely to be deferred and most certain to be missed.

HDR detection and the tonemap chain, gated on a `zscale` capability check against `ffmpeg -filters`, failing loudly rather than emitting grey video. Dolby Vision profile 5 reported as unsupported.

`-auto-orient` before `-strip` on images, so rotation is baked into pixels before the EXIF orientation tag is discarded. `-map_metadata -1` on video.

`-fps_mode cfr` on output.

Tests: a synthetic HLG fixture that comes out with plausible average luma rather than washed out, an image with a rotation EXIF tag that comes out with swapped dimensions and no metadata, and a video fixture carrying a location tag that comes out without one.

## 7. Fingerprints and skip

`internal/fingerprint`: compute from the resolved preset plus input size and mtime, write into output metadata, read back.

The four-way skip decision from the design doc, including refusing an existing output that carries no fit fingerprint until `-f`.

`undo` writing and reading a single `last-run` file, moving outputs to the system trash.

Tests: editing a preset's cap makes a previously-current output stale, moving and renaming an output keeps it current, a hand-made file at an output path is refused.

## 8. Interface

`internal/ui`: the per-line output format, TTY detection, NDJSON, `-v` solver reasoning, exit codes 0/1/2.

Concurrency with `-j`, defaulting to core count for images and 1 for video.

Hand-written zsh completion reading preset names from the config.

## 9. Later

`fit cut`, carrying rotation metadata forward across the stream copy.

`fit still`.

`fit watch` last, and only if the rest has earned it. Needs debounce against files still being copied in, a queue, and crash recovery, which makes it the most stateful thing in the tool for a job a shell loop approximates.

## Standing constraints

Dependencies stay at one, the TOML parser. Argument parsing is stdlib `flag` behind a pre-pass that lifts the leading verb or preset out of `os.Args`.

`internal/solve` never executes a process. If a test there needs a codec, the boundary is in the wrong place.

Integration tests generate real fixtures with `magick` and `ffmpeg` and skip themselves when those binaries are missing.
