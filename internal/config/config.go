// Package config loads presets, merges per-kind overrides and resolves one
// flat set of effective constraints per input kind.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rstnk/fit/internal/probe"
)

// ReservedNames are the command words a preset may not shadow.
var ReservedNames = []string{"info", "ls", "undo", "cut", "still", "help", "version"}

// Set is a sparse group of constraints. A nil field was not written down, so
// merging keeps whatever a lower layer supplied.
type Set struct {
	Under        *int64
	Width        *int
	Height       *int
	Quality      *int
	Format       *string
	FPS          *float64
	AllowFPSDrop *bool
	BppFloor     *float64
	Tonemap      *string
	Strip        *string
	Name         *string

	AudioCodec    *string
	AudioBitrate  *int
	AudioMono     *bool
	AudioLoudnorm *bool
}

// Empty reports whether nothing at all was set.
func (s Set) Empty() bool { return s == Set{} }

// OnlyAudio reports whether the set touches audio encoding and nothing else.
// Such a preset is not a transcode: the video stream is copied through.
func (s Set) OnlyAudio() bool {
	if s.AudioCodec == nil && s.AudioBitrate == nil && s.AudioMono == nil && s.AudioLoudnorm == nil {
		return false
	}
	rest := s
	rest.AudioCodec, rest.AudioBitrate, rest.AudioMono, rest.AudioLoudnorm = nil, nil, nil, nil
	return rest.Empty()
}

// Merge returns s with every field of over that is set applied on top.
func (s Set) Merge(over Set) Set {
	out := s
	assign(&out.Under, over.Under)
	assign(&out.Width, over.Width)
	assign(&out.Height, over.Height)
	assign(&out.Quality, over.Quality)
	assign(&out.Format, over.Format)
	assign(&out.FPS, over.FPS)
	assign(&out.AllowFPSDrop, over.AllowFPSDrop)
	assign(&out.BppFloor, over.BppFloor)
	assign(&out.Tonemap, over.Tonemap)
	assign(&out.Strip, over.Strip)
	assign(&out.Name, over.Name)
	assign(&out.AudioCodec, over.AudioCodec)
	assign(&out.AudioBitrate, over.AudioBitrate)
	assign(&out.AudioMono, over.AudioMono)
	assign(&out.AudioLoudnorm, over.AudioLoudnorm)
	return out
}

func assign[T any](dst **T, src *T) {
	if src != nil {
		*dst = src
	}
}

