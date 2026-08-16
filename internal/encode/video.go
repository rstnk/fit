package encode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/plan"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/solve"
)

// TonemapChain converts HLG or PQ BT.2020 down to BT.709. Without it a naive
// transcode reads BT.2020 as BT.709 and produces washed-out grey.
const TonemapChain = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709," +
	"tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

// LoudnormFilter is EBU R128's target, which is also ffmpeg's own default.
const LoudnormFilter = "loudnorm=I=-24:TP=-2:LRA=11"

// EncodeVideo runs the solved plan, measures the result, and corrects once if
// the finished file missed the cap.
func (e *Encoder) EncodeVideo(ctx context.Context, j Job) (Result, error) {
	t := j.Target
	in := t.Input

	if in.HDR && t.Cons.Tonemap != "off" && in.DoViProfile == 5 {
		return Result{}, fmt.Errorf("Dolby Vision profile 5 is not supported; " +
			"set tonemap = \"off\" to transcode it without conversion")
	}
	if e.wantsTonemap(t.Cons.Tonemap, in) && !e.HasZscale() {
		return Result{}, fmt.Errorf("this input is HDR but ffmpeg has no zscale filter " +
			"(rebuild ffmpeg with libzimg, or set tonemap = \"off\")")
	}

	ws, err := e.workspace(t.Out)
	if err != nil {
		return Result{}, err
	}
	defer ws.close()

	res := Result{Width: j.Video.Width, Height: j.Video.Height}
	tmp := ws.path("out." + t.Spec.Ext)
	bitrate := j.Video.Bitrate

	for attempt := 1; attempt <= 2; attempt++ {
		vp := j.Video
		vp.Bitrate = bitrate
		vp.MaxRate = bitrate * 3 / 2
		vp.BufSize = bitrate * 2

		if vp.Mode == solve.ModeBitrate {
			pass1 := e.videoArgs(j, vp, 1, ws.path("pass"), os.DevNull)
			res.Commands = append(res.Commands, cmdline(e.FFmpeg, pass1))
			if err := e.exec(ctx, e.FFmpeg, pass1...); err != nil {
				return res, err
			}
		}
		final := e.videoArgs(j, vp, 2, ws.path("pass"), tmp)
		res.Commands = append(res.Commands, cmdline(e.FFmpeg, final))
		if err := e.exec(ctx, e.FFmpeg, final...); err != nil {
			return res, err
		}
		if e.DryRun {
			res.Bitrate = vp.Bitrate
			return res, nil
		}

		res.Size = fileSize(tmp)
		res.Bitrate = vp.Bitrate
		if t.Cons.Under == 0 || res.Size <= t.Cons.Under {
			return res, publish(tmp, t.Out)
		}
		if attempt == 2 {
			return res, fmt.Errorf("came out at %s, still over the %s cap after two attempts",
				config.FormatSize(res.Size), config.FormatSize(t.Cons.Under))
		}
		bitrate = solve.RescaleBitrate(vp.Bitrate, t.Cons.Under, res.Size)
		e.Print(fmt.Sprintf("%s came out at %s, retrying at %d kbps",
			t.Input.Path, config.FormatSize(res.Size), bitrate/1000))
	}
	return res, fmt.Errorf("unreachable")
}

func (e *Encoder) wantsTonemap(mode string, in probe.Info) bool {
	switch mode {
	case "off":
		return false
	case "on":
		return true
	default:
		return in.HDR
	}
}

