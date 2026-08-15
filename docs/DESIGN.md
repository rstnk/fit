# fit

A single-binary Go CLI that gets media files to fit a target. You name a destination, it works out the encoding.

```bash
fit chat screen-recording.mov
fit web shots/*.png
fit mail *.mp4
```

This document is the specification. It states decisions rather than options. Where a decision was contested during design, the reasoning is recorded so it isn't relitigated during implementation.

## Contract

One input produces one output. The tool is told what the output must satisfy, and solves for the encoding parameters that satisfy it. It never guesses at intent from file extensions, never expands globs itself, and never destroys an input.

```
fit <preset> [files...] [overrides]     apply a named preset
fit [files...] --under 8M --width 1080  apply bare constraints, no preset
fit info [files...]                     probe and report
fit ls                                  list presets
fit undo                                move the last run's outputs to the trash
```

## Why this shape

Three design decisions carry the whole tool.

**Dispatch on content, not on a user-supplied category.** There is no `image` / `video` / `audio` level in the command tree. Every input is probed and classified, so `fit chat *` works in a folder holding screen recordings and screenshots together. A category in the grammar is a taxonomy the user has to maintain in their head, and it fails on exactly the invocation people reach for most.

**Named targets, not verbs.** Convert, resize and compress are three answers to one question. A personal tool serves a handful of destinations and hits them for years, so those destinations get names in a config file and everything else is a solver. Ad hoc work uses bare constraint flags.

**The solver lowers resolution to meet a size cap.** A tool that knows the resolution is too high for the budget should reduce it rather than refuse and tell the user to resize first. This is the product.

## Classification

Probe every input with `ffprobe -v error -print_format json -show_format -show_streams`. Classify by this table, checked top to bottom:

Embedded cover art is discounted before any of this runs. A picture attached to a music file is reported as a genuine video stream, in a still codec, with no frame count, which is indistinguishable from a photograph by the table below. A stream is read as artwork when `disposition.attached_pic` is set, and, for the muxers that never set it, when a still-codec stream with `nb_frames` <= 1 sits beside an audio stream. Its dimensions are kept apart from the file's own, so a tagged MP3 never offers its album art as the picture to be resized.

| Condition | Kind |
|---|---|
| ffprobe fails or reports no streams | `unknown`, skip with a note |
| container is gif, apng, or animated webp | `image`, first frame only |
| video stream codec is a still codec (mjpeg, png, bmp, tiff, webp, heif, avif, jpeg2000) and `nb_frames` <= 1 | `image` |
| any video stream present | `video` |
| audio streams only | `audio` |

Multi-frame image sources are read as their first frame, and `fit info` describes that same frame, so what you see is what a run would write.

The image pipeline uses ImageMagick. The video and audio pipelines use ffmpeg. ffprobe is the classifier for all kinds so there is one detection path.

## Presets

TOML at `$XDG_CONFIG_HOME/fit/presets.toml`, defaulting to `~/.config/fit/presets.toml`. Constraints are declared once at the top level of a preset. Per-kind sub-tables override only what differs.

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

### Constraint keys

| Key | Applies to | Meaning |
|---|---|---|
| `under` | all | Hard size cap. The one constraint that is never violated. |
| `width`, `height` | image, video | Ceilings. The solver may go below them, never above. Never upscales. |
| `quality` | image, video | 1 to 100, higher is better. The slack variable when there is no `under`. |
| `format` | all | Output container/codec family. Defaults below. |
| `fps` | video | Ceiling on frame rate. |
| `allow_fps_drop` | video | Lets the solver reduce fps to meet `under`. Default false. |
| `bpp_floor` | video | Overrides the bits-per-pixel threshold. See the solver. |
| `tonemap` | video | `auto` (default), `on`, `off`. |
| `strip` | all | `all` or `none`. Defaults to `all` for images and video, `none` for audio. |
| `name` | all | Output name template. |
| `audio_codec`, `audio_bitrate`, `audio_mono` | video, audio | Audio encoding. |
| `audio_loudnorm` | video, audio | EBU R128 loudness normalisation. |

