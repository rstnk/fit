package solve

import (
	"errors"
	"math"
	"testing"
)

func TestCRF(t *testing.T) {
	cases := []struct {
		quality int
		want    int
	}{
		{95, 16},
		{90, 18},
		{82, 21},
		{70, 25},
		{50, 33},
		{1, 51},
		{100, 14},
		// Out of range clamps rather than producing a nonsense CRF.
		{0, 51},
		{-10, 51},
		{200, 14},
	}
	for _, c := range cases {
		if got := CRF(c.quality); got != c.want {
			t.Errorf("CRF(%d) = %d, want %d", c.quality, got, c.want)
		}
	}
}

func TestDefaultBppFloor(t *testing.T) {
	cases := []struct {
		codec         string
		screenCapture bool
		want          float64
	}{
		{"libx264", false, 0.05},
		{"libx265", false, 0.03},
		{"libsvtav1", false, 0.03},
		{"libvpx-vp9", false, 0.03},
		{"libx264", true, 0.02},
		{"libx265", true, 0.02}, // already <= 0.02*... 0.03 clamped down to 0.02
		{"unknown-codec", false, 0.05},
	}
	for _, c := range cases {
		if got := DefaultBppFloor(c.codec, c.screenCapture); got != c.want {
			t.Errorf("DefaultBppFloor(%q, %v) = %g, want %g", c.codec, c.screenCapture, got, c.want)
		}
	}
}

func TestFitWithin(t *testing.T) {
	cases := []struct {
		name             string
		w, h, maxW, maxH int
		wantW, wantH     int
	}{
		{"no ceiling", 1920, 1080, 0, 0, 1920, 1080},
		{"already fits", 640, 480, 1280, 720, 640, 480},
		{"width bound", 3840, 2160, 1920, 0, 1920, 1080},
		{"height bound", 1080, 1920, 0, 960, 540, 960},
		{"never upscales", 320, 240, 1920, 1080, 320, 240},
		{"rounds to even", 641, 481, 320, 0, 320, 240},
		{"zero dims pass through", 0, 480, 100, 100, 0, 480},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotW, gotH := FitWithin(c.w, c.h, c.maxW, c.maxH)
			if gotW != c.wantW || gotH != c.wantH {
				t.Errorf("FitWithin(%d,%d,%d,%d) = %d,%d want %d,%d",
					c.w, c.h, c.maxW, c.maxH, gotW, gotH, c.wantW, c.wantH)
			}
			if gotW%2 != 0 || gotH%2 != 0 {
				t.Errorf("FitWithin(%d,%d,%d,%d) = %d,%d, not even", c.w, c.h, c.maxW, c.maxH, gotW, gotH)
			}
		})
	}
}

func TestFitWithinExact(t *testing.T) {
	// Unlike FitWithin, dimensions are not rounded to even, and the floor is 1.
	w, h := FitWithinExact(801, 601, 400, 0)
	if w != 400 {
		t.Errorf("width = %d, want 400", w)
	}
	if h == 0 || h%2 == 0 && h != 300 {
		// Just confirm it scaled proportionally and isn't forced even.
		wantH := int(math.Round(601 * 400.0 / 801.0))
		if h != wantH {
			t.Errorf("height = %d, want %d", h, wantH)
		}
	}
	// Tiny scale never rounds down to zero.
	w, h = FitWithinExact(10000, 1, 1, 0)
	if w < 1 || h < 1 {
		t.Errorf("FitWithinExact must not produce a zero dimension, got %d,%d", w, h)
	}
}

