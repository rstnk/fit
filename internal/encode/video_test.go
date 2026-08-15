package encode

import (
	"slices"
	"strings"
	"testing"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/plan"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/solve"
)

func videoTarget(format string, cons config.Constraints) *plan.Target {
	spec, err := config.LookupFormat(probe.KindVideo, format)
	if err != nil {
		panic(err)
	}
	spec, err = spec.AudioCodecOverride(cons.AudioCodec)
	if err != nil {
		panic(err)
	}
	return &plan.Target{
		Input: probe.Info{Path: "in.mp4", Width: 1920, Height: 1080, HasAudio: true, AudioCodec: "pcm"},
		Cons:  cons,
		Spec:  spec,
		Out:   "out." + spec.Ext,
	}
}

func newEncoder() *Encoder {
	e := New()
	e.Print = func(string) {}
	return e
}

// TestAudioArgs_CodecMatchesContainer is the regression test for bug 1.1:
// every output format's audio codec must be one the container actually
// accepts, not always AAC regardless of format.
func TestAudioArgs_CodecMatchesContainer(t *testing.T) {
	cases := []struct {
		format    string
		wantCodec string
	}{
		{"mp4", "aac"},
		{"webm", "libopus"},
		{"mkv", "aac"},
	}
	e := newEncoder()
	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			tgt := videoTarget(c.format, config.Constraints{AudioBitrate: 128})
			j := Job{Target: tgt}
			args := e.audioArgs(j, solve.VideoPlan{})
			if !hasFlagValue(args, "-c:a", c.wantCodec) {
				t.Errorf("audioArgs for %s = %v, want -c:a %s", c.format, args, c.wantCodec)
			}
		})
	}
}

func TestAudioArgs_ExplicitCodecOverridesContainerDefault(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioCodec: "opus", AudioBitrate: 128})
	args := e.audioArgs(Job{Target: tgt}, solve.VideoPlan{})
	if !hasFlagValue(args, "-c:a", "libopus") {
		t.Errorf("audioArgs = %v, want -c:a libopus (explicit override)", args)
	}
}

func TestAudioArgs_NoAudioStream(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{})
	tgt.Input.HasAudio = false
	args := e.audioArgs(Job{Target: tgt}, solve.VideoPlan{})
	if !slices.Contains(args, "-an") {
		t.Errorf("audioArgs = %v, want just [-an] for a silent input", args)
	}
}

func TestAudioArgs_CopyCodecSkipsBitrateAndFilters(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioCodec: "copy", AudioMono: true, AudioLoudnorm: true})
	args := e.audioArgs(Job{Target: tgt}, solve.VideoPlan{})
	if !hasFlagValue(args, "-c:a", "copy") {
		t.Errorf("audioArgs = %v, want -c:a copy", args)
	}
	if slices.Contains(args, "-b:a") || slices.Contains(args, "-ac") || slices.Contains(args, "-af") {
		t.Errorf("audioArgs = %v, a copy codec must not carry bitrate/channel/filter flags", args)
	}
}

func TestAudioArgs_MonoAndLoudnorm(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioBitrate: 128, AudioMono: true, AudioLoudnorm: true})
	args := e.audioArgs(Job{Target: tgt}, solve.VideoPlan{})
	if !hasFlagValue(args, "-ac", "1") {
		t.Errorf("audioArgs = %v, want -ac 1", args)
	}
	if !hasFlagValue(args, "-af", LoudnormFilter) {
		t.Errorf("audioArgs = %v, want -af %s", args, LoudnormFilter)
	}
}

func TestVideoArgs_StripRemovesMetadataOnly(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{Strip: "all", AudioBitrate: 128})
	args := e.videoArgs(Job{Target: tgt}, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 20}, 2, "pass", "out.mp4")
	if !hasFlagValue(args, "-map_metadata", "-1") {
		t.Errorf("videoArgs = %v, want -map_metadata -1 when strip=all", args)
	}

	tgt2 := videoTarget("mp4", config.Constraints{Strip: "none", AudioBitrate: 128})
	args2 := e.videoArgs(Job{Target: tgt2}, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 20}, 2, "pass", "out.mp4")
	if slices.Contains(args2, "-map_metadata") {
		t.Errorf("videoArgs = %v, must not strip metadata when strip=none", args2)
	}
}

func TestVideoArgs_CopyModeSkipsEncodingFlags(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioBitrate: 128})
	args := e.videoArgs(Job{Target: tgt}, solve.VideoPlan{Mode: solve.ModeCopy}, 2, "pass", "out.mp4")
	if !hasFlagValue(args, "-c:v", "copy") {
		t.Errorf("videoArgs = %v, want -c:v copy", args)
	}
	if slices.Contains(args, "-crf") || slices.Contains(args, "-b:v") {
		t.Errorf("videoArgs = %v, copy mode must not carry quality/bitrate flags", args)
	}
}