// videoArgs builds one ffmpeg invocation. pass is 1 or 2; pass 1 is only used
// in bitrate mode and writes nothing but statistics.
func (e *Encoder) videoArgs(j Job, vp solve.VideoPlan, pass int, passlog, out string) []string {
	t := j.Target
	in := t.Input
	spec := t.Spec

	a := []string{"-hide_banner", "-nostdin", "-y", "-i", in.Path}

	if t.Cons.Strip == "all" {
		// This removes com.apple.quicktime.location.ISO6709, where iPhone puts
		// GPS. The display matrix is stream side data, so it survives and
		// autorotation still happens.
		a = append(a, "-map_metadata", "-1")
	}

	if vp.Mode == solve.ModeCopy {
		a = append(a, "-c:v", "copy")
	} else {
		if vf := e.filterChain(j, vp); vf != "" {
			a = append(a, "-vf", vf)
		}
		a = append(a, "-c:v", spec.VideoCodec, "-preset", presetFor(spec.VideoCodec),
			"-pix_fmt", "yuv420p")
		// macOS screen recordings are variable frame rate; a constant rate on
		// output is what makes players behave.
		a = append(a, "-fps_mode", "cfr")
		if vp.FPS > 0 {
			a = append(a, "-r", solve.TrimFloat(vp.FPS))
		}
		switch vp.Mode {
		case solve.ModeBitrate:
			a = append(a, "-b:v", strconv.Itoa(vp.Bitrate),
				"-maxrate", strconv.Itoa(vp.MaxRate),
				"-bufsize", strconv.Itoa(vp.BufSize))
			// Every job gets its own pass log, or parallel encodes corrupt
			// each other's statistics.
			a = append(a, "-pass", strconv.Itoa(pass), "-passlogfile", passlog)
		case solve.ModeCRF:
			a = append(a, spec.CRFFlag, strconv.Itoa(vp.CRF))
			if spec.VideoCodec == "libvpx-vp9" {
				a = append(a, "-b:v", "0")
			}
		}
		if e.wantsTonemap(t.Cons.Tonemap, in) {
			a = append(a, "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709")
		}
	}

	if pass == 1 {
		// The null muxer discards everything and needs no container guess;
		// pass 1 only wants the bitrate statistics it writes to passlog.
		a = append(a, "-an", "-f", "null", out)
		return a
	}

	a = append(a, e.audioArgs(j, vp)...)
	a = append(a, "-metadata", "comment="+fingerprint.Marker(j.FP))
	if spec.Container == "mp4" || spec.Container == "mov" || spec.Container == "ipod" {
		a = append(a, "-movflags", "+faststart")
	}
	return append(a, out)
}

// audioArgs reads the codec from t.Spec, not t.Cons: Spec.AudioCodec is what
// AudioCodecOverride already resolved from the preset's audio.codec (or the
// container's own default when the preset named none), so it is the one
// value that is always correct for the container being muxed into.
func (e *Encoder) audioArgs(j Job, vp solve.VideoPlan) []string {
	t := j.Target
	in := t.Input
	if !in.HasAudio {
		return []string{"-an"}
	}
	codec := t.Spec.AudioCodec
	a := []string{"-c:a", codec}
	if codec != "copy" {
		br := vp.AudioBitrate
		if br <= 0 {
			br = t.Cons.AudioBitrate
		}
		a = append(a, "-b:a", strconv.Itoa(br)+"k")
		if t.Cons.AudioMono {
			a = append(a, "-ac", "1")
		}
		if t.Cons.AudioLoudnorm {
			a = append(a, "-af", LoudnormFilter)
		}
	}
	return a
}

func (e *Encoder) filterChain(j Job, vp solve.VideoPlan) string {
	var chain []string
	in := j.Target.Input
	if e.wantsTonemap(j.Target.Cons.Tonemap, in) {
		chain = append(chain, TonemapChain)
	}
	if vp.Width > 0 && vp.Height > 0 && (vp.Width != in.Width || vp.Height != in.Height) {
		chain = append(chain, fmt.Sprintf("scale=%d:%d:flags=lanczos", vp.Width, vp.Height))
	}
	return strings.Join(chain, ",")
}

// EncodeAudio handles audio-only inputs.
func (e *Encoder) EncodeAudio(ctx context.Context, j Job) (Result, error) {
	t := j.Target
	in := t.Input

	ws, err := e.workspace(t.Out)
	if err != nil {
		return Result{}, err
	}
	defer ws.close()
	tmp := ws.path("out." + t.Spec.Ext)

	keepCover := KeepsCover(t)
	// Cover art is copied, not re-encoded, so it lands in the output at its
	// full source size. Measuring it first is what keeps a cap honest: the
	// bitrate has to be chosen out of what is left after the picture.
	var coverBytes int64
	if keepCover && t.Cons.Under > 0 {
		if coverBytes, err = e.coverSize(ctx, in.Path); err != nil {
			return Result{}, err
		}
	}
	bitrate, err := solve.AudioBitrateFor(t.Cons.AudioBitrate, t.Cons.Under, coverBytes, in.Duration)
	if err != nil {
		return Result{}, err
	}

	// Encoders do not deliver the bitrate they are handed. libmp3lame snaps to
	// the nearest MPEG rate, so a request for 89 kbps comes back as 96 and can
	// land over the cap on its own. Measure and correct once, the way the video
	// path does, rather than failing a job a slightly lower rate would fit.
	var res Result
	for attempt := 1; attempt <= 2; attempt++ {
		a := e.audioOnlyArgs(j, keepCover, bitrate, tmp)
		res.Bitrate = bitrate * 1000
		res.Commands = append(res.Commands, cmdline(e.FFmpeg, a))
		if err := e.exec(ctx, e.FFmpeg, a...); err != nil {
			return res, err
		}
		if e.DryRun {
			return res, nil
		}

		res.Size = fileSize(tmp)
		if t.Cons.Under == 0 || res.Size <= t.Cons.Under {
			return res, publish(tmp, t.Out)
		}
		// A lossless codec has no bitrate to give up, so a second attempt would
		// re-encode the identical file and report the same miss more vaguely.
		if !hasBitrateKnob(t.Spec.AudioCodec) {
			return res, fmt.Errorf("came out at %s, over the %s cap, and %s is lossless "+
				"so there is no bitrate to trade",
				config.FormatSize(res.Size), config.FormatSize(t.Cons.Under), t.Spec.AudioCodec)
		}
		if attempt == 2 {
			return res, fmt.Errorf("came out at %s, still over the %s cap after two attempts",
				config.FormatSize(res.Size), config.FormatSize(t.Cons.Under))
		}
		// Rescale against the audio alone. Cover art is a fixed cost that does
		// not shrink with the bitrate, so leaving it in both sides of the ratio
		// would understate how far the audio has to come down.
		next := solve.RescaleBitrate(bitrate*1000, t.Cons.Under-coverBytes, res.Size-coverBytes) / 1000
		if next < solve.MinAudioKbps {
			return res, fmt.Errorf("came out at %s over the %s cap, and fitting it "+
				"would need %d kbps, below the %d kbps floor",
				config.FormatSize(res.Size), config.FormatSize(t.Cons.Under),
				next, solve.MinAudioKbps)
		}
		bitrate = next
		e.Print(fmt.Sprintf("%s came out at %s, retrying at %d kbps",
			in.Path, config.FormatSize(res.Size), bitrate))
	}
	return res, fmt.Errorf("unreachable")
}

