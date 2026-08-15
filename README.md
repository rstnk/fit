# fit

fit (ffmpeg + ImageMagick toolkit) is a single-binary Go CLI that gets media files to fit a target. You name a destination, it works out the encoding.

```bash
fit chat screen-recording.mov
fit web shots/*.png
fit mail *.mp4
```

One input produces one output (it never destroys an input). The tool is told what the output must satisfy and solves for the encoding parameters that satisfy it.

[DESIGN.md](docs/DESIGN.md) is the specification. [PLAN.md](docs/PLAN.md) is the order of work.

## Install

```bash
make install
```

That puts `fit` in `~/.local/bin`. External binaries are required and checked at startup:

```bash
make deps
```

`ffmpeg`, `ffprobe` and ImageMagick 7 (`magick`). On macOS:

```bash
brew install ffmpeg imagemagick
```

HDR tonemapping additionally needs an ffmpeg built with libzimg, which `make deps` reports on. Without it, HDR inputs are refused with a message rather than silently transcoded into washed-out grey.

## Usage

```
fit <preset> [files...] [overrides]     apply a named preset
fit [files...]                          apply the [default] preset, if defined
fit [files...] --under 8M --width 1080  apply bare constraints, no preset
fit info [files...]                     probe and report
fit ls                                  list presets
fit undo                                move the last run's outputs to the trash
fit cut <file> <range>                  trim by stream copy, 00:10-01:30 or 00:10+45s
fit still <file> [@time]                extract one frame
```

There is no `image` / `video` / `audio` level in the command tree. Every input is probed and classified, so `fit chat *` works in a folder holding screen recordings and screenshots together.

`fit info` reports what the classifier saw, which is the same view every other command works from:

```
FILE          KIND   SIZE      DIMENSIONS  DURATION  BITRATE  AUDIO  NOTES
album.flac    audio  30.0 MiB  -           2m 39s    1579k    flac   cover 3000x3000
holiday.mkv   video  6.2 MiB   1920x1080   1m 35s    550k     opus   hdr hlg, rotated 90
track.mp3     audio  5.8 MiB   -           2m 32s    321k     mp3    cover 300x300
photo.png     image  1.6 MiB   1920x1080   -         -        -
```

`BITRATE` stays in kbps at every magnitude so rows compare down the column, and is the whole-file rate, cover art included. `NOTES` carries anything worth knowing that has no column of its own: HDR and its transfer curve, Dolby Vision profile, variable frame rate and its timebase, screen capture, rotation, embedded cover art, the `made by fit` fingerprint, and the reason a file could not be read.

### Flags

| Flag | Meaning |
|---|---|
| `-t, --target <name>` | Preset, when the positional slot is ambiguous. |
| `--no-preset` | Ignore presets, including `[default]`. Constraints come from flags and built-in defaults only. |
| `-o, --out-dir <dir>` | Output directory. Default is alongside each input. |
| `-n, --dry-run` | Print the exact `ffmpeg` and `magick` invocations, write nothing. |
| `-f, --force` | Overwrite outputs, including ones fit did not produce. |
| `-j <n>` | Concurrency. Default is core count for images, 1 for video. |
| `--under <size>` | Ad hoc size cap. |
| `--width <n>`, `--height <n>` | Ad hoc dimension ceilings. |
| `-q, --quality <1-100>` | Ad hoc quality. |
| `--format <fmt>` | Ad hoc output format. |
| `--json` | NDJSON, one record per input, on stdout. |
| `-v, --verbose` | Show the solver's reasoning per file. |
| `--config <path>` | Presets file. Default `~/.config/fit/presets.toml`. |

Flags override preset values. Sizes parse as `10M`, `10MB`, `10MiB`, all binary, because the platform caps they describe are binary. Output always says MiB to be honest about it.

Exit codes: 0 when everything succeeded or was skipped, 1 when any input failed, 2 for usage and configuration errors.

## Bare constraints

A preset is not required. Pass constraint flags directly and the same solver runs, filling in anything unnamed from the built-in defaults (quality 90, `jpeg`/`mp4`/`m4a`):

