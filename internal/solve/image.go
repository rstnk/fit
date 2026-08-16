package solve

import (
	"fmt"

	"github.com/rstnk/fit/internal/config"
)

// MaxRescales is how many times the intermediate is shrunk before giving up.
const MaxRescales = 5

// RescaleFactor is how much the intermediate shrinks per round.
const RescaleFactor = 0.8

// maxProbesPerRound bounds the binary search. Seven encodes covers the whole
// quality range at single-step resolution.
const maxProbesPerRound = 7

// ImageConstraints is the resolved preset, translated for the solver.
type ImageConstraints struct {
	Under   int64
	Quality int
	Floor   int // 0 means QualityFloor
	// Lossy says whether the output format's quality knob actually trades
	// size for perceptual quality. When false, bisecting it searches noise,
	// so the search tries exactly one quality per round and rescales instead.
	Lossy bool
}

// ImageAttempt is one encode the caller should run.
type ImageAttempt struct {
	Quality int     `json:"quality"`
	Scale   float64 `json:"scale"` // cumulative scale of the intermediate
	Round   int     `json:"round"`
}

func (a ImageAttempt) String() string {
	return fmt.Sprintf("quality %d at %.0f%% scale", a.Quality, a.Scale*100)
}

// ImageSearch drives the quality binary search. The caller runs each attempt
// against a scaled intermediate and feeds the resulting size back in, so the
// search itself never touches a file.
type ImageSearch struct {
	cap    int64
	qMax   int
	qFloor int
	lossy  bool

	round      int
	lo, hi     int
	probes     int
	roundBegun bool

	pending  ImageAttempt
	best     ImageAttempt
	haveBest bool
	done     bool
}

// NewImageSearch starts a search. With no cap the search yields one attempt at
// the requested quality.
func NewImageSearch(c ImageConstraints) *ImageSearch {
	floor := c.Floor
	if floor <= 0 {
		floor = QualityFloor
	}
	q := clampInt(c.Quality, 1, 100)
	if floor > q {
		floor = q
	}
	return &ImageSearch{cap: c.Under, qMax: q, qFloor: floor, lossy: c.Lossy}
}

// Next returns the attempt to run, or false when the search is over.
func (s *ImageSearch) Next() (ImageAttempt, bool) {
	if s.done {
		return ImageAttempt{}, false
	}
	if s.cap == 0 {
		s.pending = ImageAttempt{Quality: s.qMax, Scale: 1}
		s.done = true
		return s.pending, true
	}
	if !s.roundBegun {
		s.beginRound()
	}
	return s.pending, true
}

func (s *ImageSearch) beginRound() {
	s.lo, s.hi = s.qFloor, s.qMax
	s.probes = 0
	s.roundBegun = true
	// The first round starts at the requested quality so an image that already
	// fits costs exactly one encode. Later rounds start from the midpoint.
	// Lossless formats have no perceptual quality scale to bisect, so every
	// round stays at qMax and lets the rescale do the work instead.
	q := s.qMax
	if s.lossy && s.round > 0 {
		q = (s.qFloor + s.qMax) / 2
	}
	s.pending = ImageAttempt{Quality: q, Scale: scaleFor(s.round), Round: s.round}
}

// Record feeds back the size the pending attempt produced.
func (s *ImageSearch) Record(size int64) {
	if s.done || s.cap == 0 {
		s.done = true
		return
	}
	if !s.lossy {
		s.recordLossless(size)
		return
	}

	q := s.pending.Quality
	if size <= s.cap {
		s.best, s.haveBest = s.pending, true
		s.lo = q + 1
	} else {
		s.hi = q - 1
	}
	s.probes++

	if s.lo <= s.hi && s.probes < maxProbesPerRound {
		s.pending = ImageAttempt{
			Quality: (s.lo + s.hi) / 2,
			Scale:   scaleFor(s.round),
			Round:   s.round,
		}
		return
	}
	if s.haveBest {
		s.done = true
		return
	}
	// The quality floor still misses the cap, so take pixels away instead.
	s.round++
	if s.round > MaxRescales {
		s.done = true
		return
	}
	s.roundBegun = false
	s.beginRound()
}

// recordLossless handles the format that has no quality knob worth
// searching: one encode per round, and the first one under the cap wins.
func (s *ImageSearch) recordLossless(size int64) {
	if size <= s.cap {
		s.best, s.haveBest = s.pending, true
		s.done = true
		return
	}
	s.round++
	if s.round > MaxRescales {
		s.done = true
		return
	}
	s.roundBegun = false
	s.beginRound()
}

// Best returns the attempt that came in under the cap. For a lossy search
// this is the highest quality that fit: each success raises s.lo past the
// quality that produced it, so a later success in the same round is always a
// higher quality than the one before it, and the final recorded success is
// the best one found.
func (s *ImageSearch) Best() (ImageAttempt, bool) { return s.best, s.haveBest }

func scaleFor(round int) float64 {
	scale := 1.0
	for range round {
		scale *= RescaleFactor
	}
	return scale
}

// MinAudioKbps is the lowest bitrate worth producing. Below it speech is
// artefact and music is unlistenable, so a budget that leaves less than this
// is reported as unreachable rather than encoded.
const MinAudioKbps = 24

// AudioBitrateFor picks an audio bitrate that fits a cap over a duration. It
// never raises the requested bitrate. reserved is space inside the cap already
// spoken for by bytes that are not audio, such as cover art copied through
// whole, and comes off the top before any bitrate is considered.
func AudioBitrateFor(requested int, under, reserved int64, duration float64) (int, error) {
	if under == 0 || duration <= 0 {
		return requested, nil
	}
	room := under - reserved
	if room <= 0 {
		return 0, fmt.Errorf("%s of the %s cap is spent before any audio",
			config.FormatSize(reserved), config.FormatSize(under))
	}
	budget := float64(room) * 8 * Overhead / duration / 1000
	if budget >= float64(requested) {
		return requested, nil
	}
	kbps := int(budget)
	if kbps < MinAudioKbps {
		return 0, fmt.Errorf("at %s over %s the budget leaves %d kbps, below the %d kbps floor",
			config.FormatSize(room), Duration(duration), kbps, MinAudioKbps)
	}
	return kbps, nil
}