// deref copies *src into *dst when src is set, leaving dst untouched
// otherwise. It is Resolve's equivalent of assign: assign keeps sparse
// fields as pointers, deref lands them into the resolved, concrete struct.
func deref[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// Constraints is the resolved, complete set of decisions for one kind. It is
// also the value the fingerprint hashes, so its canonical form is stable.
type Constraints struct {
	Under        int64   `json:"under,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	Quality      int     `json:"quality"`
	Format       string  `json:"format"`
	FPS          float64 `json:"fps,omitempty"`
	AllowFPSDrop bool    `json:"allow_fps_drop"`
	BppFloor     float64 `json:"bpp_floor,omitempty"`
	Tonemap      string  `json:"tonemap"`
	Strip        string  `json:"strip"`
	Name         string  `json:"name"`

	AudioCodec    string `json:"audio_codec"`
	AudioBitrate  int    `json:"audio_bitrate"`
	AudioMono     bool   `json:"audio_mono"`
	AudioLoudnorm bool   `json:"audio_loudnorm"`

	// CopyVideo is set when the preset asked for audio work only.
	CopyVideo bool `json:"copy_video"`
}

// Validate reports a resolved set that contradicts itself. Copying the audio
// stream and filtering it are mutually exclusive: ffmpeg refuses a filtergraph
// on a copied stream, and without this the run died on its bare "Invalid
// argument" with nothing to say which two keys disagreed.
func (c Constraints) Validate() error {
	if !strings.EqualFold(c.AudioCodec, "copy") {
		return nil
	}
	var conflicts []string
	if c.AudioMono {
		conflicts = append(conflicts, "audio_mono")
	}
	if c.AudioLoudnorm {
		conflicts = append(conflicts, "audio_loudnorm")
	}
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf(`audio_codec = "copy" cannot be combined with %s, a copied stream cannot be filtered`,
		strings.Join(conflicts, " and "))
}

// Canonical renders the constraints as sorted key=value lines. This is the
// text the fingerprint hashes, so it must not depend on map iteration order.
func (c Constraints) Canonical() string {
	fields := []string{
		fmt.Sprintf("under=%d", c.Under),
		fmt.Sprintf("width=%d", c.Width),
		fmt.Sprintf("height=%d", c.Height),
		fmt.Sprintf("quality=%d", c.Quality),
		fmt.Sprintf("format=%s", c.Format),
		fmt.Sprintf("fps=%g", c.FPS),
		fmt.Sprintf("allow_fps_drop=%t", c.AllowFPSDrop),
		fmt.Sprintf("bpp_floor=%g", c.BppFloor),
		fmt.Sprintf("tonemap=%s", c.Tonemap),
		fmt.Sprintf("strip=%s", c.Strip),
		fmt.Sprintf("name=%s", c.Name),
		fmt.Sprintf("audio_codec=%s", c.AudioCodec),
		fmt.Sprintf("audio_bitrate=%d", c.AudioBitrate),
		fmt.Sprintf("audio_mono=%t", c.AudioMono),
		fmt.Sprintf("audio_loudnorm=%t", c.AudioLoudnorm),
		fmt.Sprintf("copy_video=%t", c.CopyVideo),
	}
	sort.Strings(fields)
	return strings.Join(fields, "\n")
}

// Preset is one named destination.
type Preset struct {
	Name  string
	About string
	Base  Set
	Kinds map[probe.Kind]Set
}

// Effective returns the sparse set that applies to a kind, before defaults.
func (p *Preset) Effective(k probe.Kind) Set {
	if p == nil {
		return Set{}
	}
	return p.Base.Merge(p.Kinds[k])
}

// Resolve produces the complete constraints for a kind: defaults first, then
// the preset's top-level keys, then its per-kind sub-table, then flags.
func Resolve(p *Preset, k probe.Kind, flags Set) Constraints {
	effective := p.Effective(k)
	sparse := effective.Merge(flags)
	c := defaults(k)

	// A preset that only speaks about audio leaves the video stream alone.
	if p != nil && effective.OnlyAudio() && flags.Empty() && k == probe.KindVideo {
		c.CopyVideo = true
	}

	deref(&c.Under, sparse.Under)
	deref(&c.Width, sparse.Width)
	deref(&c.Height, sparse.Height)
	deref(&c.Quality, sparse.Quality)
	deref(&c.Format, sparse.Format)
	deref(&c.FPS, sparse.FPS)
	deref(&c.AllowFPSDrop, sparse.AllowFPSDrop)
	deref(&c.BppFloor, sparse.BppFloor)
	deref(&c.Tonemap, sparse.Tonemap)
	deref(&c.Strip, sparse.Strip)
	deref(&c.Name, sparse.Name)
	deref(&c.AudioCodec, sparse.AudioCodec)
	deref(&c.AudioBitrate, sparse.AudioBitrate)
	deref(&c.AudioMono, sparse.AudioMono)
	deref(&c.AudioLoudnorm, sparse.AudioLoudnorm)
	return c
}

// DefaultName is the output name template when a preset does not set one.
const DefaultName = "{stem}.{tag}.{ext}"

// DefaultPreset is the preset applied when the command line names none. It is
// an ordinary preset that happens to be looked up by convention, so a config
// that does not define it is unaffected.
const DefaultPreset = "default"

func defaults(k probe.Kind) Constraints {
	c := Constraints{
		Quality: 90,
		Tonemap: "auto",
		Strip:   "all",
		Name:    DefaultName,
		// AudioCodec is left empty: it means "whatever the container defaults
		// to", which FormatSpec.AudioCodecOverride resolves. A concrete
		// default here would make every preset's audio codec indistinguishable
		// from one that actually asked for AAC by name.
		AudioBitrate: 128,
	}
	switch k {
	case probe.KindImage:
		c.Format = "jpeg"
	case probe.KindVideo:
		c.Format = "mp4"
	case probe.KindAudio:
		c.Format = "m4a"
		// Stripping exists to drop the incidental metadata a camera leaves
		// behind, above all GPS. On a music file the tags are the content, so
		// the same default would throw away the artist and title the file
		// exists to carry.
		c.Strip = "none"
	}
	return c
}

// Config is a loaded presets file.
type Config struct {
	Path    string
	Presets map[string]*Preset
}

// Names returns preset names in sorted order.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Presets))
	for n := range c.Presets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Get returns a preset by name.
func (c *Config) Get(name string) (*Preset, bool) {
	p, ok := c.Presets[name]
	return p, ok
}

// DefaultPath is the presets file location, honouring XDG_CONFIG_HOME.
func DefaultPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "fit", "presets.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "fit", "presets.toml")
	}
	return filepath.Join(home, ".config", "fit", "presets.toml")
}

// Load reads the presets file, writing the built-in set on first run.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeBuiltin(path); err != nil {
			return nil, fmt.Errorf("writing default presets to %s: %w", path, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(path, data)
}

func writeBuiltin(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(BuiltinPresets), 0o644)
}

// constraintKeys are the keys accepted anywhere constraints are written. The
// audio_ names describe the audio stream of whatever the enclosing table
// selects, so audio_bitrate under [p.video] is the audio inside a video and
// audio_bitrate under [p.audio] is a music file. Which table a key sits in is
// the only thing that decides what it applies to, with no key exempt.
var constraintKeys = map[string]bool{
	"under": true, "width": true, "height": true, "quality": true,
	"format": true, "fps": true, "allow_fps_drop": true, "bpp_floor": true,
	"tonemap": true, "strip": true, "name": true,
	"audio_codec": true, "audio_bitrate": true, "audio_mono": true,
	"audio_loudnorm": true,
}

// renamedAudioKeys are the bare spellings the old `audio` sub-table took.
// They land here as unknown keys, so the error names the replacement rather
// than leaving someone to diff their config against the docs.
var renamedAudioKeys = map[string]string{
	"codec": "audio_codec", "bitrate": "audio_bitrate",
	"mono": "audio_mono", "loudnorm": "audio_loudnorm",
}

// flatNames renders the keys of an old `audio` sub-table in their replacement
// spelling, so the error shows the line that should have been written. Only
// the four encoding keys take the prefix: everything else was already an
// ordinary constraint that belongs in the enclosing table under its own name.
func flatNames(sub map[string]any) string {
	out := make([]string, 0, len(sub))
	for k := range sub {
		if flat, ok := renamedAudioKeys[k]; ok {
			out = append(out, flat)
		} else {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func parse(path string, data []byte) (*Config, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg := &Config{Path: path, Presets: map[string]*Preset{}}
	lines := strings.Split(string(data), "\n")

	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := checkReserved(name, path, lines); err != nil {
			return nil, err
		}
		tbl, ok := raw[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: preset %q must be a table", path, name)
		}
		p, err := parsePreset(name, tbl, path)
		if err != nil {
			return nil, err
		}
		cfg.Presets[name] = p
	}
	return cfg, nil
}

func checkReserved(name string, path string, lines []string) error {
	bad := strings.HasPrefix(name, "-")
	for _, r := range ReservedNames {
		if name == r {
			bad = true
		}
	}
	if !bad {
		return nil
	}
	return fmt.Errorf("preset %q shadows a built-in command (%s:%d)",
		name, filepath.Base(path), headingLine(lines, name))
}

func headingLine(lines []string, name string) int {
	re := regexp.MustCompile(`^\s*\[\s*` + regexp.QuoteMeta(name) + `\s*[\].]`)
	for i, l := range lines {
		if re.MatchString(l) {
			return i + 1
		}
	}
	return 1
}

func parsePreset(name string, tbl map[string]any, path string) (*Preset, error) {
	p := &Preset{Name: name, Kinds: map[probe.Kind]Set{}}

	base := map[string]any{}
	for k, v := range tbl {
		switch k {
		case "about":
			p.About, _ = v.(string)
		case "image", "video", "audio":
			sub, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: [%s.%s] must be a table", path, name, k)
			}
			s, err := parseSet(sub, fmt.Sprintf("%s.%s", name, k), path)
			if err != nil {
				return nil, err
			}
			p.Kinds[probe.Kind(k)] = s
		default:
			base[k] = v
		}
	}

	s, err := parseSet(base, name, path)
	if err != nil {
		return nil, err
	}
	p.Base = s
	return p, nil
}

func parseSet(tbl map[string]any, where, path string) (Set, error) {
	var s Set
	keys := make([]string, 0, len(tbl))
	for k := range tbl {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := tbl[k]
		if !constraintKeys[k] {
			if k == "audio" {
				if sub, ok := v.(map[string]any); ok {
					return s, fmt.Errorf("%s: [%s.audio] is not a table any more; write %s in [%s]",
						path, where, flatNames(sub), where)
				}
			}
			if flat, renamed := renamedAudioKeys[k]; renamed {
				return s, fmt.Errorf("%s: unknown key %q in [%s]; audio keys are flat now, write %s",
					path, k, where, flat)
			}
			return s, fmt.Errorf("%s: unknown key %q in [%s]", path, k, where)
		}
		var err error
		switch k {
		case "under":
			var raw string
			if raw, err = asString(v, k, where, path); err == nil {
				var n int64
				if n, err = ParseSize(raw); err == nil {
					s.Under = &n
				}
			}
		case "width":
			s.Width, err = asIntP(v, k, where, path)
		case "height":
			s.Height, err = asIntP(v, k, where, path)
		case "quality":
			s.Quality, err = asIntRangeP(v, k, where, path, 1, 100)
		case "format":
			s.Format, err = asStringP(v, k, where, path)
		case "fps":
			s.FPS, err = asFloatP(v, k, where, path)
		case "allow_fps_drop":
			s.AllowFPSDrop, err = asBoolP(v, k, where, path)
		case "bpp_floor":
			s.BppFloor, err = asFloatP(v, k, where, path)
		case "tonemap":
			s.Tonemap, err = asEnumP(v, k, where, path, "auto", "on", "off")
		case "strip":
			s.Strip, err = asEnumP(v, k, where, path, "all", "none")
		case "name":
			s.Name, err = asStringP(v, k, where, path)
		case "audio_codec":
			s.AudioCodec, err = asStringP(v, k, where, path)
		case "audio_bitrate":
			s.AudioBitrate, err = asIntP(v, k, where, path)
		case "audio_mono":
			s.AudioMono, err = asBoolP(v, k, where, path)
		case "audio_loudnorm":
			s.AudioLoudnorm, err = asBoolP(v, k, where, path)
		}
		if err != nil {
			return s, err
		}
	}
	return s, nil
}

func typeErr(k, where, path, want string) error {
	return fmt.Errorf("%s: key %q in [%s] must be %s", path, k, where, want)
}

func asString(v any, k, where, path string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", typeErr(k, where, path, "a string")
	}
	return s, nil
}

func asStringP(v any, k, where, path string) (*string, error) {
	s, err := asString(v, k, where, path)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func asEnumP(v any, k, where, path string, allowed ...string) (*string, error) {
	s, err := asString(v, k, where, path)
	if err != nil {
		return nil, err
	}
	if slices.Contains(allowed, s) {
		return &s, nil
	}
	return nil, typeErr(k, where, path, "one of "+strings.Join(allowed, ", "))
}

func asIntP(v any, k, where, path string) (*int, error) {
	switch t := v.(type) {
	case int64:
		n := int(t)
		return &n, nil
	case float64:
		n := int(t)
		return &n, nil
	}
	return nil, typeErr(k, where, path, "an integer")
}

func asIntRangeP(v any, k, where, path string, lo, hi int) (*int, error) {
	n, err := asIntP(v, k, where, path)
	if err != nil {
		return nil, err
	}
	if *n < lo || *n > hi {
		return nil, fmt.Errorf("%s: key %q in [%s] must be between %d and %d, got %d",
			path, k, where, lo, hi, *n)
	}
	return n, nil
}

func asFloatP(v any, k, where, path string) (*float64, error) {
	switch t := v.(type) {
	case int64:
		f := float64(t)
		return &f, nil
	case float64:
		return &t, nil
	}
	return nil, typeErr(k, where, path, "a number")
}

func asBoolP(v any, k, where, path string) (*bool, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, typeErr(k, where, path, "true or false")
	}
	return &b, nil
}
