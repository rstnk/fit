package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/probe"
	"github.com/rstnk/fit/internal/solve"
)

func cmdInfo(ctx context.Context, o options, files []string) int {
	if len(files) == 0 {
		return fail(fmt.Errorf("info needs at least one file"))
	}
	infos := probeAll(ctx, files, o.jobs)

	if o.asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, in := range infos {
			_ = enc.Encode(infoRecord(in))
		}
		return exitOK
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tKIND\tSIZE\tDIMENSIONS\tDURATION\tBITRATE\tAUDIO\tNOTES")
	for _, in := range infos {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			displayName(in.Path),
			in.Kind,
			config.FormatSize(in.Size),
			dims(in),
			duration(in),
			bitrate(in),
			audioDesc(in),
			strings.Join(notes(in), ", "),
		)
	}
	tw.Flush()
	return exitOK
}

type infoJSON struct {
	probe.Info
	Fingerprint string `json:"fingerprint,omitempty"`
}

func infoRecord(in probe.Info) infoJSON {
	rec := infoJSON{Info: in}
	if fp, ok := fingerprint.Read(in.Path); ok {
		rec.Fingerprint = fp
	}
	return rec
}

func dims(in probe.Info) string {
	if in.Width == 0 || in.Height == 0 {
		return "-"
	}
	return fmt.Sprintf("%dx%d", in.Width, in.Height)
}

func duration(in probe.Info) string {
	if in.Duration <= 0 {
		return "-"
	}
	return solve.Duration(in.Duration)
}

// bitrate stays in kbps at every magnitude. The column's job is comparison
// between rows, and switching to Mbps partway down puts 1.6 next to 551 with
// nothing to say which is larger.
func bitrate(in probe.Info) string {
	if in.Bitrate <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dk", in.Bitrate/1000)
}

func audioDesc(in probe.Info) string {
	if !in.HasAudio {
		return "-"
	}
	s := in.AudioCodec
	if in.AudioChannels == 1 {
		s += " mono"
	}
	return s
}

func notes(in probe.Info) []string {
	var out []string
	if in.Note != "" {
		out = append(out, in.Note)
	}
	if in.Kind == probe.KindImage && in.NBFrames == 1 && in.Format == "gif" {
		out = append(out, "first frame only")
	}
	if in.CoverWidth > 0 && in.CoverHeight > 0 {
		out = append(out, fmt.Sprintf("cover %dx%d", in.CoverWidth, in.CoverHeight))
	}
	if in.HDR {
		hdr := "hdr"
		switch in.ColorTransfer {
		case "arib-std-b67":
			hdr = "hdr hlg"
		case "smpte2084":
			hdr = "hdr pq"
		}
		out = append(out, hdr)
	}
	if in.DoViProfile > 0 {
		out = append(out, fmt.Sprintf("dolby vision profile %d", in.DoViProfile))
	}
	if in.VFR {
		out = append(out, fmt.Sprintf("vfr (timebase %.0f)", in.TimebaseFPS))
	}
	if in.ScreenCapture {
		out = append(out, "screen capture")
	}
	if in.Rotation != 0 {
		out = append(out, fmt.Sprintf("rotated %d", in.Rotation))
	}
	if fp, ok := fingerprint.Read(in.Path); ok {
		out = append(out, "made by fit ("+fp+")")
	}
	return out
}

func cmdLs(o options) int {
	cfg, err := config.Load(o.confPath)
	if err != nil {
		return fail(err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PRESET\tABOUT\tCONSTRAINTS")
	for _, name := range cfg.Names() {
		p := cfg.Presets[name]
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, p.About, summarise(p.Base))
	}
	tw.Flush()
	fmt.Fprintf(os.Stdout, "\n%s\n", cfg.Path)
	return exitOK
}

func summarise(s config.Set) string {
	var parts []string
	if s.Under != nil {
		parts = append(parts, "under="+config.FormatSize(*s.Under))
	}
	if s.Width != nil {
		parts = append(parts, fmt.Sprintf("width=%d", *s.Width))
	}
	if s.Height != nil {
		parts = append(parts, fmt.Sprintf("height=%d", *s.Height))
	}
	if s.Format != nil {
		parts = append(parts, "format="+*s.Format)
	}
	if s.Quality != nil {
		parts = append(parts, fmt.Sprintf("quality=%d", *s.Quality))
	}
	if s.AudioLoudnorm != nil && *s.AudioLoudnorm {
		parts = append(parts, "audio_loudnorm")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}