Per-kind sub-tables are `[preset.image]`, `[preset.video]`, `[preset.audio]`.

**Scope is positional, and nothing is exempt.** A key applies to the kind whose table it is in, and to every kind at the preset level. The `audio_` keys name the audio stream of whichever kind their table selects, so `audio_bitrate` under `[p.video]` is the audio inside a video and under `[p.audio]` it is a music file.

The keys were once a nested `audio` table (`audio.bitrate = 96`) whose encoding keys were hoisted out to every kind regardless of where they appeared. That carried three problems, and flattening the names removed all of them at the cost of one stuttering line in `[p.audio]`:

- Scope ignored position for exactly one family of keys, so `loudnorm` written in `[p.audio]` silently reached video, and "normalise music, leave video alone" could not be expressed at all.
- The same setting had two spellings: `audio.bitrate` in most tables, bare `bitrate` inside `[p.audio]`.
- Writing the dotted form inside `[p.audio]` produced `[p.audio.audio]`, which parsed as a real audio table and quietly took effect.

The old spellings are rejected with an error naming the replacement, since every config predating the change uses them.

### Defaults

Format defaults to `jpeg` for images and `mp4` (H.264 + AAC) for video. AVIF and WebP are available per preset and are the right choice for `web`, which is the one context where the decoder is known. JPEG stays the default everywhere else because the files go to arbitrary places.

Quality defaults to 90. Audio defaults to AAC at 128 kbps, stereo, no loudnorm.

### The default preset

A preset named `default` is applied when the command line names none, following the AWS CLI's `[default]` profile. It is resolved after `-t` and after a preset named in the positional slot, so it only ever fills a gap. An unknown `-t` is still reported by name rather than silently falling back, since running something the user did not ask for is worse than an error.

It is an ordinary preset in every other respect: it can be invoked explicitly, it appears in `fit ls`, and a config that omits it keeps the previous behaviour where naming neither a preset nor a constraint is a usage error.

With a `default` defined, bare constraint flags layer over it rather than over the built-in defaults. AWS has no way out of that; `--no-preset` is the way out here, and it is worth having because the built-in defaults are a real starting point someone may want back without editing their config.

`--no-preset` turns preset resolution off entirely rather than skipping only the default, so no positional is read as a preset name and a file called `chat` stays a file. Combined with `-t` it is a contradiction, and the pair is refused rather than resolved by precedence: a command line that says both "use this preset" and "use no preset" has no reading that is obviously right, and guessing at one silently runs work the user did not ask for.

### Reserved names

`info`, `ls`, `undo`, `cut`, `still`, `help`, `version`, and any name beginning with `-`. A preset using a reserved name is a config load error naming the collision:

```
Error: preset "still" shadows a built-in command (presets.toml:14)
```

`-t, --target <name>` invokes a preset explicitly and bypasses the positional slot entirely, so `fit -t still photo.png` is always unambiguous.

## The solver

`under` is hard. Everything else gives way to it, in a fixed order that is printed whenever the solver fails: quality first, down to a floor, then resolution, then frame rate if the preset opted in.

```
                    ┌──────────────────────────────┐
   probe input ───► │ satisfies every constraint   │──yes──► skip, untouched
                    │ already?                     │
                    └───────────────┬──────────────┘
                                    │ no
                    ┌───────────────▼──────────────┐
                    │ `under` present?             │
                    └───┬───────────────────────┬──┘
                        │ no                    │ yes
                ┌───────▼───────┐      ┌────────▼────────┐
                │ single encode │      │  size solver    │
                │ at `quality`  │      │  (per kind)     │
                └───────────────┘      └────────┬────────┘
                                                │
                                       measure the real file
                                                │
                                    ┌───────────┴──────────┐
                                    │ over cap?            │
                                    └──┬─────────────────┬─┘
                                       │ yes             │ no
                                  retry once,        publish via
                                  then fail          atomic rename
```

### Images

Decode and scale once into a temporary intermediate, then run the search against that intermediate so the expensive decode is paid for a single time.