```bash
fit clip.mov --under 8M                      # one constraint: fit under 8 MiB, whatever it costs
fit *.heic --format jpeg --width 2048        # ad hoc conversion, no size cap at all
fit screenshot.png -q 60                     # ad hoc quality alone
fit meeting.mp4 --under 25M --width 1280 -f  # combine several, force overwrite
```

At least one of `--under`, `--width`, `--height`, `-q`/`--quality` or `--format` is required. Neither a preset nor a constraint is a usage error:

```
$ fit clip.mov
Error: no preset and no constraints; try `fit ls` or pass --under
```

`-t, --target <name>` names a preset explicitly, and these same flags still apply on top of it as overrides — the mechanism is identical either way, a preset just supplies the flags' defaults instead of you typing them out.

## Presets

TOML at `$XDG_CONFIG_HOME/fit/presets.toml`, defaulting to `~/.config/fit/presets.toml`, written with a starting set on first run. Constraints are declared once at the top level of a preset; per-kind sub-tables override only what differs.

```toml
[chat]
about = "under Discord's 10 MiB cap"
under = "10MiB"
width = 1280
image.width = 2048          # stills can afford more pixels than motion

[mail]
under = "25MiB"

[web]
image.format = "avif"
width   = 1600
quality = 82
name    = "{stem}@{width}.{ext}"

[voice]
audio_loudnorm = true       # video stream is copied through untouched
```

| Key | Applies to | Meaning |
|---|---|---|
| `under` | all | Hard size cap. The one constraint that is never violated. |
| `width`, `height` | image, video | Ceilings. The solver may go below them, never above. Never upscales. |
| `quality` | image, video | 1 to 100, higher is better. The slack variable when there is no `under`. |
| `format` | all | Output container/codec family. |
| `fps` | video | Ceiling on frame rate. |
| `allow_fps_drop` | video | Lets the solver reduce fps to meet `under`. Default false. |
| `bpp_floor` | video | Overrides the bits-per-pixel threshold. |
| `tonemap` | video | `auto` (default), `on`, `off`. |
| `strip` | all | `all` or `none`. Defaults to `all` for images and video, `none` for audio. |
| `name` | all | Output name template. |
| `audio_codec`, `audio_bitrate`, `audio_mono`, `audio_loudnorm` | video, audio | Audio encoding. |

Per-kind sub-tables are `[preset.image]`, `[preset.video]` and `[preset.audio]`. One rule decides scope: a key applies to the kind whose table it is in, and to every kind when written at the preset level. No key is exempt.

The `audio_` keys name the audio stream of whichever kind their table selects, which is what lets the two be set apart:

```toml
[podcast]
audio_bitrate = 192         # both kinds, unless overridden below

[podcast.video]
audio_bitrate = 96          # the audio inside a video

[podcast.audio]
audio_loudnorm = true       # music files only, video left alone
```

Formats default to `jpeg` for images, `mp4` (H.264 + AAC) for video and `m4a` (AAC) for audio. Quality defaults to 90. Audio defaults to AAC at 128 kbps, stereo, no loudness normalisation, with tags and cover art carried through.

### The default preset

A preset named `default` is applied when the command line names none, the way the AWS CLI falls back to its `[default]` profile:

```bash
fit holiday.mkv              # uses [default] if the config defines one
fit chat holiday.mkv         # [chat], the default is not consulted
fit holiday.mkv -q 60        # [default] with quality overridden to 60
```

It is an ordinary preset, so `fit default holiday.mkv` still works and `fit ls` lists it. A config that does not define one behaves exactly as before: naming no preset and no constraints is a usage error.

Once a `default` exists, ad hoc constraint runs layer on top of it rather than starting from the built-in defaults, so `fit photo.png -q 60` picks up the default preset's `width`, `name` and everything else. `--no-preset` is the way back to a clean slate:

```bash
fit photo.png -q 60 --no-preset   # quality 60 over the built-in defaults, nothing else
```

With `--no-preset` no positional is read as a preset name either, so a file called `chat` stays a file. It contradicts `-t` and the two together are refused rather than one being silently picked.

### Reserved names

`info`, `ls`, `undo`, `cut`, `still`, `help`, `version` and any name beginning with `-` are reserved. A preset using one is a config load error naming the collision and its line. `-t, --target <name>` bypasses the positional slot entirely, so `fit -t still photo.png` is always unambiguous.

