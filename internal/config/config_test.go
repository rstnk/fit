package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstnk/fit/internal/probe"
)

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

// TestResolve_EveryFieldWiresThrough sets every Set field to a distinguishable
// sentinel and checks Resolve carries every one of them into Constraints. A
// field added to Set without a matching deref() line in Resolve would fail
// here instead of silently vanishing at runtime, which is what happened with
// the audio checks in plan.Satisfied before this test existed.
func TestResolve_EveryFieldWiresThrough(t *testing.T) {
	sv := reflect.ValueOf(&Set{}).Elem()
	st := sv.Type()

	for i := 0; i < st.NumField(); i++ {
		f := sv.Field(i)
		elemType := f.Type().Elem()
		np := reflect.New(elemType)
		switch elemType.Kind() {
		case reflect.Int64:
			np.Elem().SetInt(424242)
		case reflect.Int:
			np.Elem().SetInt(4242)
		case reflect.String:
			np.Elem().SetString("sentinel-" + st.Field(i).Name)
		case reflect.Float64:
			np.Elem().SetFloat(42.5)
		case reflect.Bool:
			np.Elem().SetBool(true)
		default:
			t.Fatalf("Set field %s has an unhandled element kind %s; teach this test about it",
				st.Field(i).Name, elemType.Kind())
		}
		f.Set(np)
	}
	full := sv.Interface().(Set)

	c := Resolve(nil, probe.KindVideo, full)
	cv := reflect.ValueOf(c)

	for i := 0; i < st.NumField(); i++ {
		name := st.Field(i).Name
		want := sv.Field(i).Elem().Interface()

		got := cv.FieldByName(name)
		if !got.IsValid() {
			t.Fatalf("Constraints has no field named %q matching Set.%s", name, name)
		}
		if !reflect.DeepEqual(got.Interface(), want) {
			t.Errorf("field %s: Resolve(nil, ..., full) = %v, want sentinel %v", name, got.Interface(), want)
		}
	}
}

// TestCanonical_CoversEveryField locks each Constraints field to a key
// Canonical() must emit. A field missing from Canonical() is invisible to
// the fingerprint hash, so changing it in a preset would never invalidate an
// output made under the old value.
func TestCanonical_CoversEveryField(t *testing.T) {
	keys := map[string]string{
		"Under":         "under=",
		"Width":         "width=",
		"Height":        "height=",
		"Quality":       "quality=",
		"Format":        "format=",
		"FPS":           "fps=",
		"AllowFPSDrop":  "allow_fps_drop=",
		"BppFloor":      "bpp_floor=",
		"Tonemap":       "tonemap=",
		"Strip":         "strip=",
		"Name":          "name=",
		"AudioCodec":    "audio_codec=",
		"AudioBitrate":  "audio_bitrate=",
		"AudioMono":     "audio_mono=",
		"AudioLoudnorm": "audio_loudnorm=",
		"CopyVideo":     "copy_video=",
	}

	ct := reflect.TypeFor[Constraints]()
	for field := range ct.Fields() {
		name := field.Name
		if _, ok := keys[name]; !ok {
			t.Errorf("Constraints field %q has no entry in this test's key map; add one and confirm Canonical() hashes it", name)
		}
	}
	if t.Failed() {
		return
	}

	c := Constraints{
		Under: 1, Width: 2, Height: 3, Quality: 4, Format: "f",
		FPS: 5, AllowFPSDrop: true, BppFloor: 6, Tonemap: "t", Strip: "s",
		Name: "n", AudioCodec: "ac", AudioBitrate: 7, AudioMono: true,
		AudioLoudnorm: true, CopyVideo: true,
	}
	canon := c.Canonical()
	for field, prefix := range keys {
		if !strings.Contains(canon, prefix) {
			t.Errorf("Canonical() is missing key %q for field %s", prefix, field)
		}
	}
}

func TestCanonical_StableRegardlessOfFieldOrder(t *testing.T) {
	// The whole point of sorting is that Canonical is deterministic; assert
	// it directly rather than trusting sort.Strings by inspection.
	a := Constraints{Under: 100, Width: 10, Format: "mp4"}.Canonical()
	b := Constraints{Format: "mp4", Width: 10, Under: 100}.Canonical()
	if a != b {
		t.Errorf("Canonical() depends on struct literal field order:\n%s\n!=\n%s", a, b)
	}
}

func TestMerge_Precedence(t *testing.T) {
	base := Set{Width: new(1280), Quality: new(90)}
	over := Set{Width: new(640)} // overrides Width, leaves Quality alone

	got := base.Merge(over)
	if *got.Width != 640 {
		t.Errorf("Width = %d, want 640 (override wins)", *got.Width)
	}
	if *got.Quality != 90 {
		t.Errorf("Quality = %d, want 90 (unset in override, base survives)", *got.Quality)
	}
}

