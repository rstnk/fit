package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/probe"
)

func baseSpec() config.FormatSpec { return config.FormatSpec{Ext: "jpg"} }

func TestSatisfied_UnderCapAndSameExt(t *testing.T) {
	in := probe.Info{Path: "photo.jpg", Size: 100}
	c := config.Constraints{Under: 1000, Tonemap: "auto"}
	ok, note := Satisfied(in, c, baseSpec())
	if !ok {
		t.Fatalf("expected satisfied, got not satisfied")
	}
	if note == "" {
		t.Error("expected a human-readable note explaining why")
	}
}

func TestSatisfied_NoCapNeverSatisfied(t *testing.T) {
	in := probe.Info{Path: "photo.jpg", Size: 1}
	ok, _ := Satisfied(in, config.Constraints{}, baseSpec())
	if ok {
		t.Error("a preset with no cap should never claim an input is already satisfied")
	}
}

func TestSatisfied_OverCap(t *testing.T) {
	in := probe.Info{Path: "photo.jpg", Size: 2000}
	ok, _ := Satisfied(in, config.Constraints{Under: 1000}, baseSpec())
	if ok {
		t.Error("an input larger than the cap must not be satisfied")
	}
}

func TestSatisfied_DifferentExtension(t *testing.T) {
	in := probe.Info{Path: "photo.png", Size: 100}
	ok, _ := Satisfied(in, config.Constraints{Under: 1000}, baseSpec()) // spec.Ext = jpg
	if ok {
		t.Error("a format conversion is never a no-op, regardless of size")
	}
}

func TestSatisfied_WidthHeightFPSCeilings(t *testing.T) {
	cases := []struct {
		name string
		in   probe.Info
		c    config.Constraints
		want bool
	}{
		{"width over ceiling", probe.Info{Path: "a.jpg", Size: 1, Width: 2000}, config.Constraints{Under: 1000, Width: 1000}, false},
		{"width under ceiling", probe.Info{Path: "a.jpg", Size: 1, Width: 500}, config.Constraints{Under: 1000, Width: 1000}, true},
		{"height over ceiling", probe.Info{Path: "a.jpg", Size: 1, Height: 2000}, config.Constraints{Under: 1000, Height: 1000}, false},
		{"fps over ceiling", probe.Info{Path: "a.jpg", Size: 1, AvgFPS: 60}, config.Constraints{Under: 1000, FPS: 30}, false},
		{"fps under ceiling", probe.Info{Path: "a.jpg", Size: 1, AvgFPS: 24}, config.Constraints{Under: 1000, FPS: 30}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, _ := Satisfied(c.in, c.c, baseSpec())
			if ok != c.want {
				t.Errorf("Satisfied = %v, want %v", ok, c.want)
			}
		})
	}
}

func TestSatisfied_LoudnormAndCopyVideoAlwaysReencode(t *testing.T) {
	in := probe.Info{Path: "a.jpg", Size: 1}
	if ok, _ := Satisfied(in, config.Constraints{Under: 1000, AudioLoudnorm: true}, baseSpec()); ok {
		t.Error("a loudnorm request must never be satisfied by a pre-existing file")
	}
	if ok, _ := Satisfied(in, config.Constraints{Under: 1000, CopyVideo: true}, baseSpec()); ok {
		t.Error("CopyVideo must never be satisfied by a pre-existing file")
	}
}

func TestSatisfied_AudioMono(t *testing.T) {
	stereo := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioChannels: 2}
	mono := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioChannels: 1}
	c := config.Constraints{Under: 1000, AudioMono: true}

	if ok, _ := Satisfied(stereo, c, baseSpec()); ok {
		t.Error("a stereo input under an audio.mono preset must not be satisfied (this was bug 1.5)")
	}
	if ok, _ := Satisfied(mono, c, baseSpec()); !ok {
		t.Error("an already-mono input under an audio.mono preset should be satisfied")
	}
}

func TestSatisfied_AudioBitrate(t *testing.T) {
	loud := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioBitrate: 256_000}
	quiet := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioBitrate: 96_000}
	c := config.Constraints{Under: 1000, AudioBitrate: 128} // kbps

	if ok, _ := Satisfied(loud, c, baseSpec()); ok {
		t.Error("audio well above the target bitrate must not be satisfied")
	}
	if ok, _ := Satisfied(quiet, c, baseSpec()); !ok {
		t.Error("audio already below the target bitrate should be satisfied")
	}
}