Without `under`: one encode at `quality`, scaled to fit the `width` and `height` ceilings.

With `under`, for a lossy format: binary search quality over `[floor, quality]` against the cap, roughly seven encodes of an already-scaled image. Quality floor is 40. If the floor still misses the cap, scale the intermediate by 0.8, reset quality to the midpoint, and repeat. Give up after five rescales.

Do the search in `fit` rather than delegating to ImageMagick's `jpeg:extent`, so AVIF, WebP and JPEG all behave identically and the reported achieved quality means the same thing across formats.

PNG and TIFF are lossless: ImageMagick reads `-quality` for them as a zlib compression level and filter type, not a perceptual scale, so size does not fall monotonically with quality and a bisection over it converges on nothing meaningful. For these formats `under` skips the quality search entirely and goes straight to the rescale loop, one encode per round, stopping at the first round that fits.

### Video

Under a hard cap the control variable is bitrate, and the budget fixes it. There is no ladder of quality values to walk. The question is whether the resulting bitrate is enough for the resolution, which is a bits-per-pixel question:

```
budget_bits = under * 8 * 0.95              # 5% held back for container overhead
audio_bits  = audio_bitrate * duration      # 0 when there is no audio track
bitrate     = (budget_bits - audio_bits) / duration
bpp         = bitrate / (width * height * avg_fps)
```

While `bpp` is below the floor, step `width` down the ladder and recompute:

```
3840 → 2560 → 1920 → 1600 → 1280 → 960 → 854 → 720 → 640 → 480
```

Preserve aspect ratio, round both dimensions to even numbers, and start from the first rung at or below the input width. If `allow_fps_drop` is set and the input is above 30 fps, halving the frame rate is tried before the second resolution step, because for screen and game capture it costs less than pixels do.

This is one arithmetic pass with no trial encoding, and it lands on the right resolution before any work happens.

**The bpp floor is content-dependent.** Default 0.05 for H.264 and 0.03 for H.265 and AV1. These are tuned for camera footage. Screen recordings tolerate far less, and applying the camera threshold to a screencast downscales it needlessly, destroying the text legibility that is the entire point of the recording. So `bpp_floor` is overridable per preset, and the default drops to 0.02 when the input is detected as screen capture: no audio track, an unusual `avg_frame_rate`, or a `com.apple.quicktime` screen recording metadata tag.

**Use `avg_frame_rate`, never `r_frame_rate`.** macOS screen recordings are variable frame rate and `r_frame_rate` reports the timebase, frequently 600. Feeding that into the bpp denominator computes an absurdly low value and downscales every screencast into mud. This is the failure that shows up on day one, because screen recordings are the main thing `chat` will ever see. Set `-fps_mode cfr` on output so players behave.

Then encode two-pass:

```
-c:v libx264 -b:v <bitrate> -maxrate <1.5x> -bufsize <2x> -preset medium -pix_fmt yuv420p
```

Two passes because a single-pass bitrate encode spends its budget blind and cannot know the still scene ahead is cheap. `maxrate` and `bufsize` bound local deviation, which matters when the receiving platform enforces its limit on the file rather than on the average.

Measure the finished file. If it came out over the cap, scale the bitrate by `cap / actual * 0.97` and encode once more. After two total attempts, fail rather than publish a file that misses.

**Two-pass writes `ffmpeg2pass-0.log` into the working directory.** Every job must pass `-passlogfile <jobtmpdir>/pass` or parallel encodes corrupt each other's statistics.

Without `under`, video is a single-pass CRF encode. Map quality onto CRF linearly from `[1,100]` to `[51,14]`:

```
crf = 51 - (quality - 1) * 37 / 99
```

| quality | 95 | 90 | 82 | 70 | 50 |
|---|---|---|---|---|---|
| crf | 16 | 18 | 21 | 25 | 33 |

This mapping is calibrated for libx264. It is applied unchanged to libsvtav1 and libvpx-vp9, both of which use a 0-63 CRF scale with a different perceptual curve, so a given `quality` is not the same apparent quality on those codecs as it is on H.264.

