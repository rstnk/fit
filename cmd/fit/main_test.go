package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/rstnk/fit/internal/config"
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
