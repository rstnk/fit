package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rstnk/fit/internal/probe"
)

// FormatSpec is everything the output format decides: the extension, the
// codecs, and the container ffmpeg should mux into.
type FormatSpec struct {
	Name       string
	Ext        string
	Container  string // ffmpeg -f, empty means infer from the extension
	VideoCodec string
	AudioCodec string
	// CRFFlag is the encoder's quality knob, which is not -crf everywhere.
	CRFFlag string
	// Lossy says whether quality is a real knob for this image format. PNG
	// and TIFF are lossless: ImageMagick reads -quality for them as a zlib
	// compression level and filter type, not a perceptual scale, so a size
	// search that bisects it is bisecting noise.
	Lossy bool
	// CoverArt says whether the container can mux an attached picture. Ogg
	// carries artwork inside a comment rather than as a stream, and WAV has
	// nowhere to put one at all, so both muxers reject the copied stream.
	CoverArt bool
}

var imageFormats = map[string]FormatSpec{
	"jpeg": {Name: "jpeg", Ext: "jpg", Lossy: true},
	"jpg":  {Name: "jpeg", Ext: "jpg", Lossy: true},
	"png":  {Name: "png", Ext: "png"},
	"webp": {Name: "webp", Ext: "webp", Lossy: true},
	"avif": {Name: "avif", Ext: "avif", Lossy: true},
	"heic": {Name: "heic", Ext: "heic", Lossy: true},
	"tiff": {Name: "tiff", Ext: "tiff"},
}

var videoFormats = map[string]FormatSpec{
	"mp4":  {Name: "mp4", Ext: "mp4", Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac", CRFFlag: "-crf"},
	"h264": {Name: "mp4", Ext: "mp4", Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac", CRFFlag: "-crf"},
	"h265": {Name: "h265", Ext: "mp4", Container: "mp4", VideoCodec: "libx265", AudioCodec: "aac", CRFFlag: "-crf"},
	"hevc": {Name: "h265", Ext: "mp4", Container: "mp4", VideoCodec: "libx265", AudioCodec: "aac", CRFFlag: "-crf"},
	"av1":  {Name: "av1", Ext: "mp4", Container: "mp4", VideoCodec: "libsvtav1", AudioCodec: "aac", CRFFlag: "-crf"},
	"webm": {Name: "webm", Ext: "webm", Container: "webm", VideoCodec: "libvpx-vp9", AudioCodec: "libopus", CRFFlag: "-crf"},
	"mkv":  {Name: "mkv", Ext: "mkv", Container: "matroska", VideoCodec: "libx264", AudioCodec: "aac", CRFFlag: "-crf"},
	"mov":  {Name: "mov", Ext: "mov", Container: "mov", VideoCodec: "libx264", AudioCodec: "aac", CRFFlag: "-crf"},
}

var audioFormats = map[string]FormatSpec{
	"m4a":  {Name: "m4a", Ext: "m4a", Container: "ipod", AudioCodec: "aac", CoverArt: true},
	"aac":  {Name: "m4a", Ext: "m4a", Container: "ipod", AudioCodec: "aac", CoverArt: true},
	"mp3":  {Name: "mp3", Ext: "mp3", Container: "mp3", AudioCodec: "libmp3lame", CoverArt: true},
	"opus": {Name: "opus", Ext: "opus", Container: "opus", AudioCodec: "libopus"},
	"flac": {Name: "flac", Ext: "flac", Container: "flac", AudioCodec: "flac", CoverArt: true},
	"wav":  {Name: "wav", Ext: "wav", Container: "wav", AudioCodec: "pcm_s16le"},
}

// LookupFormat resolves a format name for a kind.
func LookupFormat(kind probe.Kind, name string) (FormatSpec, error) {
	tbl := tableFor(kind)
	if tbl == nil {
		return FormatSpec{}, fmt.Errorf("no output formats for kind %s", kind)
	}
	spec, ok := tbl[strings.ToLower(name)]
	if !ok {
		return FormatSpec{}, fmt.Errorf("unknown %s format %q, try one of: %s",
			kind, name, strings.Join(formatNames(tbl), ", "))
	}
	return spec, nil
}

// audioCodecAliases is the one table mapping a preset's human-friendly
// audio.codec name to the ffmpeg encoder name. It is consulted from both
// AudioCodecOverride and nowhere else, so a name known here is a name fit can
// actually produce.
var audioCodecAliases = map[string]string{
	"aac":        "aac",
	"copy":       "copy",
	"opus":       "libopus",
	"libopus":    "libopus",
	"mp3":        "libmp3lame",
	"libmp3lame": "libmp3lame",
	"vorbis":     "libvorbis",
	"libvorbis":  "libvorbis",
	"flac":       "flac",
}

// AudioCodecOverride swaps the container's default audio codec for one the
// preset named. An empty name keeps the container's own default. Any other
// name must be one AudioCodecOverride recognises, since ffmpeg would
// otherwise be asked to mux a codec no container here can actually carry.
// audioCodecNames are the friendly spellings worth suggesting in an error;
// the ffmpeg-native aliases in audioCodecAliases (libopus, libmp3lame, ...)
// are accepted but not worth surfacing to someone reading a preset file.
var audioCodecNames = []string{"aac", "copy", "flac", "mp3", "opus", "vorbis"}

func (f FormatSpec) AudioCodecOverride(name string) (FormatSpec, error) {
	if name == "" {
		return f, nil
	}
	codec, ok := audioCodecAliases[strings.ToLower(name)]
	if !ok {
		return f, fmt.Errorf("unknown audio codec %q, try one of: %s",
			name, strings.Join(audioCodecNames, ", "))
	}
	f.AudioCodec = codec
	return f, nil
}

func tableFor(kind probe.Kind) map[string]FormatSpec {
	switch kind {
	case probe.KindImage:
		return imageFormats
	case probe.KindVideo:
		return videoFormats
	case probe.KindAudio:
		return audioFormats
	}
	return nil
}

func formatNames(tbl map[string]FormatSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, spec := range tbl {
		if !seen[spec.Name] {
			seen[spec.Name] = true
			out = append(out, spec.Name)
		}
	}
	sort.Strings(out)
	return out
}