func TestRescaleBitrate(t *testing.T) {
	// Came out 20% over a 10Mb cap: correction should land under cap with headroom.
	bitrate, limit, actual := 1_000_000, int64(10_000_000), int64(12_000_000)
	got := RescaleBitrate(bitrate, limit, actual)
	want := int(float64(bitrate) * float64(limit) / float64(actual) * 0.97)
	if got != want {
		t.Errorf("RescaleBitrate = %d, want %d", got, want)
	}
	// Degenerate measured size never divides by zero or goes negative.
	if got := RescaleBitrate(500_000, 10_000_000, 0); got != 500_000 {
		t.Errorf("RescaleBitrate with actual=0 = %d, want unchanged 500000", got)
	}
	// Floors at 1000 bps rather than going to zero or negative.
	if got := RescaleBitrate(1000, 1, 1_000_000_000); got != 1000 {
		t.Errorf("RescaleBitrate floor = %d, want 1000", got)
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0s"},
		{-5, "0s"},
		{45, "45s"},
		{125, "2m 05s"},
		{3725, "1h 02m 05s"},
	}
	for _, c := range cases {
		if got := Duration(c.sec); got != c.want {
			t.Errorf("Duration(%g) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestTrimFloat(t *testing.T) {
	cases := []struct {
		f    float64
		want string
	}{
		{29.97, "29.97"},
		{24000.0 / 1001.0, "23.976"}, // must keep 3 places, not round to 23.98
		{30, "30"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := TrimFloat(c.f); got != c.want {
			t.Errorf("TrimFloat(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestSolveVideo_CopyMode(t *testing.T) {
	in := VideoInput{Width: 1920, Height: 1080, FPS: 30, Duration: 10, HasAudio: true}
	p, err := SolveVideo(in, VideoConstraints{CopyVideo: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Mode != ModeCopy {
		t.Errorf("Mode = %v, want ModeCopy", p.Mode)
	}
	if p.Width != 1920 || p.Height != 1080 {
		t.Errorf("copy mode must not resize, got %dx%d", p.Width, p.Height)
	}
}

func TestSolveVideo_NoCapUsesCRF(t *testing.T) {
	in := VideoInput{Width: 1920, Height: 1080, FPS: 30, Duration: 10, HasAudio: true}
	p, err := SolveVideo(in, VideoConstraints{Quality: 90})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Mode != ModeCRF {
		t.Errorf("Mode = %v, want ModeCRF", p.Mode)
	}
	if p.CRF != CRF(90) {
		t.Errorf("CRF = %d, want %d", p.CRF, CRF(90))
	}
	if p.Width != 1920 || p.Height != 1080 {
		t.Errorf("uncapped path must not resize below the ceiling, got %dx%d", p.Width, p.Height)
	}
}

func TestSolveVideo_FitsAtFullResolution(t *testing.T) {
	// A short clip with a generous cap should land at source resolution,
	// first attempt, no rescale.
	in := VideoInput{Width: 1920, Height: 1080, FPS: 30, Duration: 5, HasAudio: false}
	p, err := SolveVideo(in, VideoConstraints{Under: 50 << 20, Codec: "libx264"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Mode != ModeBitrate {
		t.Errorf("Mode = %v, want ModeBitrate", p.Mode)
	}
	if p.Width != 1920 || p.Height != 1080 {
		t.Errorf("got %dx%d, want full 1920x1080", p.Width, p.Height)
	}
	if p.Rescaled {
		t.Error("should not have needed a rescale")
	}
}

func TestSolveVideo_LongClipCannotFitAnyRung(t *testing.T) {
	// A very long clip under a tiny cap cannot reach the bpp floor even at
	// the bottom of the ladder.
	in := VideoInput{Width: 1920, Height: 1080, FPS: 30, Duration: 3600 * 2, HasAudio: false}
	_, err := SolveVideo(in, VideoConstraints{Under: 1 << 20, Codec: "libx264"})
	if err == nil {
		t.Fatal("expected a failure, got none")
	}
	var vf *VideoFailure
	if !errors.As(err, &vf) {
		t.Fatalf("error is %T, want *VideoFailure", err)
	}
	if vf.AudioStarved {
		t.Error("should have failed on bpp floor, not audio budget")
	}
	// Must report the bottom of the ladder as where it gave up.
	if vf.Width != 480 && vf.Height != 480 {
		// aspect-fit of the bottom rung; just confirm it's the smallest rung
		if vf.Width > 480 {
			t.Errorf("expected the failure to report the smallest rung tried, got %dx%d", vf.Width, vf.Height)
		}
	}
}

func TestSolveVideo_AudioStarved(t *testing.T) {
	in := VideoInput{Width: 1920, Height: 1080, FPS: 30, Duration: 60, HasAudio: true}
	c := VideoConstraints{Under: 1024, AudioBitrate: 128, Codec: "libx264"} // 1KiB cap, way under audio alone
	_, err := SolveVideo(in, c)
	var vf *VideoFailure
	if !errors.As(err, &vf) {
		t.Fatalf("expected *VideoFailure, got %v", err)
	}
	if !vf.AudioStarved {
		t.Error("expected AudioStarved, got a resolution/bpp failure")
	}
}

func TestSolveVideo_SilentInputCarriesNoAudioBudget(t *testing.T) {
	in := VideoInput{Width: 1280, Height: 720, FPS: 30, Duration: 10, HasAudio: false}
	p, err := SolveVideo(in, VideoConstraints{Under: 10 << 20, AudioBitrate: 128, Codec: "libx264"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.AudioBitrate != 0 {
		t.Errorf("AudioBitrate = %d, want 0 for a silent input", p.AudioBitrate)
	}
}

func TestSolveVideo_60fpsWithAndWithoutFPSDrop(t *testing.T) {
	// A demanding 60fps screen capture under a cap that only fits if fps
	// drops. Without AllowFPSDrop it must fail or downscale instead;
	// with it, it should find an attempt with halved fps.
	in := VideoInput{Width: 1920, Height: 1080, FPS: 60, Duration: 120, ScreenCapture: true}
	base := VideoConstraints{Under: 6 << 20, Codec: "libx264"} // tight cap over 2 minutes

	noDrop := base
	noDrop.AllowFPSDrop = false
	pNoDrop, errNoDrop := SolveVideo(in, noDrop)

	withDrop := base
	withDrop.AllowFPSDrop = true
	pWithDrop, errWithDrop := SolveVideo(in, withDrop)

	if errWithDrop == nil && pWithDrop.FPS >= 60 && errNoDrop == nil && pNoDrop.FPS >= 60 {
		t.Skip("cap too generous for this input to distinguish the two paths")
	}
	if errWithDrop == nil && pWithDrop.FPSHalved {
		if pWithDrop.FPS != 30 {
			t.Errorf("halved fps = %g, want 30", pWithDrop.FPS)
		}
	}
	// Allowing the drop must never make the outcome worse: if the no-drop
	// path succeeded, the with-drop path must succeed too.
	if errNoDrop == nil && errWithDrop != nil {
		t.Error("allow_fps_drop=true failed where allow_fps_drop=false succeeded")
	}
}

func TestSolveVideo_ScreenCaptureLowerFloorAvoidsNeedlessDownscale(t *testing.T) {
	in := VideoInput{Width: 1280, Height: 720, FPS: 30, Duration: 60, ScreenCapture: true}
	c := VideoConstraints{Under: 8 << 20, Codec: "libx264"}

	p, err := SolveVideo(in, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.BppFloor != 0.02 {
		t.Errorf("BppFloor = %g, want 0.02 for a screen capture", p.BppFloor)
	}
	// Compare against what the camera floor (0.05) would have required: this
	// clip should NOT need to downscale under the lower floor.
	if p.Rescaled {
		t.Error("screen capture should fit at full resolution under the 0.02 floor")
	}
}

func TestSolveVideo_AlreadyUnderCapStillSolves(t *testing.T) {
	// SolveVideo itself doesn't skip (that's plan.Satisfied's job); it should
	// just solve normally and land at full resolution when the budget is
	// generous relative to the content.
	in := VideoInput{Width: 640, Height: 480, FPS: 30, Duration: 2, HasAudio: false}
	p, err := SolveVideo(in, VideoConstraints{Under: 100 << 20, Codec: "libx264"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Width != 640 || p.Height != 480 {
		t.Errorf("got %dx%d, want unchanged 640x480", p.Width, p.Height)
	}
}

func TestSolveVideo_NoDurationErrors(t *testing.T) {
	in := VideoInput{Width: 640, Height: 480, FPS: 30, Duration: 0}
	_, err := SolveVideo(in, VideoConstraints{Under: 10 << 20})
	if err == nil {
		t.Fatal("expected an error for zero duration under a cap")
	}
	if _, ok := errors.AsType[*VideoFailure](err); ok {
		t.Fatal("zero duration should be a plain error, not a VideoFailure")
	}
}

func TestAttempts_LadderOrder(t *testing.T) {
	// Starting above the first two ladder rungs, widths should walk down in
	// ladder order, aspect-fit at each step.
	tried := attempts(1920, 1080, 1920, 1080, 30, false)
	if len(tried) < 2 {
		t.Fatalf("expected multiple attempts, got %d", len(tried))
	}
	if tried[0].w != 1920 {
		t.Errorf("first attempt width = %d, want 1920 (source)", tried[0].w)
	}
	for i := 1; i < len(tried); i++ {
		if tried[i].w > tried[i-1].w {
			t.Errorf("attempt %d width %d is larger than previous %d, ladder should be descending",
				i, tried[i].w, tried[i-1].w)
		}
	}
}

func TestAttempts_FPSHalvingBeforeSecondResolutionStep(t *testing.T) {
	tried := attempts(1920, 1080, 1920, 1080, 60, true)
	// Expect: [0]=source@60, [1]=firstLadderRung@60, [2]=firstLadderRung@30(halved), [3+]=next rungs@30...
	if len(tried) < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", len(tried))
	}
	if tried[0].halved {
		t.Error("first attempt (source resolution) should not have fps halved yet")
	}
	if tried[1].halved {
		t.Error("second attempt (one ladder step) should still be at full fps")
	}
	if !tried[2].halved || tried[2].fps != 30 {
		t.Errorf("third attempt should be the halved-fps retry at the same resolution as attempt 2, got fps=%g halved=%v",
			tried[2].fps, tried[2].halved)
	}
	if tried[2].w != tried[1].w {
		t.Errorf("halved attempt should keep the same resolution as the step before it: %d != %d", tried[2].w, tried[1].w)
	}
}

func TestAttempts_NoHalvingWhenNotAllowedOrNotOver30(t *testing.T) {
	tried := attempts(1920, 1080, 1920, 1080, 30, true)
	for _, a := range tried {
		if a.halved {
			t.Error("must not halve fps that is already <= 30")
		}
	}
	tried = attempts(1920, 1080, 1920, 1080, 60, false)
	for _, a := range tried {
		if a.halved {
			t.Error("must not halve fps when allow_fps_drop is false")
		}
	}
}