func TestOnlyAudio(t *testing.T) {
	if (Set{}).OnlyAudio() {
		t.Error("empty set must not be OnlyAudio")
	}
	if (Set{AudioMono: new(true)}).OnlyAudio() != true {
		t.Error("a set with only an audio key must be OnlyAudio")
	}
	if (Set{AudioMono: new(true), Width: new(100)}).OnlyAudio() {
		t.Error("a set with a non-audio key too must not be OnlyAudio")
	}
}

func TestResolve_PerKindOverridesTopLevel(t *testing.T) {
	p := &Preset{
		Base:  Set{Width: new(1280)},
		Kinds: map[probe.Kind]Set{probe.KindImage: {Width: new(2048)}},
	}
	imgC := Resolve(p, probe.KindImage, Set{})
	if imgC.Width != 2048 {
		t.Errorf("image Width = %d, want 2048 (per-kind override)", imgC.Width)
	}
	vidC := Resolve(p, probe.KindVideo, Set{})
	if vidC.Width != 1280 {
		t.Errorf("video Width = %d, want 1280 (top-level, no video override)", vidC.Width)
	}
}

func TestResolve_FlagsOverridePreset(t *testing.T) {
	p := &Preset{Base: Set{Width: new(1280)}}
	flags := Set{Width: new(99)}
	c := Resolve(p, probe.KindVideo, flags)
	if c.Width != 99 {
		t.Errorf("Width = %d, want 99 (flags beat the preset)", c.Width)
	}
}

func TestResolve_NilPresetUsesDefaults(t *testing.T) {
	c := Resolve(nil, probe.KindImage, Set{})
	if c.Format != "jpeg" || c.Quality != 90 {
		t.Errorf("defaults for image = %+v, want format=jpeg quality=90", c)
	}
}

func TestResolve_AudioOnlyPresetCopiesVideo(t *testing.T) {
	p := &Preset{Base: Set{AudioLoudnorm: new(true)}}
	c := Resolve(p, probe.KindVideo, Set{})
	if !c.CopyVideo {
		t.Error("a preset that only sets audio keys should set CopyVideo for the video kind")
	}
	// A flag override, even on an unrelated field, should turn this off:
	// once flags are involved, "only audio" is no longer statically true.
	c2 := Resolve(p, probe.KindVideo, Set{Width: new(100)})
	if c2.CopyVideo {
		t.Error("CopyVideo should not apply once flags add a non-audio constraint")
	}
}

func TestParse_ReservedNameRejected(t *testing.T) {
	data := []byte("[chat]\nunder = \"10MiB\"\n\n[info]\nunder = \"5MiB\"\n")
	_, err := parse("presets.toml", data)
	if err == nil {
		t.Fatal("expected an error for a preset named after a reserved command")
	}
	if !strings.Contains(err.Error(), "info") {
		t.Errorf("error %q does not name the offending preset", err)
	}
	if !strings.Contains(err.Error(), "presets.toml:4") {
		t.Errorf("error %q does not cite the line the reserved name appears on", err)
	}
}

func TestParse_LeadingDashRejected(t *testing.T) {
	data := []byte("[\"-weird\"]\nunder = \"10MiB\"\n")
	if _, err := parse("presets.toml", data); err == nil {
		t.Fatal("expected an error for a preset name starting with -")
	}
}

func TestParse_UnknownKeyRejected(t *testing.T) {
	data := []byte("[chat]\nnotakey = 5\n")
	_, err := parse("presets.toml", data)
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "notakey") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestParse_QualityOutOfRangeRejected(t *testing.T) {
	data := []byte("[chat]\nquality = 500\n")
	if _, err := parse("presets.toml", data); err == nil {
		t.Fatal("expected an error for quality out of the 1-100 range")
	}
}

func TestParse_AudioKeysAtPresetLevelApplyToEveryKind(t *testing.T) {
	data := []byte(`
[voice]
audio_loudnorm = true
audio_mono = true
`)
	cfg, err := parse("presets.toml", data)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Presets["voice"]
	if p.Base.AudioLoudnorm == nil || !*p.Base.AudioLoudnorm {
		t.Error("audio_loudnorm at preset level should land on Base")
	}
	if p.Base.AudioMono == nil || !*p.Base.AudioMono {
		t.Error("audio_mono at preset level should land on Base")
	}
	for _, k := range []probe.Kind{probe.KindAudio, probe.KindVideo} {
		if c := Resolve(p, k, Set{}); !c.AudioLoudnorm {
			t.Errorf("%s should inherit loudnorm from the preset level", k)
		}
	}
}