### Audio and loudnorm

A preset that sets only audio keys is not a transcode. The video stream is copied through with `-c:v copy` and `-af loudnorm=I=-24:TP=-2:LRA=11` is applied to audio, which is ffmpeg's own default and EBU R128's target. An input with no audio stream is skipped.

Two-pass loudnorm, measuring with `-f null` and feeding the measured values back into a second run, is more accurate than the single-pass filter. Implement single-pass first and treat the second pass as a refinement.

### Audio-only inputs and cover art

Bitrate is the only variable an audio-only encode has, so the budget names it outright rather than searching for it. It is never raised above what the preset asked for, and a budget leaving less than 24 kbps is reported as unreachable instead of encoded.

Cover art is copied with `-c:v copy` wherever the output container accepts it, which is `mp3`, `m4a` and `flac`. Ogg carries artwork inside a comment rather than as a stream and WAV has nowhere to put one, so the `opus` and `wav` muxers reject the copied stream and both drop it. This is a property of the container, so it belongs on `FormatSpec` beside the codec choices rather than being rediscovered at encode time. The disposition is re-asserted on output with `-disposition:v:0 attached_pic`, because a picture that loses the flag becomes a one-frame video track that players try to play.

Copying rather than re-encoding means the picture enters the output at its full source size, and a 3000x3000 cover is easily a megabyte. Under a cap those bytes have to come off the top before a bitrate is chosen, or the solver budgets the whole cap for audio and the finished file lands over it. The size is measured rather than estimated, by copying the single packet out to the workspace and weighing it, which costs a few milliseconds and beats guessing at a JPEG nobody has decoded.

The result is then measured and corrected once, the way the video path is. An encoder does not deliver the bitrate it was handed: libmp3lame snaps to the nearest MPEG rate, so a request for 89 kbps returns 96 and can clear the cap by itself. The correction rescales against the audio alone, since the cover is a fixed cost that does not shrink with the bitrate, and leaving it in both sides of the ratio would understate how far the audio has to come down.

### Failure output

When the solver cannot reach the cap, say what it tried and where it stopped.

```
✕ lecture.mp4 cannot reach 25.0 MiB over 41m 12s
    at 480x270 the budget leaves 81 kbps, below the 0.05 bpp floor
    order of sacrifice: resolution → fps (fps not permitted by preset "mail")
    raise the cap, set allow_fps_drop, or lower bpp_floor
```

Video has no `quality` variable to give up under a cap: the budget fixes the bitrate directly, so the order of sacrifice is resolution then fps only. This message is video-specific; the image failure path (`cannot reach ...: still over after N rescales at quality Q`) is quality-driven from the start and does not use this line.

## Metadata

The handling below is what makes output usable rather than merely small, and it is the part most likely to be skipped and most certain to be missed.

### HDR

iPhone video is HLG or PQ HEVC. Transcoding it naively reads BT.2020 as BT.709 and produces washed-out grey. This is the single most likely first-run disappointment.

Detect HDR when `color_transfer` is `arib-std-b67` or `smpte2084`, or `color_primaries` is `bt2020`. When detected and `tonemap` is not `off`, insert:

```
zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,
tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p
```

and tag the output `-color_primaries bt709 -color_trc bt709 -colorspace bt709`.

This chain needs ffmpeg built with libzimg. Check `ffmpeg -filters` for `zscale` at startup and fail the tonemap path with a clear message rather than silently emitting grey video. Dolby Vision profile 8.4 carries an HLG base layer and tonemaps correctly. Profile 5 does not, and should be reported as unsupported rather than mangled.

### Orientation and stripping

`strip` is `all` or `none`, defaulting to `all` for images and video. There is no `location` middle ground because ImageMagick cannot remove EXIF GPS selectively without rewriting the whole block, and stripping everything achieves the privacy goal more completely.

**Audio defaults to `none` instead.** Stripping is a privacy measure aimed at what a camera writes without being asked, and the reasoning does not transfer: on a music file the artist, album and title are the content rather than a by-product, so the image default would empty the file of the thing it exists to carry. `strip = "all"` still works on audio for anyone who wants the sweep, cover art included.

