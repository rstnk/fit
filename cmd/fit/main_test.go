package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/ui"
)

func testConfig(names ...string) *config.Config {
	cfg := &config.Config{Presets: map[string]*config.Preset{}}
	for _, n := range names {
		cfg.Presets[n] = &config.Preset{Name: n}
	}
	return cfg
}

func TestResolvePreset(t *testing.T) {
	cases := []struct {
		name      string
		presets   []string
		target    string
		noPreset  bool
		pos       []string
		wantName  string
		wantFiles []string
	}{
		{
			"named in the positional slot",
			[]string{"chat", "default"}, "", false, []string{"chat", "a.png"},
			"chat", []string{"a.png"},
		},
		{
			"-t wins over the positional slot",
			[]string{"chat", "web", "default"}, "web", false, []string{"chat", "a.png"},
			"web", []string{"chat", "a.png"},
		},
		{
			"default fills in when none is named",
			[]string{"chat", "default"}, "", false, []string{"a.png"},
			"default", []string{"a.png"},
		},
		{
			// Without a default preset the behaviour is what it always was:
			// no preset, and bare constraints or a usage error downstream.
			"no default defined",
			[]string{"chat"}, "", false, []string{"a.png"},
			"", []string{"a.png"},
		},
		{
			// An unknown -t stays put so the caller reports it by name rather
			// than silently running something the user did not ask for.
			"unknown -t is not replaced by default",
			[]string{"default"}, "typo", false, []string{"a.png"},
			"typo", []string{"a.png"},
		},
		{
			"a file sharing a preset's name is still consumed as the preset",
			[]string{"chat"}, "", false, []string{"chat"},
			"chat", []string{},
		},
		{
			"default applies with no files, leaving the usage error to the caller",
			[]string{"default"}, "", false, nil,
			"default", nil,
		},
		{
			// The whole point of the flag: back to the built-in defaults even
			// though a default preset is sitting there.
			"--no-preset skips the default",
			[]string{"chat", "default"}, "", true, []string{"a.png"},
			"", []string{"a.png"},
		},
		{
			// With presets off, nothing is a preset name, so a file that
			// happens to share one is left alone as a file.
			"--no-preset takes every positional as a file",
			[]string{"chat", "default"}, "", true, []string{"chat", "a.png"},
			"", []string{"chat", "a.png"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, files, err := resolvePreset(testConfig(c.presets...), c.target, c.noPreset, c.pos)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.wantName {
				t.Errorf("preset = %q, want %q", got, c.wantName)
			}
			if !slices.Equal(files, c.wantFiles) {
				t.Errorf("files = %v, want %v", files, c.wantFiles)
			}
		})
	}
}

// TestProbeAll_InterruptedSlotsStayIdentifiable covers Ctrl-C landing mid-probe.
// The worker pool stops filling the slice, and a zero Info has an empty Kind,
// which is not KindUnknown: unfilled slots sailed past the unknown-kind guard in
// planInputs and came out as failures with no filename attached.
func TestProbeAll_InterruptedSlotsStayIdentifiable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	files := []string{"a.jpg", "b.png"}
	infos := probeAll(ctx, files, 4)
	if len(infos) != len(files) {
		t.Fatalf("probeAll returned %d infos for %d files", len(infos), len(files))
	}
	for i, in := range infos {
		if in.Path != files[i] {
			t.Errorf("infos[%d].Path = %q, want %q", i, in.Path, files[i])
		}
		if in.Kind != probe.KindUnknown {
			t.Errorf("infos[%d].Kind = %q, want %q", i, in.Kind, probe.KindUnknown)
		}
	}

	jobs, early := planInputs(infos, options{}, nil, "", config.Set{})
	if len(jobs) != 0 {
		t.Errorf("planInputs queued %d jobs from an interrupted probe", len(jobs))
	}
	for _, rec := range early {
		if rec.Status != ui.StatusSkip {
			t.Errorf("record for %q has status %q, want a skip", rec.Input, rec.Status)
		}
		if rec.Input == "" {
			t.Error("a record came out with no filename to show the user")
		}
	}
}

// TestResolvePreset_NoPresetWithTarget refuses a command line that both names
// a preset and asks for none, rather than silently honouring one of them.
func TestResolvePreset_NoPresetWithTarget(t *testing.T) {
	_, _, err := resolvePreset(testConfig("chat"), "chat", true, []string{"a.png"})
	if err == nil {
		t.Fatal("expected --no-preset with -t to be refused")
	}
	if !strings.Contains(err.Error(), "--no-preset") {
		t.Errorf("error = %q, want it to name the conflicting flag", err)
	}
}
