package probe

import "testing"

// coverStream is an embedded picture as ffprobe reports one: a still codec, no
// frame count, and the duration of the audio it is attached to.
func coverStream(codec string, w, h int, attached bool) ffStream {
	s := ffStream{
		CodecType: "video", CodecName: codec, Width: w, Height: h,
		AvgFrameRate: "0/0", RFrameRate: "90000/1", Duration: "159.4",
		Disposition: map[string]int{},
	}
	if attached {
		s.Disposition["attached_pic"] = 1
	}
	return s
}

func audioStream(codec string) ffStream {
	return ffStream{
		CodecType: "audio", CodecName: codec, Channels: 2,
		SampleRate: "48000", Duration: "159.4", Disposition: map[string]int{},
	}
}

// TestClassify_TaggedAudioIsNotAnImage covers the case that sent every tagged
// music file down the image path: a cover is a video stream holding one frame
// in a still codec, which is indistinguishable from a photograph until the
// attached_pic disposition is read.
func TestClassify_TaggedAudioIsNotAnImage(t *testing.T) {
	cases := []struct {
		name   string
		format string
		codec  string
	}{
		{"flac", "flac", "flac"},
		{"mp3", "mp3", "mp3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in Info
			ff := &ffOutput{
				Format:  ffFormat{FormatName: c.format, Duration: "159.4"},
				Streams: []ffStream{audioStream(c.codec), coverStream("mjpeg", 3000, 3000, true)},
			}
			classify(&in, ff)

			if in.Kind != KindAudio {
				t.Errorf("Kind = %s, want %s", in.Kind, KindAudio)
			}
			if in.Duration != 159.4 {
				t.Errorf("Duration = %v, want 159.4 (a cover must not zero it)", in.Duration)
			}
			if in.Width != 0 || in.Height != 0 {
				t.Errorf("Width/Height = %dx%d, want 0x0: the cover's size is not the file's",
					in.Width, in.Height)
			}
			if in.CoverWidth != 3000 || in.CoverHeight != 3000 {
				t.Errorf("Cover = %dx%d, want 3000x3000", in.CoverWidth, in.CoverHeight)
			}
			if !in.HasAudio || in.AudioCodec != c.codec {
				t.Errorf("HasAudio = %v, AudioCodec = %q, want true, %q",
					in.HasAudio, in.AudioCodec, c.codec)
			}
		})
	}
}

func TestClassify_Bitrate(t *testing.T) {
	// The container's own figure is used when it has one.
	var in Info
	in.Size = 31_464_149
	classify(&in, &ffOutput{
		Format:  ffFormat{FormatName: "flac", Duration: "159.4", BitRate: "1579172"},
		Streams: []ffStream{audioStream("flac")},
	})
	if in.Bitrate != 1579172 {
		t.Errorf("Bitrate = %d, want 1579172", in.Bitrate)
	}

	// Matroska stores no bitrate anywhere, so the size and duration have to
	// supply it or the column is empty on every mkv.
	var mkv Info
	mkv.Size = 6_553_600
	classify(&mkv, &ffOutput{
		Format: ffFormat{FormatName: "matroska,webm", Duration: "95.0"},
		Streams: []ffStream{
			{CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080,
				NBFrames: "2280", AvgFrameRate: "24/1", RFrameRate: "24/1"},
			audioStream("opus"),
		},
	})
	if want := int(float64(mkv.Size) * 8 / 95.0); mkv.Bitrate != want {
		t.Errorf("Bitrate = %d, want %d derived from size and duration", mkv.Bitrate, want)
	}
}

// TestClassify_StillHasNoBitrate keeps a rate off files that have no duration
// to spread their bytes over.
func TestClassify_StillHasNoBitrate(t *testing.T) {
	var in Info
	in.Size = 1_700_000
	classify(&in, &ffOutput{
		Format: ffFormat{FormatName: "png_pipe", BitRate: "8000000"},
		Streams: []ffStream{{
			CodecType: "video", CodecName: "png", Width: 1920, Height: 1080,
			NBFrames: "1", AvgFrameRate: "25/1", RFrameRate: "25/1",
		}},
	})
	if in.Kind != KindImage {
		t.Fatalf("Kind = %s, want %s", in.Kind, KindImage)
	}
	if in.Bitrate != 0 {
		t.Errorf("Bitrate = %d, want 0: a still has no rate", in.Bitrate)
	}
}