**Order matters, and getting it wrong is how you produce sideways photos.** Stripping metadata removes the EXIF orientation tag along with the GPS coordinates, so rotation must be baked into pixels first. Images always run `-auto-orient` before `-strip`.

For video, the display matrix is stream side data rather than container metadata, so `-map 0 -c copy` carries it through unchanged. That covers both the solver paths and `cut`'s stream-copy path identically; neither needs to handle rotation by hand.

Video metadata is stripped with `-map_metadata -1`, which removes the `com.apple.quicktime.location.ISO6709` tag where iPhone stores GPS. The display matrix survives this the same way, so autorotation still happens.

## Output naming

Default template `{stem}.{tag}.{ext}`, where `tag` is the preset name. The tag is dropped when the output extension already differs from the input's and no collision results, so `photo.heic` under `web` becomes `photo.avif`, while `clip.mp4` under `chat` becomes `clip.chat.mp4`.

Template variables: `{stem}` `{ext}` `{tag}` `{width}` `{height}` `{hash}` `{date}`. `{hash}` is the first 12 hex characters of the SHA-256 of the input's contents.

Because the name derives from the preset, an output landing on its own input is close to structurally impossible, which is what collapses the usual force/overwrite/hash-filename apparatus into the short safety section below.

Outputs land beside their inputs unless `-o, --out-dir` says otherwise.

## Skip, fingerprints, and force

Every output carries a fingerprint of how it was made, written into the file's own metadata:

- video and audio: `-metadata comment="fit:<hex>"`
- images: `magick -set comment "fit:<hex>"`

The fingerprint is the SHA-256 of the resolved preset (every effective constraint after defaults are applied, canonicalised) combined with the input's size and modification time in nanoseconds.

Reading it back scans 256 KiB at each end of the file, since every format keeps its metadata at one end or the other, and that costs no process. Cover art defeats the assumption: the picture shares the metadata region with the tags and displaces them by its own size, so a FLAC writes the picture block ahead of the comment and MP4 leaves the picture between the comment atom and the end. A megabyte cover puts the marker out of reach of both windows, and since a cover can be arbitrarily large no window setting fixes it. A miss therefore falls back to `ffprobe -show_entries format_tags=comment:stream_tags=comment`, which is the exact answer the scan approximates. Without the fallback fit stops recognising its own output, and re-encoding is the harmless half of that: the overwrite guard reads an unrecognised file as somebody else's and refuses the job outright.

```
output exists?
├─ no ────────────────────────────────► run
└─ yes
   ├─ fingerprint matches ────────────► skip, "already current"
   ├─ fingerprint differs ────────────► run, overwrite via temp + rename
   └─ no fit fingerprint present ─────► refuse, require -f
```

**This exists instead of an mtime comparison because mtime does not know the preset changed.** Edit `chat` from 10 MiB down to 8 MiB, rerun the same glob, and a make-style rule finds every output newer than its input and skips, leaving you holding stale files that miss your new cap. That is a silent wrong answer, which is worse than doing the work again.

**It also exists instead of a database.** State in a personal tool goes stale against moved files and grows a rebuild flag two years in. A fingerprint in the output survives renaming and moving the file, needs no external store, and answers "how was this made" by reading the file itself. There is no run ledger, no `log`, and no `why`. Dry run with `-n` prints the exact command for anything you are about to do.

`undo` reads a single `last-run` file listing the paths written by the most recent invocation and moves them to the system trash. One file, not a database.

## Safety

Plan every output path before running anything. Refuse the whole batch, before any encoding, if two inputs map to one output or if any output path equals any input path. Compare paths as the filesystem sees them, so `Photo.JPG` and `photo.jpg` are one file on macOS.

```
Error: unsafe output paths, nothing was processed
  photo.chat.jpg  ←  photo.png, photo.jpeg
```

Every encode writes to a temporary file beside its destination and is renamed into place only on success, so an interrupted run leaves the destination exactly as it was. That includes forced overwrites: the file being replaced survives intact until there is a complete replacement.