## How the solver works

`under` is hard. Everything else gives way to it, in a fixed order that is printed whenever the solver fails: quality first, down to a floor, then resolution, then frame rate if the preset opted in.

**Video** under a cap has one control variable, bitrate, and the budget fixes it. The open question is whether that bitrate is enough for the resolution, which is a bits-per-pixel question:

```
budget_bits = under * 8 * 0.95              # 5% held back for container overhead
audio_bits  = audio_bitrate * duration      # 0 when there is no audio track
bitrate     = (budget_bits - audio_bits) / duration
bpp         = bitrate / (width * height * avg_fps)
```

While `bpp` sits below the floor, width steps down the ladder `3840 → 2560 → 1920 → 1600 → 1280 → 960 → 854 → 720 → 640 → 480` and the arithmetic runs again. One pass, no trial encoding, and it lands on the right resolution before any work happens. Then a two-pass encode, measured, with one bitrate correction if the finished file missed.

The bpp floor is content-dependent: 0.05 for H.264 and 0.03 for H.265 and AV1, dropping to 0.02 when the input looks like a screen capture, because applying the camera threshold to a screencast downscales it needlessly and destroys the text legibility that is the point of the recording.

Frame rate comes from `avg_frame_rate`, never `r_frame_rate`. macOS screen recordings are variable frame rate and `r_frame_rate` reports the timebase, frequently 600, which would compute an absurd bpp and downscale every screencast into mud.

Without a cap, video is a single-pass CRF encode with quality mapped onto CRF as `crf = 51 - (quality - 1) * 37 / 99`. That mapping is calibrated for H.264; AV1 and VP9 use a 0-63 CRF scale with a different perceptual curve, so the same `quality` number is not the same apparent quality across codecs.

**Images** decode and scale once into a temporary intermediate, then binary-search quality over `[40, quality]` against the cap, roughly seven encodes of an already-scaled image. If the quality floor still misses, the intermediate is scaled by 0.8 and the search restarts, up to five times.

**Audio** has bitrate as its only variable, so the budget names it outright, never raising what the preset asked for and refusing below 24 kbps. Cover art complicates the arithmetic, because a copied picture is a fixed cost that does not shrink as the bitrate falls:

```
room     = under - cover_bytes               # measured, by copying the picture out
bitrate  = room * 8 * 0.95 / duration
```

The result is then measured like a video encode, with one correction if it missed. Encoders do not deliver the bitrate they are handed: libmp3lame snaps to the nearest MPEG rate, so a request for 89 kbps returns 96 and can clear the cap on its own.

When the solver cannot reach the cap, it says what it tried and where it stopped:

```
✕ lecture.mp4 cannot reach 25.0 MiB over 41m 12s
    at 480x270 the budget leaves 81 kbps, below the 0.05 bpp floor
    order of sacrifice: resolution → fps (fps not permitted by preset "mail")
    raise the cap, set allow_fps_drop, or lower bpp_floor
```

Video under a cap has no `quality` variable to give up: the budget fixes the bitrate directly, so only resolution and fps are in play. Images go through `quality` first, since their search is quality-driven from the start.

## Metadata

HDR video (HLG or PQ, BT.2020) is detected and tonemapped to BT.709 through a zscale chain, then tagged BT.709. Without that step a naive transcode reads BT.2020 as BT.709 and produces washed-out grey. Dolby Vision profile 8.4 carries an HLG base layer and converts correctly; profile 5 is reported as unsupported rather than mangled.

Images always run `-auto-orient` before `-strip`, because stripping removes the EXIF orientation tag along with the GPS coordinates, and rotation has to be baked into pixels first. Video metadata is stripped with `-map_metadata -1`, which removes the `com.apple.quicktime.location.ISO6709` tag where iPhone stores GPS; the display matrix is stream side data, so it survives and autorotation still happens.

Audio is the exception, and defaults to `strip = "none"`. Stripping exists to drop the incidental metadata a camera leaves behind; on a music file the artist, album and title are the content, so the same default would empty the file of the thing it exists to carry. Tags cross between tag schemes on their own, including non-standard ones: a FLAC's `BARCODE` and `ISRC` Vorbis comments survive into an MP3 as ID3 `TXXX` frames. Setting `strip = "all"` on audio restores the sweep, cover art included.