func TestVideoArgs_BitrateModeUsesPassAndPasslog(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioBitrate: 128})
	vp := solve.VideoPlan{Mode: solve.ModeBitrate, Bitrate: 1_000_000, MaxRate: 1_500_000, BufSize: 2_000_000}
	args := e.videoArgs(Job{Target: tgt}, vp, 1, "/tmp/pass", "/dev/null")
	if !hasFlagValue(args, "-passlogfile", "/tmp/pass") {
		t.Errorf("videoArgs = %v, want -passlogfile /tmp/pass", args)
	}
	if !hasFlagValue(args, "-pass", "1") {
		t.Errorf("videoArgs = %v, want -pass 1", args)
	}
	if !slices.Contains(args, "-an") {
		t.Errorf("pass 1 videoArgs = %v, must be silent (-an)", args)
	}
}

func TestVideoArgs_CRFModeUsesSpecCRFFlag(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("webm", config.Constraints{AudioBitrate: 128}) // vp9 uses -crf too, but -b:v 0 alongside
	args := e.videoArgs(Job{Target: tgt}, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 30}, 2, "pass", "out.webm")
	if !hasFlagValue(args, "-crf", "30") {
		t.Errorf("videoArgs = %v, want -crf 30", args)
	}
	if !hasFlagValue(args, "-b:v", "0") {
		t.Errorf("videoArgs = %v, vp9 CRF mode wants -b:v 0", args)
	}
}

func TestVideoArgs_FaststartOnlyForMovLikeContainers(t *testing.T) {
	e := newEncoder()
	mp4 := videoTarget("mp4", config.Constraints{AudioBitrate: 128})
	args := e.videoArgs(Job{Target: mp4}, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 20}, 2, "pass", "out.mp4")
	if !slices.Contains(args, "-movflags") {
		t.Errorf("videoArgs = %v, want +faststart for mp4", args)
	}

	webm := videoTarget("webm", config.Constraints{AudioBitrate: 128})
	args2 := e.videoArgs(Job{Target: webm}, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 20}, 2, "pass", "out.webm")
	if slices.Contains(args2, "-movflags") {
		t.Errorf("videoArgs = %v, webm must not get -movflags +faststart", args2)
	}
}

func TestVideoArgs_FingerprintEmbedded(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{AudioBitrate: 128})
	j := Job{Target: tgt, FP: "deadbeefdeadbeef"}
	args := e.videoArgs(j, solve.VideoPlan{Mode: solve.ModeCRF, CRF: 20}, 2, "pass", "out.mp4")
	want := "comment=fit:deadbeefdeadbeef"
	if !slices.Contains(args, want) {
		t.Errorf("videoArgs = %v, want a comment metadata value %q", args, want)
	}
}

func TestFilterChain_TonemapOnlyForHDR(t *testing.T) {
	e := newEncoder()

	sdr := videoTarget("mp4", config.Constraints{Tonemap: "auto"})
	sdr.Input.HDR = false
	if got := e.filterChain(Job{Target: sdr}, solve.VideoPlan{}); strings.Contains(got, "zscale") {
		t.Errorf("filterChain(SDR input) = %q, must not include the tonemap chain", got)
	}

	hdr := videoTarget("mp4", config.Constraints{Tonemap: "auto"})
	hdr.Input.HDR = true
	if got := e.filterChain(Job{Target: hdr}, solve.VideoPlan{}); !strings.Contains(got, "zscale") {
		t.Errorf("filterChain(HDR input) = %q, want the tonemap chain included", got)
	}

	forcedOff := videoTarget("mp4", config.Constraints{Tonemap: "off"})
	forcedOff.Input.HDR = true
	if got := e.filterChain(Job{Target: forcedOff}, solve.VideoPlan{}); strings.Contains(got, "zscale") {
		t.Errorf("filterChain(HDR, tonemap=off) = %q, must not tonemap", got)
	}
}

func TestFilterChain_ScaleOnlyWhenDimensionsChange(t *testing.T) {
	e := newEncoder()
	tgt := videoTarget("mp4", config.Constraints{})
	same := e.filterChain(Job{Target: tgt}, solve.VideoPlan{Width: 1920, Height: 1080})
	if strings.Contains(same, "scale=") {
		t.Errorf("filterChain = %q, must not scale when dimensions match the input", same)
	}
	scaled := e.filterChain(Job{Target: tgt}, solve.VideoPlan{Width: 1280, Height: 720})
	if !strings.Contains(scaled, "scale=1280:720") {
		t.Errorf("filterChain = %q, want a scale filter to 1280x720", scaled)
	}
}

func audioTarget(format string, cons config.Constraints) *plan.Target {
	spec, err := config.LookupFormat(probe.KindAudio, format)
	if err != nil {
		panic(err)
	}
	return &plan.Target{
		Input: probe.Info{Path: "in.flac", Kind: probe.KindAudio, HasAudio: true,
			AudioCodec: "flac", Duration: 159.4, CoverWidth: 3000, CoverHeight: 3000},
		Cons: cons,
		Spec: spec,
		Out:  "out." + spec.Ext,
	}
}