`-f` permits overwriting outputs. It never authorises destroying an input.

There is no time limit on an encode. Ctrl-C cancels the running child process, removes the temporary file, and reports which inputs were never reached.

## Flags

| Flag | Meaning |
|---|---|
| `-t, --target <name>` | Preset, when the positional slot is ambiguous. |
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

Flags override preset values. Bare flags with no preset run the constraints directly.

Sizes parse as `10M`, `10MB`, `10MiB`, all binary. Platform limits are counted in binary units, so that is how they are read, and output always says MiB to be honest about it.

Video defaults to `-j 1` because x264 already saturates available cores, and because two-pass log files and memory pressure make parallel video encodes a poor trade.

## Interface output

One line per input on a terminal, plain lines when stdout is not a TTY:

```
✓ clip.mov → clip.chat.mp4    98.2 MiB → 9.4 MiB   1280x720, 2.1 Mbps
✓ track.flac → track.mp3      30.0 MiB → 2.7 MiB   84 kbps
⊘ already-small.mp4           4.1 MiB, under the 10.0 MiB cap
⊘ shot.chat.jpg               already current
✕ lecture.mp4                 cannot reach 25.0 MiB over 41m 12s
```

Bitrates below 1 Mbps print as kbps. Audio lives between 64 and 320 kbps, where a Mbps figure at one decimal renders every rate as the same `0.1 Mbps` and hides the one number the audio solver moves.

`fit info` reports the same rate per input, in a `BITRATE` column that stays in kbps at every magnitude, since a column exists to be compared down its length and switching units partway puts `1.6` next to `551` with nothing to say which is larger. The figure is the container's own total, falling back to size over duration: per-stream rates would be the more precise number, but ffprobe mostly does not have them, as Matroska stores none at all and FLAC reports none either. Being a whole-file total, it counts cover art and container overhead, which on a 30 MiB FLAC with a megabyte cover overstates the audio by about 4%.

`--json` emits the same information as one NDJSON record per input, including the resolved constraints and the achieved parameters, so batches are scriptable.

Exit codes: 0 when everything succeeded or was skipped, 1 when any input failed, 2 for usage and configuration errors.

## Later commands

Specified here, sequenced after the core in the build plan.

`fit cut <file> <range>` trims by stream copy without re-encoding, with ranges as `00:10-01:30` or `00:10+45s`. The display matrix survives the stream copy on its own, so autorotation still applies without extra handling.

`fit still <file> [@time]` extracts one frame, defaulting to 10% into the duration.

`fit watch <dir> -t <preset>` is deliberately last. A watch daemon needs debounce so it does not grab a file still being copied in, plus a queue and crash recovery, which makes it the most stateful thing in the tool. A shell loop approximates it. Build it only after the rest has earned its keep.

## Implementation notes

Go 1.26+. External binaries: `ffmpeg`, `ffprobe`, ImageMagick 7 (`magick`). Check for all three at startup and name the missing one.

Dependencies stay at one: a TOML parser (`github.com/BurntSushi/toml`). Argument parsing is stdlib `flag` behind a small pre-pass that lifts the leading verb or preset out of `os.Args`, which avoids a CLI framework for a grammar this small. Shell completion for preset names is a hand-written zsh function reading the config, not generated.

```
cmd/fit/main.go
internal/config/       preset loading, resolution, reserved-name validation
internal/probe/        ffprobe wrapper, classification, HDR and VFR detection
internal/plan/         output paths, collision detection, skip decisions
internal/solve/        image and video constraint solvers, no I/O
internal/encode/       magick and ffmpeg command construction and execution
internal/fingerprint/  compute, write, read
internal/ui/           line output, NDJSON, progress
```

Keep `internal/solve` free of process execution so the bpp arithmetic, the resolution ladder and the CRF mapping are unit-testable against table-driven cases without touching a codec. That is where the logic worth testing lives.

Integration tests generate real fixtures with `magick` and `ffmpeg`, including a synthetic HLG clip and a variable frame rate clip, and skip themselves when the binaries are absent.