Embedded cover art is copied through untouched wherever the output container can hold it, which covers `mp3`, `m4a` and `flac`. Ogg keeps artwork inside a comment rather than as a stream and WAV has nowhere to put one, so `opus` and `wav` drop it. Because the picture is copied rather than re-encoded, it enters the output at its full source size, and under an `under` cap its bytes are measured and reserved before any bitrate is chosen.

`fit cut` is the one path that copies streams rather than transcoding; the display matrix survives that copy the same way, so rotation needs no special handling there either.

## Output naming

Default template `{stem}.{tag}.{ext}`, where `tag` is the preset name. The tag is dropped when the output extension already differs from the input's and no collision results, so `photo.heic` under `web` becomes `photo.avif`, while `clip.mp4` under `chat` becomes `clip.chat.mp4`.

Template variables: `{stem}` `{ext}` `{tag}` `{width}` `{height}` `{hash}` `{date}`. `{hash}` is the first 12 hex characters of the SHA-256 of the input's contents.

Outputs land beside their inputs unless `-o, --out-dir` says otherwise.

## Skip, fingerprints and force

Every output carries a fingerprint of how it was made, written into the file's own metadata: a comment for video, audio and JPEG or PNG, an XMP packet for WebP, AVIF and HEIC, whose ImageMagick delegates drop plain comments. The fingerprint is the SHA-256 of the resolved preset combined with the input's size and modification time.

Reading it back scans both ends of the file, where metadata lives, and falls back to `ffprobe` when that misses. The fallback earns its keep on music: a megabyte of cover art shares the metadata region with the tags and pushes them past any fixed window.

```
output exists?
├─ no ────────────────────────────────► run
└─ yes
   ├─ fingerprint matches ────────────► skip, "already current"
   ├─ fingerprint differs ────────────► run, overwrite via temp + rename
   └─ no fit fingerprint present ─────► refuse, require -f
```

This exists instead of an mtime comparison because mtime does not know the preset changed. Edit `chat` from 10 MiB down to 8 MiB, rerun the same glob, and a make-style rule would find every output newer than its input and skip, leaving you holding stale files that miss the new cap.

It also exists instead of a database. A fingerprint in the output survives renaming and moving, needs no external store, and answers "how was this made" by reading the file itself, which `fit info` does.

`fit undo` reads a single `last-run` file listing the paths written by the most recent invocation and moves them to the system trash.

## Safety

Every output path is planned before anything runs. The whole batch is refused, before any encoding, if two inputs map to one output or if any output path equals any input path. Paths are compared as the filesystem sees them, so `Photo.JPG` and `photo.jpg` are one file on macOS.

```
Error: unsafe output paths, nothing was processed
  photo.chat.jpg  ←  photo.png, photo.jpeg
```

Every encode writes to a temporary file beside its destination and is renamed into place only on success, so an interrupted run leaves the destination exactly as it was. That includes forced overwrites. `-f` permits overwriting outputs; it never authorises destroying an input. Ctrl-C cancels the running child process and removes the temporary file.

## Development

```bash
make            # list targets
make build      # bin/fit
make test
make lint
make deps       # check ffmpeg, ffprobe, magick and zscale
make completions
```

Layout:

```
cmd/fit/               argument pre-pass, command dispatch, the run pipeline
internal/config/       preset loading, resolution, reserved-name validation
internal/probe/        ffprobe wrapper, classification, HDR and VFR detection
internal/plan/         output paths, collision detection, skip decisions
internal/solve/        image and video constraint solvers, no I/O
internal/encode/       magick and ffmpeg command construction and execution
internal/fingerprint/  compute, write, read
internal/ui/           line output, NDJSON
```

`internal/solve` never executes a process, so the bpp arithmetic, the resolution ladder and the CRF mapping are covered by table-driven tests that never touch a codec. `internal/config`, `internal/plan` and `internal/encode` have the same kind of coverage; `cmd/fit` itself (argument parsing, the run pipeline, `cut`/`still`/`undo`) does not yet.
