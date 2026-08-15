// Package solve turns constraints into encoding parameters. Nothing in this
// package executes a process, so the arithmetic can be tested without a codec.
package solve

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/rstnk/fit/internal/config"
)

// Mode is how the encoder should be driven.
type Mode string

const (
	// ModeCRF is a single-pass quality encode, used when there is no cap.
	ModeCRF Mode = "crf"
	// ModeBitrate is a two-pass encode against a bitrate the budget fixes.
	ModeBitrate Mode = "bitrate"
	// ModeCopy leaves the video stream untouched.
	ModeCopy Mode = "copy"
)

// Ladder is the resolution ladder the solver steps down to meet a cap.
var Ladder = []int{3840, 2560, 1920, 1600, 1280, 960, 854, 720, 640, 480}

// Overhead is the fraction of the cap held back for container overhead.
const Overhead = 0.95

// QualityFloor is how far the image search may lower quality.
const QualityFloor = 40

// VideoInput is what the prober found.
type VideoInput struct {
	Width, Height int
	FPS           float64
	Duration      float64
	HasAudio      bool
	ScreenCapture bool
}

// VideoConstraints is the resolved preset, translated for the solver.
type VideoConstraints struct {
	Under        int64
	MaxWidth     int
	MaxHeight    int
	MaxFPS       float64
	Quality      int
	AllowFPSDrop bool
	BppFloor     float64 // 0 means derive from codec and content
	Codec        string  // libx264, libx265, libsvtav1, libvpx-vp9
	AudioBitrate int     // kbps, ignored when the input has no audio
	CopyVideo    bool
}

// VideoPlan is the encoder's marching orders.
type VideoPlan struct {
	Mode         Mode     `json:"mode"`
	Width        int      `json:"width"`
	Height       int      `json:"height"`
	FPS          float64  `json:"fps,omitempty"`
	Bitrate      int      `json:"bitrate,omitempty"` // bits/s, video only
	MaxRate      int      `json:"maxrate,omitempty"`
	BufSize      int      `json:"bufsize,omitempty"`
	CRF          int      `json:"crf,omitempty"`
	AudioBitrate int      `json:"audio_bitrate,omitempty"` // kbps
	BppFloor     float64  `json:"bpp_floor,omitempty"`
	Bpp          float64  `json:"bpp,omitempty"`
	Rescaled     bool     `json:"rescaled,omitempty"`
	FPSHalved    bool     `json:"fps_halved,omitempty"`
	Reasoning    []string `json:"reasoning,omitempty"`
}

// VideoFailure reports a cap the solver could not reach, and what it tried.
type VideoFailure struct {
	Under         int64
	Duration      float64
	Width, Height int
	FPS           float64
	Bitrate       int
	BppFloor      float64
	FPSAllowed    bool
	AudioStarved  bool
}

func (f *VideoFailure) Error() string {
	if f.AudioStarved {
		return fmt.Sprintf("the audio track alone needs more than the cap over %s", Duration(f.Duration))
	}
	return fmt.Sprintf("at %dx%d the budget leaves %d kbps, below the %g bpp floor",
		f.Width, f.Height, f.Bitrate/1000, f.BppFloor)
}

// SolveVideo picks resolution, frame rate and bitrate for one input.
func SolveVideo(in VideoInput, c VideoConstraints) (VideoPlan, error) {
	p := VideoPlan{AudioBitrate: c.AudioBitrate}
	if !in.HasAudio {
		p.AudioBitrate = 0
	}

	if c.CopyVideo {
		p.Mode = ModeCopy
		p.Width, p.Height = in.Width, in.Height
		p.Reasoning = append(p.Reasoning, "preset sets audio keys only, video stream copied")
		return p, nil
	}

	w, h := FitWithin(in.Width, in.Height, c.MaxWidth, c.MaxHeight)
	fps := in.FPS
	if c.MaxFPS > 0 && fps > c.MaxFPS {
		fps = c.MaxFPS
		p.Reasoning = append(p.Reasoning, fmt.Sprintf("frame rate capped at %g", fps))
	}
	if fps <= 0 {
		fps = 30
	}

	if c.Under == 0 {
		p.Mode = ModeCRF
		p.Width, p.Height = w, h
		p.FPS = fps
		p.CRF = CRF(c.Quality)
		p.Reasoning = append(p.Reasoning,
			fmt.Sprintf("no cap, quality %d maps to crf %d", c.Quality, p.CRF))
		return p, nil
	}

	if in.Duration <= 0 {
		return p, fmt.Errorf("input has no duration, cannot budget a bitrate")
	}

	floor := c.BppFloor
	if floor <= 0 {
		floor = DefaultBppFloor(c.Codec, in.ScreenCapture)
		if in.ScreenCapture {
			p.Reasoning = append(p.Reasoning,
				fmt.Sprintf("screen capture detected, bpp floor %g", floor))
		}
	}
	p.BppFloor = floor

	budget := float64(c.Under) * 8 * Overhead
	audioBits := 0.0
	if in.HasAudio {
		audioBits = float64(c.AudioBitrate) * 1000 * in.Duration
	}
	videoBits := budget - audioBits
	if videoBits <= 0 {
		return p, &VideoFailure{Under: c.Under, Duration: in.Duration, BppFloor: floor,
			FPSAllowed: c.AllowFPSDrop, AudioStarved: true}
	}
	bitrate := int(videoBits / in.Duration)

	tried := attempts(w, h, in.Width, in.Height, fps, c.AllowFPSDrop)
	for i, a := range tried {
		bpp := float64(bitrate) / (float64(a.w) * float64(a.h) * a.fps)
		if bpp >= floor {
			p.Mode = ModeBitrate
			p.Width, p.Height = a.w, a.h
			p.FPS = a.fps
			p.Bitrate = bitrate
			p.MaxRate = bitrate * 3 / 2
			p.BufSize = bitrate * 2
			p.Bpp = bpp
			p.Rescaled = i > 0
			p.FPSHalved = a.halved
			p.Reasoning = append(p.Reasoning, fmt.Sprintf(
				"%s cap over %s leaves %d kbps for video; %dx%d at %g fps is %.4f bpp (floor %g)",
				config.FormatSize(c.Under), Duration(in.Duration), bitrate/1000, a.w, a.h, a.fps, bpp, floor))
			return p, nil
		}
	}

	last := tried[len(tried)-1]
	return p, &VideoFailure{
		Under: c.Under, Duration: in.Duration,
		Width: last.w, Height: last.h, FPS: last.fps,
		Bitrate: bitrate, BppFloor: floor, FPSAllowed: c.AllowFPSDrop,
	}
}