// TestClassify_CoverWithoutDisposition is the fallback path: not every muxer
// flags its artwork, so a still beside an audio stream is read as a cover on
// the strength of that pairing alone.
func TestClassify_CoverWithoutDisposition(t *testing.T) {
	var in Info
	ff := &ffOutput{
		Format:  ffFormat{FormatName: "ogg", Duration: "159.4"},
		Streams: []ffStream{audioStream("vorbis"), coverStream("png", 600, 600, false)},
	}
	classify(&in, ff)

	if in.Kind != KindAudio {
		t.Errorf("Kind = %s, want %s", in.Kind, KindAudio)
	}
	if in.CoverWidth != 600 {
		t.Errorf("CoverWidth = %d, want 600", in.CoverWidth)
	}
}

// TestClassify_StillWithoutAudio guards the other direction: an ordinary
// photograph has the same one-frame still shape as a cover and must keep
// classifying as an image.
func TestClassify_StillWithoutAudio(t *testing.T) {
	var in Info
	ff := &ffOutput{
		Format: ffFormat{FormatName: "png_pipe"},
		Streams: []ffStream{{
			CodecType: "video", CodecName: "png", Width: 1920, Height: 1080,
			NBFrames: "1", AvgFrameRate: "25/1", RFrameRate: "25/1",
		}},
	}
	classify(&in, ff)

	if in.Kind != KindImage {
		t.Errorf("Kind = %s, want %s", in.Kind, KindImage)
	}
	if in.Width != 1920 || in.Height != 1080 {
		t.Errorf("Width/Height = %dx%d, want 1920x1080", in.Width, in.Height)
	}
	if in.CoverWidth != 0 {
		t.Errorf("CoverWidth = %d, want 0: a photograph is not its own cover art", in.CoverWidth)
	}
}

// TestClassify_VideoWithCoverArt keeps a real picture stream winning over an
// attached one, so a film with a poster frame stays a video.
func TestClassify_VideoWithCoverArt(t *testing.T) {
	var in Info
	ff := &ffOutput{
		Format: ffFormat{FormatName: "mov,mp4,m4a", Duration: "95.0"},
		Streams: []ffStream{
			coverStream("mjpeg", 600, 600, true),
			{CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080,
				NBFrames: "2280", AvgFrameRate: "24/1", RFrameRate: "24/1"},
			audioStream("aac"),
		},
	}
	classify(&in, ff)

	if in.Kind != KindVideo {
		t.Errorf("Kind = %s, want %s", in.Kind, KindVideo)
	}
	if in.Width != 1920 || in.Height != 1080 {
		t.Errorf("Width/Height = %dx%d, want 1920x1080 (the film, not the poster)",
			in.Width, in.Height)
	}
	if in.CoverWidth != 600 {
		t.Errorf("CoverWidth = %d, want 600", in.CoverWidth)
	}
}

// TestClassify_AudioWithoutCover is the plain case, kept so the cover handling
// cannot start inventing artwork that is not there.
func TestClassify_AudioWithoutCover(t *testing.T) {
	var in Info
	ff := &ffOutput{
		Format:  ffFormat{FormatName: "wav", Duration: "12.5"},
		Streams: []ffStream{audioStream("pcm_s16le")},
	}
	classify(&in, ff)

	if in.Kind != KindAudio {
		t.Errorf("Kind = %s, want %s", in.Kind, KindAudio)
	}
	if in.CoverWidth != 0 || in.CoverHeight != 0 {
		t.Errorf("Cover = %dx%d, want 0x0", in.CoverWidth, in.CoverHeight)
	}
	if in.Duration != 12.5 {
		t.Errorf("Duration = %v, want 12.5", in.Duration)
	}
}