// TestParse_AudioKeysScopeToTheirTable is the point of the flat spelling: a
// key applies to the kind whose table it sits in, with no key exempt. The
// previous `audio` sub-table hoisted its encoding keys to every kind, which
// made "normalise music, leave video alone" impossible to express.
func TestParse_AudioKeysScopeToTheirTable(t *testing.T) {
	data := []byte(`
[music]
[music.audio]
audio_loudnorm = true
audio_bitrate = 192

[music.video]
audio_bitrate = 96
`)
	cfg, err := parse("presets.toml", data)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Presets["music"]

	audio := Resolve(p, probe.KindAudio, Set{})
	if !audio.AudioLoudnorm {
		t.Error("audio inputs should get loudnorm from [music.audio]")
	}
	if audio.AudioBitrate != 192 {
		t.Errorf("audio bitrate = %d, want 192", audio.AudioBitrate)
	}

	video := Resolve(p, probe.KindVideo, Set{})
	if video.AudioLoudnorm {
		t.Error("video must not inherit loudnorm written in [music.audio]")
	}
	if video.AudioBitrate != 96 {
		t.Errorf("video audio bitrate = %d, want 96 from [music.video]", video.AudioBitrate)
	}
}

// TestParse_OldAudioTableNamesTheReplacement keeps the previous syntax from
// failing as a bare unknown key, since every config written before the change
// uses it and there were three spellings of it in circulation.
func TestParse_OldAudioTableNamesTheReplacement(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			"dotted at preset level",
			"[voice]\naudio.loudnorm = true\n",
			"audio_loudnorm",
		},
		{
			"bare inside the audio table",
			"[v]\n[v.audio]\nloudnorm = true\n",
			"audio_loudnorm",
		},
		{
			"dotted inside a kind table",
			"[v]\n[v.video]\naudio.bitrate = 96\n",
			"audio_bitrate",
		},
		{
			// The old double-nesting trap. `under` was never an encoding key,
			// so the fix is the bare name, not an invented audio_under.
			"double-nested non-encoding key",
			"[v]\n[v.audio]\naudio.under = \"3MB\"\n",
			"write under in [v.audio]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parse("presets.toml", []byte(c.toml))
			if err == nil {
				t.Fatal("expected the old spelling to be rejected")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParse_PerKindSubtable(t *testing.T) {
	data := []byte(`
[chat]
under = "10MiB"
width = 1280
image.width = 2048
`)
	cfg, err := parse("presets.toml", data)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Presets["chat"]
	imgC := Resolve(p, probe.KindImage, Set{})
	if imgC.Width != 2048 {
		t.Errorf("image width = %d, want 2048", imgC.Width)
	}
	vidC := Resolve(p, probe.KindVideo, Set{})
	if vidC.Width != 1280 {
		t.Errorf("video width = %d, want 1280", vidC.Width)
	}
}

func TestParse_UnknownAudioCodecStillAcceptedAtParseTime(t *testing.T) {
	// parseSet only checks that audio_codec is a string; validating it
	// against the known codec list happens later, in AudioCodecOverride,
	// which is where the error can name the actual output format too.
	data := []byte("[chat]\naudio_codec = \"whatever\"\n")
	if _, err := parse("presets.toml", data); err != nil {
		t.Errorf("unexpected error at parse time: %v", err)
	}
}

// TestBuiltinPresetsParse guards the one config every user is handed. A key
// renamed in the parser but missed in the built-in file would only surface on
// somebody's first run, with nothing of theirs to compare against.
func TestBuiltinPresetsParse(t *testing.T) {
	cfg, err := parse("builtin", []byte(BuiltinPresets))
	if err != nil {
		t.Fatalf("built-in presets do not parse: %v", err)
	}
	for _, name := range []string{"chat", "mail", "web", "voice"} {
		if _, ok := cfg.Get(name); !ok {
			t.Errorf("built-in preset %q is missing", name)
		}
	}
	voice := cfg.Presets["voice"]
	if voice.Base.AudioLoudnorm == nil || !*voice.Base.AudioLoudnorm {
		t.Error("the voice preset should set loudnorm, which is the whole preset")
	}
	// voice is loudnorm and nothing else, which is what makes a video input a
	// stream copy rather than a transcode.
	if !voice.Effective(probe.KindVideo).OnlyAudio() {
		t.Error("voice should register as audio-only work on a video input")
	}
}

func TestLoad_WritesBuiltinOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "presets.toml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Get("chat"); !ok {
		t.Error("expected the built-in \"chat\" preset after first-run Load")
	}

	// A second Load must not error or overwrite what's there with different
	// content; it just reads back the same file.
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(cfg2.Names()) != len(cfg.Names()) {
		t.Error("second Load produced a different preset set than the first")
	}
}

func TestConfig_NamesSorted(t *testing.T) {
	cfg := &Config{Presets: map[string]*Preset{"zebra": {}, "alpha": {}, "mid": {}}}
	got := cfg.Names()
	want := []string{"alpha", "mid", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}