func TestSatisfied_AudioCodec(t *testing.T) {
	aac := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioCodec: "aac"}
	opus := probe.Info{Path: "a.jpg", Size: 1, HasAudio: true, AudioCodec: "opus"}
	c := config.Constraints{Under: 1000, AudioCodec: "opus"}

	if ok, _ := Satisfied(aac, c, baseSpec()); ok {
		t.Error("aac audio under an opus request must not be satisfied")
	}
	if ok, _ := Satisfied(opus, c, baseSpec()); !ok {
		t.Error("opus audio under an opus request should be satisfied")
	}

	// audio.codec = "copy" means "whatever it already is", so no codec value
	// should ever block satisfaction.
	copyC := config.Constraints{Under: 1000, AudioCodec: "copy"}
	if ok, _ := Satisfied(aac, copyC, baseSpec()); !ok {
		t.Error("audio.codec = copy must not require re-encoding regardless of the input's codec")
	}
}

func TestSatisfied_SilentInputIgnoresAudioConstraints(t *testing.T) {
	in := probe.Info{Path: "a.jpg", Size: 1, HasAudio: false}
	c := config.Constraints{Under: 1000, AudioMono: true, AudioBitrate: 64, AudioCodec: "opus"}
	if ok, _ := Satisfied(in, c, baseSpec()); !ok {
		t.Error("audio constraints must not block satisfaction for an input with no audio stream")
	}
}

func TestSatisfied_HDRAlwaysReencodesUnlessTonemapOff(t *testing.T) {
	in := probe.Info{Path: "a.jpg", Size: 1, HDR: true}
	if ok, _ := Satisfied(in, config.Constraints{Under: 1000, Tonemap: "auto"}, baseSpec()); ok {
		t.Error("HDR input must be re-encoded when tonemap is not off")
	}
	if ok, _ := Satisfied(in, config.Constraints{Under: 1000, Tonemap: "off"}, baseSpec()); !ok {
		t.Error("HDR input with tonemap=off should be satisfiable")
	}
}