type attempt struct {
	w, h   int
	fps    float64
	halved bool
}

// attempts is the order of sacrifice: the starting resolution, one step down
// the ladder, then the frame rate halving when the preset allows it, then the
// rest of the ladder. Halving comes before the second resolution step because
// for screen and game capture it costs less than pixels do.
func attempts(w, h, srcW, srcH int, fps float64, allowFPSDrop bool) []attempt {
	widths := []int{w}
	for _, r := range Ladder {
		if r < w {
			widths = append(widths, r)
		}
	}
	halve := allowFPSDrop && fps > 30
	dropAfter := min(1, len(widths)-1)

	out := make([]attempt, 0, len(widths)+1)
	add := func(cw int, f float64, halved bool) {
		nw, nh := FitWithin(srcW, srcH, cw, 0)
		out = append(out, attempt{nw, nh, f, halved})
	}
	for i, cw := range widths {
		f, halved := fps, false
		if halve && i > dropAfter {
			f, halved = fps/2, true
		}
		add(cw, f, halved)
		if halve && i == dropAfter {
			add(cw, fps/2, true)
		}
	}
	return out
}

// DefaultBppFloor is tuned for camera footage. Screen recordings tolerate far
// less, and applying the camera threshold to a screencast would downscale it
// needlessly and destroy the text legibility that is the point of it.
func DefaultBppFloor(codec string, screenCapture bool) float64 {
	base := 0.05
	switch codec {
	case "libx265", "libsvtav1", "libaom-av1", "libvpx-vp9":
		base = 0.03
	}
	if screenCapture && base > 0.02 {
		base = 0.02
	}
	return base
}

// CRF maps quality 1..100 onto the x264 CRF scale 51..14. This mapping is
// calibrated for libx264 specifically and is applied to every codec the
// uncapped path can target; libsvtav1 and libvpx-vp9 use a 0..63 CRF scale
// with a different perceptual curve, so the same quality number lands at a
// different apparent quality on those codecs.
func CRF(quality int) int {
	q := clampInt(quality, 1, 100)
	return int(math.Round(51 - float64(q-1)*37/99))
}

// fitScale is the shared core of FitWithin and FitWithinExact: how much w x h
// must shrink to fit the ceilings, never growing past 1. A zero ceiling means
// that dimension is unconstrained.
func fitScale(w, h, maxW, maxH int) float64 {
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && h > maxH {
		if s := float64(maxH) / float64(h); s < scale {
			scale = s
		}
	}
	return scale
}

// FitWithin scales w x h down to fit the ceilings, never up, keeping the
// aspect ratio and rounding both sides to even numbers, which video encoders
// require.
func FitWithin(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	scale := fitScale(w, h, maxW, maxH)
	if scale >= 1 {
		return even(w), even(h)
	}
	return even(int(math.Round(float64(w) * scale))), even(int(math.Round(float64(h) * scale)))
}

// FitWithinExact is FitWithin without the even-dimension rounding that video
// encoders need. Stills have no such requirement.
func FitWithinExact(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	scale := fitScale(w, h, maxW, maxH)
	if scale >= 1 {
		return w, h
	}
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	return max(nw, 1), max(nh, 1)
}

func even(n int) int {
	if n < 2 {
		return 2
	}
	return n &^ 1
}

// RescaleBitrate is the correction applied after a finished file misses the
// cap: aim for the cap with 3% of headroom.
func RescaleBitrate(bitrate int, limit, actual int64) int {
	if actual <= 0 {
		return bitrate
	}
	next := max(int(float64(bitrate)*float64(limit)/float64(actual)*0.97), 1000)
	return next
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Duration renders seconds as the "41m 12s" form used in messages.
func Duration(sec float64) string {
	if sec <= 0 {
		return "0s"
	}
	total := int(math.Round(sec))
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// TrimFloat renders a float at up to 3 decimal places, trimming trailing
// zeros. 3 places is enough to keep standard rates like 23.976 and 29.97
// distinct; fewer would round two different rates to the same digits, which
// is why this is the one place in the tree that formats a fractional fps or
// seconds value.
func TrimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(f, 'f', 3, 64), "0"), ".")
}