// audioOnlyArgs builds one ffmpeg invocation for an audio input. It is split
// out because the cap correction has to build the same command twice at two
// different bitrates.
func (e *Encoder) audioOnlyArgs(j Job, keepCover bool, bitrate int, out string) []string {
	t := j.Target
	a := []string{"-hide_banner", "-nostdin", "-y", "-i", t.Input.Path}
	if keepCover {
		// The disposition is set again on the way out because a copied stream
		// does not carry it across every muxer, and a picture without it is a
		// one-frame video track that players try to play.
		a = append(a, "-map", "0:a:0", "-map", "0:v:0",
			"-c:v", "copy", "-disposition:v:0", "attached_pic")
	} else {
		a = append(a, "-vn")
	}
	if t.Cons.Strip == "all" {
		a = append(a, "-map_metadata", "-1")
	}
	codec := t.Spec.AudioCodec
	a = append(a, "-c:a", codec)
	// Nothing below means anything to a copied stream, and ffmpeg rejects the
	// filters outright rather than ignoring them. Constraints.Validate catches
	// the contradictory preset first; this keeps the builder correct on its own,
	// the way audioArgs on the video path already is.
	if codec != "copy" {
		if hasBitrateKnob(codec) {
			a = append(a, "-b:a", strconv.Itoa(bitrate)+"k")
		}
		if t.Cons.AudioMono {
			a = append(a, "-ac", "1")
		}
		if t.Cons.AudioLoudnorm {
			a = append(a, "-af", LoudnormFilter)
		}
	}
	return append(a, "-metadata", "comment="+fingerprint.Marker(j.FP), out)
}

// hasBitrateKnob reports whether -b:a means anything to a codec. The lossless
// ones reject it, and their size is whatever the source compresses to.
func hasBitrateKnob(codec string) bool {
	return codec != "flac" && codec != "pcm_s16le"
}

// KeepsCover reports whether an audio encode carries the input's embedded
// artwork through. Three things all have to hold: the input has a picture, the
// output container can mux one, and the preset is not stripping metadata,
// since a cover is metadata as much as the artist name is.
func KeepsCover(t *plan.Target) bool {
	return t.Input.CoverWidth > 0 && t.Spec.CoverArt && t.Cons.Strip != "all"
}

// coverSize weighs the embedded picture without writing anything. An attached
// picture is a single packet, so ffprobe reports its size exactly, which beats
// estimating from the dimensions of a JPEG nobody has decoded and leaves the
// figure available under -n, where no temporary file may be produced.
func (e *Encoder) coverSize(ctx context.Context, path string) (int64, error) {
	out, err := exec.CommandContext(ctx, e.FFprobe, "-v", "error",
		"-select_streams", "v:0", "-show_entries", "packet=size",
		"-of", "csv=p=0", "-read_intervals", "%+#1", path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: measuring cover art: %w", err)
	}
	size := strings.TrimSpace(string(out))
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe reported an unreadable cover art size %q", size)
	}
	return n, nil
}

func presetFor(codec string) string {
	if codec == "libsvtav1" {
		return "6"
	}
	return "medium"
}