func TestDecide(t *testing.T) {
	dir := t.TempDir()

	t.Run("does not exist", func(t *testing.T) {
		d, _ := Decide(filepath.Join(dir, "missing.jpg"), "want", false, false)
		if d != Run {
			t.Errorf("Decide = %v, want Run", d)
		}
	})

	t.Run("forced overwrite", func(t *testing.T) {
		p := writeFile(t, dir, "forced.jpg", "not made by fit")
		d, why := Decide(p, "want", true, true)
		if d != Run || why == "" {
			t.Errorf("Decide = %v, %q; want Run with a reason", d, why)
		}
	})

	t.Run("exists without a fit fingerprint", func(t *testing.T) {
		p := writeFile(t, dir, "handmade.jpg", "just a regular file")
		d, _ := Decide(p, "want", true, false)
		if d != Refuse {
			t.Errorf("Decide = %v, want Refuse", d)
		}
	})

	t.Run("fingerprint matches", func(t *testing.T) {
		const fp = "abc123abc123abcd" // fingerprint.Length hex chars
		p := writeFile(t, dir, "current.jpg", fingerprint.Marker(fp))
		d, why := Decide(p, fp, true, false)
		if d != SkipCurrent || why != "already current" {
			t.Errorf("Decide = %v, %q; want SkipCurrent, \"already current\"", d, why)
		}
	})

	t.Run("fingerprint differs", func(t *testing.T) {
		p := writeFile(t, dir, "stale.jpg", fingerprint.Marker("1111111111111111"))
		d, _ := Decide(p, "2222222222222222", true, false)
		if d != Run {
			t.Errorf("Decide = %v, want Run", d)
		}
	})
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTarget(inputPath string, nameTemplate string) *Target {
	return &Target{
		Input:  probe.Info{Path: inputPath},
		Cons:   config.Constraints{Name: nameTemplate},
		Spec:   config.FormatSpec{Ext: "jpg"},
		Tag:    "chat",
		Width:  100,
		Height: 100,
	}
}

func TestResolve_TagDroppedWhenExtensionDiffers(t *testing.T) {
	dir := t.TempDir()
	tgt := newTarget(filepath.Join(dir, "photo.png"), config.DefaultName)
	if err := Resolve([]*Target{tgt}); err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(tgt.Out), "photo.jpg"; got != want {
		t.Errorf("Out = %q, want %q (tag dropped, extension already differs)", got, want)
	}
}

func TestResolve_TagKeptWhenExtensionMatches(t *testing.T) {
	dir := t.TempDir()
	tgt := newTarget(filepath.Join(dir, "clip.jpg"), config.DefaultName)
	if err := Resolve([]*Target{tgt}); err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(tgt.Out), "clip.chat.jpg"; got != want {
		t.Errorf("Out = %q, want %q (tag kept, same extension would otherwise collide)", got, want)
	}
}

// TestResolve_NamingIsBatchIndependent is bug 1.7: naming must not depend on
// which other inputs are in the same run.
func TestResolve_NamingIsBatchIndependent(t *testing.T) {
	dir := t.TempDir()

	alone := newTarget(filepath.Join(dir, "p1.png"), config.DefaultName)
	if err := Resolve([]*Target{alone}); err != nil {
		t.Fatal(err)
	}
	aloneOut := filepath.Base(alone.Out)

	withSibling := newTarget(filepath.Join(dir, "p1.png"), config.DefaultName)
	sibling := newTarget(filepath.Join(dir, "p1.jpeg"), config.DefaultName)
	err := Resolve([]*Target{withSibling, sibling})

	if aloneOut != "p1.jpg" {
		t.Fatalf("sanity: alone.Out = %q, want p1.jpg", aloneOut)
	}
	// The batch with a sibling must be refused outright (a hard collision),
	// not silently renamed to a different filename than the solo run used.
	if err == nil {
		t.Fatal("expected a collision error when a sibling would produce the same untagged name")
	}
	if _, ok := errors.AsType[*CollisionError](err); !ok {
		t.Fatalf("error is %T, want *CollisionError", err)
	}
}

func TestResolve_CollisionBetweenTwoInputs(t *testing.T) {
	dir := t.TempDir()
	a := newTarget(filepath.Join(dir, "a.jpg"), "same.{ext}")
	b := newTarget(filepath.Join(dir, "b.jpg"), "same.{ext}")
	err := Resolve([]*Target{a, b})
	if err == nil {
		t.Fatal("expected a collision error")
	}
	if !strings.Contains(err.Error(), "same.jpg") {
		t.Errorf("error %q does not name the colliding output", err)
	}
}

func TestResolve_OutputEqualsInput(t *testing.T) {
	dir := t.TempDir()
	// Template renders to exactly the input's own name.
	tgt := newTarget(filepath.Join(dir, "photo.jpg"), "{stem}.{ext}")
	err := Resolve([]*Target{tgt})
	if err == nil {
		t.Fatal("expected an error when an output path equals an input path")
	}
}

func TestResolve_CaseInsensitiveCollisionOnDarwin(t *testing.T) {
	if key("A") != key("a") {
		t.Skip("this check only applies on the case-insensitive platforms fit special-cases")
	}
	dir := t.TempDir()
	a := newTarget(filepath.Join(dir, "Photo.PNG"), "out.{ext}")
	b := newTarget(filepath.Join(dir, "photo.png"), "OUT.{ext}")
	err := Resolve([]*Target{a, b})
	if err == nil {
		t.Fatal("expected out.jpg and OUT.jpg to collide on a case-insensitive filesystem")
	}
}

func TestRender_HashComputedOnce(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "input.bin", "some content")
	tgt := &Target{
		Input: probe.Info{Path: p},
		Cons:  config.Constraints{Name: "{stem}-{hash}.{ext}"},
		Spec:  config.FormatSpec{Ext: "bin"},
	}
	h1, err := tgt.contentHash()
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the file; if contentHash recomputed, it would see new content.
	if err := os.WriteFile(p, []byte("different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := tgt.contentHash()
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Error("contentHash recomputed on a second call instead of using the cached value")
	}
}

func TestDropTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"{stem}.{tag}.{ext}", "{stem}.{ext}"},
		{"{stem}-{tag}.{ext}", "{stem}.{ext}"},
		{"{stem}_{tag}.{ext}", "{stem}.{ext}"},
		{"{stem}{tag}.{ext}", "{stem}.{ext}"},
		{"{stem}.{ext}", "{stem}.{ext}"}, // no tag present, unchanged
	}
	for _, c := range cases {
		if got := dropTag(c.in); got != c.want {
			t.Errorf("dropTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
