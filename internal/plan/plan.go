// Package plan works out where every output goes and refuses the whole batch
// before any encoding when two of them would collide or one would land on an
// input.
package plan

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/probe"
)

// Target is one input on its way to one output.
type Target struct {
	Input probe.Info
	Cons  config.Constraints
	Spec  config.FormatSpec
	Tag   string

	// Width and Height are the solved output parameters, available to the
	// name template.
	Width, Height int

	OutDir string
	Out    string

	// hash caches ContentHash for the {hash} template variable, computed at
	// most once even though Resolve may render a target's name twice.
	hash    string
	hashSet bool
}

// Resolve fills in Out for every target and refuses the batch if the set of
// output paths is unsafe.
//
// The tag-drop decision is made per target, not per batch: the same input
// under the same preset always names its output the same way, whether it
// runs alone or alongside others. A name that collides because of that is a
// hard error from check() below, not a silent fallback to the tagged form,
// since a silent fallback is what let two different batches of the same
// input write two different filenames for the same output.
func Resolve(targets []*Target) error {
	for _, t := range targets {
		tagged, err := Render(t, true)
		if err != nil {
			return err
		}
		out := tagged
		// The tag is only worth dropping when the output extension already
		// differs from the input's, since the name is unambiguous without it.
		if !strings.EqualFold(inputExt(t.Input.Path), t.Spec.Ext) {
			untagged, err := Render(t, false)
			if err != nil {
				return err
			}
			if untagged != tagged {
				out = untagged
			}
		}
		t.Out = out
	}
	return check(targets)
}

func check(targets []*Target) error {
	inputNames := map[string]string{}
	for _, t := range targets {
		inputNames[key(t.Input.Path)] = filepath.Base(t.Input.Path)
	}

	byOut := map[string][]*Target{}
	var order []string
	for _, t := range targets {
		k := key(t.Out)
		if _, seen := byOut[k]; !seen {
			order = append(order, k)
		}
		byOut[k] = append(byOut[k], t)
	}

	var groups []Collision
	for _, k := range order {
		ts := byOut[k]
		onInput, isInput := inputNames[k]
		if len(ts) == 1 && !isInput {
			continue
		}
		srcs := map[string]bool{}
		for _, t := range ts {
			srcs[filepath.Base(t.Input.Path)] = true
		}
		if isInput {
			srcs[onInput] = true
		}
		names := make([]string, 0, len(srcs))
		for n := range srcs {
			names = append(names, n)
		}
		sort.Strings(names)
		groups = append(groups, Collision{Out: filepath.Base(ts[0].Out), Inputs: names})
	}
	if len(groups) == 0 {
		return nil
	}
	return &CollisionError{Groups: groups}
}

// Collision is one output path that more than one file wants.
type Collision struct {
	Out    string
	Inputs []string
}

// CollisionError refuses the batch before any work happens.
type CollisionError struct{ Groups []Collision }

func (e *CollisionError) Error() string {
	var b strings.Builder
	b.WriteString("unsafe output paths, nothing was processed")
	for _, g := range e.Groups {
		fmt.Fprintf(&b, "\n  %s  ←  %s", g.Out, strings.Join(g.Inputs, ", "))
	}
	return b.String()
}

// Render builds one output path from the target's name template.
func Render(t *Target, withTag bool) (string, error) {
	tmpl := t.Cons.Name
	if !withTag {
		tmpl = dropTag(tmpl)
	}

	base := filepath.Base(t.Input.Path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	out := tmpl
	repl := map[string]string{
		"{stem}":   stem,
		"{ext}":    t.Spec.Ext,
		"{tag}":    t.Tag,
		"{width}":  fmt.Sprint(t.Width),
		"{height}": fmt.Sprint(t.Height),
		"{date}":   time.Now().Format("2006-01-02"),
	}
	if strings.Contains(out, "{hash}") {
		h, err := t.contentHash()
		if err != nil {
			return "", fmt.Errorf("hashing %s: %w", t.Input.Path, err)
		}
		repl["{hash}"] = h
	}
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	dir := t.OutDir
	if dir == "" {
		dir = filepath.Dir(t.Input.Path)
	}
	return filepath.Join(dir, out), nil
}

// contentHash returns the input's content hash, computing it at most once
// even if the target's name is rendered more than once.
func (t *Target) contentHash() (string, error) {
	if !t.hashSet {
		h, err := fingerprint.ContentHash(t.Input.Path)
		if err != nil {
			return "", err
		}
		t.hash, t.hashSet = h, true
	}
	return t.hash, nil
}

func dropTag(tmpl string) string {
	for _, sep := range []string{".{tag}", "-{tag}", "_{tag}", "{tag}"} {
		if strings.Contains(tmpl, sep) {
			return strings.Replace(tmpl, sep, "", 1)
		}
	}
	return tmpl
}

// Decision is what to do about an output path that may already exist.
type Decision int

const (
	// Run encodes and writes the output.
	Run Decision = iota
	// SkipCurrent leaves an output that was made the same way alone.
	SkipCurrent
	// Refuse stops rather than overwrite a file fit did not produce.
	Refuse
)

// Decide reads any fingerprint already at the output path and compares it with
// the one this run would write.
func Decide(out, want string, exists, force bool) (Decision, string) {
	if !exists {
		return Run, ""
	}
	if force {
		return Run, "forced overwrite"
	}
	got, ok := fingerprint.Read(out)
	switch {
	case !ok:
		return Refuse, "exists and was not made by fit, pass -f to overwrite"
	case got == want:
		return SkipCurrent, "already current"
	default:
		return Run, "fingerprint differs, remaking"
	}
}

// Satisfied reports whether an input already meets every constraint, in which
// case it is left untouched rather than re-encoded into a copy of itself.
func Satisfied(in probe.Info, c config.Constraints, spec config.FormatSpec) (bool, string) {
	if c.Under == 0 || in.Size > c.Under {
		return false, ""
	}
	if !strings.EqualFold(inputExt(in.Path), spec.Ext) {
		return false, ""
	}
	if c.Width > 0 && in.Width > c.Width {
		return false, ""
	}
	if c.Height > 0 && in.Height > c.Height {
		return false, ""
	}
	if c.FPS > 0 && in.AvgFPS > c.FPS {
		return false, ""
	}
	if c.AudioLoudnorm || c.CopyVideo {
		return false, ""
	}
	// Audio work the preset asked for is invisible in the fields checked
	// above, so a file already under the size cap would otherwise be waved
	// through with its mono-down, bitrate or codec request never applied.
	if in.HasAudio {
		if c.AudioMono && in.AudioChannels > 1 {
			return false, ""
		}
		if c.AudioBitrate > 0 && in.AudioBitrate > c.AudioBitrate*1000 {
			return false, ""
		}
		if c.AudioCodec != "" && !strings.EqualFold(c.AudioCodec, "copy") &&
			!strings.EqualFold(c.AudioCodec, in.AudioCodec) {
			return false, ""
		}
	}
	if in.HDR && c.Tonemap != "off" {
		return false, ""
	}
	return true, fmt.Sprintf("%s, under the %s cap",
		config.FormatSize(in.Size), config.FormatSize(c.Under))
}

func inputExt(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

// key normalises a path the way the filesystem compares it, so Photo.JPG and
// photo.jpg are one file where the filesystem says they are.
func key(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}
