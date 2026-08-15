// Package probe wraps ffprobe and classifies media files into kinds.
//
// ffprobe is the single detection path for every kind, so images, video and
// audio are all recognised the same way regardless of extension.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Kind is the classification of an input file.
type Kind string

const (
	KindImage   Kind = "image"
	KindVideo   Kind = "video"
	KindAudio   Kind = "audio"
	KindUnknown Kind = "unknown"
)

// Kinds lists the kinds that can be encoded, in a stable order.
var Kinds = []Kind{KindImage, KindVideo, KindAudio}

// Info is everything the rest of the tool needs to know about an input.
type Info struct {
	Path    string    `json:"path"`
	Kind    Kind      `json:"kind"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"-"`

	Format   string  `json:"format"`
	Duration float64 `json:"duration,omitempty"`
	// Bitrate is the whole file's rate, cover art and container overhead
	// included. Per-stream rates are the more precise figure but ffprobe
	// mostly does not have them: Matroska stores none at all and FLAC reports
	// none either, so only the container's own total is reliably present.
	Bitrate int `json:"bitrate,omitempty"`

	VideoCodec  string  `json:"video_codec,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	AvgFPS      float64 `json:"avg_fps,omitempty"`
	TimebaseFPS float64 `json:"timebase_fps,omitempty"`
	NBFrames    int     `json:"nb_frames,omitempty"`
	VFR         bool    `json:"vfr,omitempty"`
	Rotation    int     `json:"rotation,omitempty"`

	ColorTransfer  string `json:"color_transfer,omitempty"`
	ColorPrimaries string `json:"color_primaries,omitempty"`
	HDR            bool   `json:"hdr,omitempty"`
	DoViProfile    int    `json:"dovi_profile,omitempty"`

	HasAudio      bool   `json:"has_audio"`
	AudioCodec    string `json:"audio_codec,omitempty"`
	AudioChannels int    `json:"audio_channels,omitempty"`
	AudioBitrate  int    `json:"audio_bitrate,omitempty"`

	// CoverWidth and CoverHeight describe embedded artwork, kept apart from
	// Width and Height so a tagged music file never presents its album art
	// as the picture to be resized.
	CoverWidth  int `json:"cover_width,omitempty"`
	CoverHeight int `json:"cover_height,omitempty"`

	ScreenCapture bool `json:"screen_capture,omitempty"`

	// Note explains an unknown classification.
	Note string `json:"note,omitempty"`
	// Unreadable is true when the path itself could not be opened or stat'd,
	// as opposed to being read fine but not recognised as media. Kind is
	// KindUnknown either way; this is what tells a caller whether the input
	// failed outright or is a legitimate skip, such as a stray text file
	// swept up by a glob.
	Unreadable bool `json:"unreadable,omitempty"`
}

// stillCodecs are codecs that only ever carry a single picture.
var stillCodecs = map[string]bool{
	"mjpeg": true, "png": true, "bmp": true, "tiff": true, "webp": true,
	"heif": true, "avif": true, "jpeg2000": true, "jpegls": true,
	"ppm": true, "pgm": true, "pgmyuv": true, "pam": true, "targa": true,
	"dpx": true, "exr": true, "sgi": true, "xwd": true, "pcx": true,
	"jpegxl": true, "qoi": true, "dds": true, "psd": true, "ico": true,
}

// animatedImageFormats are containers we read as a still first frame.
var animatedImageFormats = map[string]bool{
	"gif": true, "apng": true, "webp": true, "webp_pipe": true,
}

// Prober runs ffprobe.
type Prober struct {
	FFprobe string
}

// New returns a Prober using ffprobe from PATH.
func New() *Prober { return &Prober{FFprobe: "ffprobe"} }

type ffOutput struct {
	Format  ffFormat   `json:"format"`
	Streams []ffStream `json:"streams"`
}

type ffFormat struct {
	Filename   string         `json:"filename"`
	NBStreams  int            `json:"nb_streams"`
	FormatName string         `json:"format_name"`
	Duration   string         `json:"duration"`
	Size       string         `json:"size"`
	BitRate    string         `json:"bit_rate"`
	Tags       map[string]any `json:"tags"`
}

type ffStream struct {
	Index          int              `json:"index"`
	CodecName      string           `json:"codec_name"`
	CodecType      string           `json:"codec_type"`
	Width          int              `json:"width"`
	Height         int              `json:"height"`
	NBFrames       string           `json:"nb_frames"`
	AvgFrameRate   string           `json:"avg_frame_rate"`
	RFrameRate     string           `json:"r_frame_rate"`
	Duration       string           `json:"duration"`
	BitRate        string           `json:"bit_rate"`
	ColorTransfer  string           `json:"color_transfer"`
	ColorPrimaries string           `json:"color_primaries"`
	ColorSpace     string           `json:"color_space"`
	Channels       int              `json:"channels"`
	SampleRate     string           `json:"sample_rate"`
	Tags           map[string]any   `json:"tags"`
	SideDataList   []map[string]any `json:"side_data_list"`
	Disposition    map[string]int   `json:"disposition"`
}

// Probe runs ffprobe against path and classifies the result. A file that
// ffprobe cannot read comes back as KindUnknown with a Note rather than an
// error, so a batch can carry on past it.
func (p *Prober) Probe(ctx context.Context, path string) (Info, error) {
	in := Info{Path: path, Kind: KindUnknown}

	st, err := os.Stat(path)
	if err != nil {
		in.Note = err.Error()
		in.Unreadable = true
		return in, err
	}
	if st.IsDir() {
		in.Note = "is a directory"
		return in, nil
	}
	in.Size = st.Size()
	in.ModTime = st.ModTime()

	cmd := exec.CommandContext(ctx, p.FFprobe,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		in.Note = strings.TrimPrefix(firstLine(stderr.String()), path+": ")
		if in.Note == "" {
			in.Note = "ffprobe failed"
		}
		return in, nil
	}

	var ff ffOutput
	if err := json.Unmarshal(out, &ff); err != nil {
		in.Note = "ffprobe produced unreadable output"
		return in, nil
	}
	if len(ff.Streams) == 0 {
		in.Note = "no streams"
		return in, nil
	}

	classify(&in, &ff)
	return in, nil
}

func classify(in *Info, ff *ffOutput) {
	in.Format = ff.Format.FormatName
	in.Duration = parseFloat(ff.Format.Duration)

	var video *ffStream
	var audio *ffStream
	var cover *ffStream
	for i := range ff.Streams {
		s := &ff.Streams[i]
		switch s.CodecType {
		case "video":
			switch {
			case attachedPic(s):
				if cover == nil {
					cover = s
				}
			case video == nil:
				video = s
			}
		case "audio":
			if audio == nil {
				audio = s
			}
		}
	}
	// A lone still frame sitting beside an audio stream is album art the muxer
	// never flagged. Without this a tagged MP3 or FLAC classifies as an image,
	// because a cover is a video stream carrying one picture in a still codec,
	// which is exactly what the test for a still photograph looks for.
	if video != nil && audio != nil && cover == nil && coverLike(video) {
		video, cover = nil, video
	}
	if cover != nil {
		in.CoverWidth = cover.Width
		in.CoverHeight = cover.Height
	}

	if audio != nil {
		in.HasAudio = true
		in.AudioCodec = audio.CodecName
		in.AudioChannels = audio.Channels
		in.AudioBitrate = int(parseFloat(audio.BitRate))
	}

	if video != nil {
		in.VideoCodec = video.CodecName
		in.Width = video.Width
		in.Height = video.Height
		in.NBFrames = int(parseFloat(video.NBFrames))
		in.AvgFPS = parseRate(video.AvgFrameRate)
		in.TimebaseFPS = parseRate(video.RFrameRate)
		in.ColorTransfer = video.ColorTransfer
		in.ColorPrimaries = video.ColorPrimaries
		in.Rotation = rotationOf(video)
		in.DoViProfile = doviProfile(video)
		// BT.2020 in any of the three fields is enough: read as BT.709 it
		// produces washed-out grey, which is what the tonemap chain prevents.
		in.HDR = video.ColorTransfer == "arib-std-b67" ||
			video.ColorTransfer == "smpte2084" ||
			video.ColorPrimaries == "bt2020" ||
			strings.HasPrefix(video.ColorSpace, "bt2020")
		if in.Duration == 0 {
			in.Duration = parseFloat(video.Duration)
		}
		// A timebase far above the measured rate is the macOS variable frame
		// rate signature. r_frame_rate is never used for arithmetic.
		if in.AvgFPS > 0 && in.TimebaseFPS > in.AvgFPS*1.5 {
			in.VFR = true
		}
	}

	firstFrameOnly := animatedImageFormats[ff.Format.FormatName]
	still := video != nil && in.NBFrames <= 1 &&
		(stillCodecs[video.CodecName] || (in.Duration == 0 && in.NBFrames == 1))

	in.Bitrate = int(parseFloat(ff.Format.BitRate))
	if in.Bitrate == 0 && in.Duration > 0 && in.Size > 0 {
		in.Bitrate = int(float64(in.Size) * 8 / in.Duration)
	}

	switch {
	case firstFrameOnly, still:
		in.Kind = KindImage
		// A still is one frame however the container describes itself.
		in.Duration = 0
		in.AvgFPS = 0
		in.NBFrames = 1
		in.Bitrate = 0
	case video != nil:
		in.Kind = KindVideo
		in.ScreenCapture = detectScreenCapture(in, ff, video)
	case audio != nil:
		in.Kind = KindAudio
	default:
		in.Note = "no usable streams"
	}
}

// attachedPic reports the muxer's own verdict that a video stream is embedded
// artwork rather than picture content. Where it is set it is definitive.
func attachedPic(s *ffStream) bool { return s.Disposition["attached_pic"] == 1 }

// coverLike is the fallback for containers that carry artwork without setting
// the disposition flag: one frame in a codec that only ever holds a picture.
func coverLike(s *ffStream) bool {
	return stillCodecs[s.CodecName] && int(parseFloat(s.NBFrames)) <= 1
}

// standardRates are the frame rates ordinary camera and broadcast material
// arrives at. Anything else is a hint that the source is a capture.
var standardRates = []float64{23.976, 24, 25, 29.97, 30, 48, 50, 59.94, 60, 90, 100, 120, 144, 240}

func detectScreenCapture(in *Info, ff *ffOutput, video *ffStream) bool {
	for k, v := range ff.Format.Tags {
		if strings.HasPrefix(strings.ToLower(k), "com.apple.quicktime") &&
			strings.Contains(strings.ToLower(str(v)), "screen") {
			return true
		}
	}
	for k := range video.Tags {
		if strings.Contains(strings.ToLower(k), "screen") {
			return true
		}
	}
	// Silence alone is not evidence: it matches any muted export as much as it
	// matches a screen recording. A non-standard frame rate is the real
	// signal, since ordinary camera and broadcast material always lands on a
	// standard rate; it counts whether or not the clip has audio.
	return in.AvgFPS > 0 && !nearStandardRate(in.AvgFPS)
}

func nearStandardRate(fps float64) bool {
	for _, r := range standardRates {
		if math.Abs(fps-r) < 0.2 {
			return true
		}
	}
	return false
}

func rotationOf(s *ffStream) int {
	for _, sd := range s.SideDataList {
		if str(sd["side_data_type"]) == "Display Matrix" {
			if r, ok := sd["rotation"]; ok {
				return normaliseRotation(int(math.Round(toFloat(r))))
			}
		}
	}
	if v, ok := s.Tags["rotate"]; ok {
		return normaliseRotation(int(math.Round(toFloat(v))))
	}
	return 0
}

func normaliseRotation(deg int) int {
	deg %= 360
	if deg < 0 {
		deg += 360
	}
	return deg
}

func doviProfile(s *ffStream) int {
	for _, sd := range s.SideDataList {
		if strings.Contains(str(sd["side_data_type"]), "DOVI") {
			if p, ok := sd["dv_profile"]; ok {
				return int(toFloat(p))
			}
		}
	}
	return 0
}

// parseRate reads an ffprobe rational such as "30000/1001".
func parseRate(s string) float64 {
	if s == "" || s == "0/0" {
		return 0
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	n, d := parseFloat(num), parseFloat(den)
	if d == 0 {
		return 0
	}
	return n / d
}

func parseFloat(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		return parseFloat(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
