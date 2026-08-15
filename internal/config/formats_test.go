package config

import (
	"testing"

	"github.com/rstnk/fit/internal/probe"
)

func TestLookupFormat(t *testing.T) {
	spec, err := LookupFormat(probe.KindVideo, "mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.VideoCodec != "libx264" || spec.AudioCodec != "aac" {
		t.Errorf("mp4 spec = %+v, want libx264/aac", spec)
	}

	if _, err := LookupFormat(probe.KindVideo, "notaformat"); err == nil {
		t.Error("expected an error for an unknown format")
	}
	if _, err := LookupFormat(probe.KindImage, "mp4"); err == nil {
		t.Error("expected an error looking up a video format under image")
	}
}

func TestLookupFormat_CaseInsensitive(t *testing.T) {
	if _, err := LookupFormat(probe.KindImage, "JPEG"); err != nil {
		t.Errorf("expected case-insensitive lookup to succeed: %v", err)
	}
}

func TestFormatSpec_Lossy(t *testing.T) {
	cases := []struct {
		name  string
		lossy bool
	}{
		{"jpeg", true},
		{"webp", true},
		{"avif", true},
		{"heic", true},
		{"png", false},
		{"tiff", false},
	}
	for _, c := range cases {
		spec, err := LookupFormat(probe.KindImage, c.name)
		if err != nil {
			t.Fatalf("LookupFormat(%q): %v", c.name, err)
		}
		if spec.Lossy != c.lossy {
			t.Errorf("%s: Lossy = %v, want %v", c.name, spec.Lossy, c.lossy)
		}
	}
}

func TestAudioCodecOverride(t *testing.T) {
	base, err := LookupFormat(probe.KindVideo, "webm")
	if err != nil {
		t.Fatal(err)
	}
	if base.AudioCodec != "libopus" {
		t.Fatalf("webm default audio codec = %q, want libopus", base.AudioCodec)
	}

	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"", "libopus", false}, // unset keeps the container's default
		{"aac", "aac", false},  // explicit request overrides the default, unlike unset
		{"opus", "libopus", false},
		{"libopus", "libopus", false},
		{"mp3", "libmp3lame", false},
		{"vorbis", "libvorbis", false},
		{"copy", "copy", false},
		{"flac", "flac", false},
		{"AAC", "aac", false}, // case-insensitive
		{"notacodec", "", true},
	}
	for _, c := range cases {
		got, err := base.AudioCodecOverride(c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("AudioCodecOverride(%q) = %+v, nil; want an error", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("AudioCodecOverride(%q) unexpected error: %v", c.name, err)
			continue
		}
		if got.AudioCodec != c.want {
			t.Errorf("AudioCodecOverride(%q).AudioCodec = %q, want %q", c.name, got.AudioCodec, c.want)
		}
	}
}