func TestKeepsCover(t *testing.T) {
	cases := []struct {
		name   string
		format string
		strip  string
		cover  int
		want   bool
	}{
		{"mp3 carries artwork", "mp3", "none", 3000, true},
		{"m4a carries artwork", "m4a", "none", 3000, true},
		{"flac carries artwork", "flac", "none", 3000, true},
		// Ogg holds artwork in a comment rather than a stream and WAV has
		// nowhere to put one, so both muxers reject a copied picture.
		{"opus cannot carry artwork", "opus", "none", 3000, false},
		{"wav cannot carry artwork", "wav", "none", 3000, false},
		// A cover is metadata as much as the artist name is.
		{"stripping drops artwork", "mp3", "all", 3000, false},
		{"nothing to keep", "mp3", "none", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tgt := audioTarget(c.format, config.Constraints{Strip: c.strip})
			tgt.Input.CoverWidth = c.cover
			if got := KeepsCover(tgt); got != c.want {
				t.Errorf("KeepsCover(%s, strip=%s, cover=%d) = %v, want %v",
					c.format, c.strip, c.cover, got, c.want)
			}
		})
	}
}

func TestAudioOnlyArgs_CoverArt(t *testing.T) {
	e := newEncoder()
	tgt := audioTarget("mp3", config.Constraints{Strip: "none", AudioBitrate: 128})
	j := Job{Target: tgt}

	kept := e.audioOnlyArgs(j, true, 128, "out.mp3")
	if !hasFlagValue(kept, "-c:v", "copy") {
		t.Errorf("args = %v, want -c:v copy to carry the picture through", kept)
	}
	if !hasFlagValue(kept, "-disposition:v:0", "attached_pic") {
		t.Errorf("args = %v, want the attached_pic disposition re-asserted", kept)
	}
	if slices.Contains(kept, "-vn") {
		t.Errorf("args = %v, must not pass -vn while keeping the cover", kept)
	}

	dropped := e.audioOnlyArgs(j, false, 128, "out.mp3")
	if !slices.Contains(dropped, "-vn") {
		t.Errorf("args = %v, want -vn when the cover is not kept", dropped)
	}
	if slices.Contains(dropped, "copy") {
		t.Errorf("args = %v, must not copy a stream it is dropping", dropped)
	}
}

func TestAudioOnlyArgs_StripKeepsTagsByDefault(t *testing.T) {
	e := newEncoder()
	j := Job{Target: audioTarget("mp3", config.Constraints{Strip: "none", AudioBitrate: 128})}
	if args := e.audioOnlyArgs(j, true, 128, "out.mp3"); slices.Contains(args, "-map_metadata") {
		t.Errorf("args = %v, must not strip metadata when strip is none", args)
	}

	stripped := Job{Target: audioTarget("mp3", config.Constraints{Strip: "all", AudioBitrate: 128})}
	if args := e.audioOnlyArgs(stripped, false, 128, "out.mp3"); !hasFlagValue(args, "-map_metadata", "-1") {
		t.Errorf("args = %v, want -map_metadata -1 when strip is all", args)
	}
}

// TestAudioOnlyArgs_LosslessTakesNoBitrate keeps the bitrate flag off codecs
// that would reject it, which the correction loop must not reintroduce.
func TestAudioOnlyArgs_LosslessTakesNoBitrate(t *testing.T) {
	e := newEncoder()
	for _, format := range []string{"flac", "wav"} {
		j := Job{Target: audioTarget(format, config.Constraints{Strip: "none", AudioBitrate: 128})}
		if args := e.audioOnlyArgs(j, false, 128, "out."+format); slices.Contains(args, "-b:a") {
			t.Errorf("%s args = %v, want no -b:a on a lossless codec", format, args)
		}
	}
}

// TestAudioDefaultsKeepTags is the regression test for music losing its tags:
// the strip default that protects photographs from leaking GPS would take the
// artist and title off every track it touched.
func TestAudioDefaultsKeepTags(t *testing.T) {
	cons := config.Resolve(nil, probe.KindAudio, config.Set{})
	if cons.Strip == "all" {
		t.Error("audio defaults to strip=all, which discards every tag on the file")
	}
	if video := config.Resolve(nil, probe.KindVideo, config.Set{}); video.Strip != "all" {
		t.Errorf("video Strip = %q, want all: stripping still protects location data", video.Strip)
	}
	if image := config.Resolve(nil, probe.KindImage, config.Set{}); image.Strip != "all" {
		t.Errorf("image Strip = %q, want all: stripping still protects location data", image.Strip)
	}
}

// hasFlagValue reports whether args contains flag immediately followed by
// value.
func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
