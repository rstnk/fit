package encode

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/fingerprint"
	"github.com/rstnk/fit/internal/solve"
)

// xmpTemplate carries the fingerprint in formats whose ImageMagick delegate
// drops a plain comment, which is every one of webp, avif and heic.
const xmpTemplate = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:description><rdf:Alt><rdf:li xml:lang="x-default">%s</rdf:li></rdf:Alt></dc:description>
</rdf:Description></rdf:RDF></x:xmpmeta>
<?xpacket end="w"?>
`

func needsXMP(ext string) bool {
	switch ext {
	case "webp", "avif", "heic":
		return true
	}
	return false
}

// EncodeImage decodes and scales once into a temporary intermediate, then runs
// the quality search against that intermediate, so the expensive decode is
// paid for a single time.
func (e *Encoder) EncodeImage(ctx context.Context, j Job) (Result, error) {
	t := j.Target

	ws, err := e.workspace(t.Out)
	if err != nil {
		return Result{}, err
	}
	defer ws.close()

	res := Result{Width: t.Width, Height: t.Height}

	var xmp string
	if needsXMP(t.Spec.Ext) {
		xmp = ws.path("fp.xmp")
		if !e.DryRun {
			body := fmt.Appendf(nil, xmpTemplate, fingerprint.Marker(j.FP))
			if err := os.WriteFile(xmp, body, 0o600); err != nil {
				return res, err
			}
		}
	}

	inter := ws.path("inter.mpc")
	prep := []string{t.Input.Path + "[0]", "-auto-orient"}
	if t.Width > 0 && t.Height > 0 && (t.Width != t.Input.Width || t.Height != t.Input.Height) {
		prep = append(prep, "-resize", fmt.Sprintf("%dx%d", t.Width, t.Height))
	}
	prep = append(prep, inter)
	res.Commands = append(res.Commands, cmdline(e.Magick, prep))
	if err := e.exec(ctx, e.Magick, prep...); err != nil {
		return res, err
	}

	search := solve.NewImageSearch(solve.ImageConstraints{
		Under:   t.Cons.Under,
		Quality: t.Cons.Quality,
		Lossy:   t.Spec.Lossy,
	})

	best := ws.path("best." + t.Spec.Ext)
	round := 0
	source := inter
	haveBest := false

	for {
		att, ok := search.Next()
		if !ok {
			break
		}

		if att.Round != round {
			round = att.Round
			source = ws.path(fmt.Sprintf("inter%d.mpc", round))
			shrink := []string{inter, "-resize",
				strconv.FormatFloat(att.Scale*100, 'f', 2, 64) + "%", source}
			res.Commands = append(res.Commands, cmdline(e.Magick, shrink))
			if err := e.exec(ctx, e.Magick, shrink...); err != nil {
				return res, err
			}
			e.note("%s: quality floor missed the cap, rescaling to %.0f%%",
				t.Input.Path, att.Scale*100)
		}

		try := ws.path(fmt.Sprintf("try%d-%d.%s", att.Round, att.Quality, t.Spec.Ext))
		args := e.imageArgs(j, source, att.Quality, xmp, try)
		res.Commands = append(res.Commands, cmdline(e.Magick, args))
		if err := e.exec(ctx, e.Magick, args...); err != nil {
			return res, err
		}

		if e.DryRun {
			// Without a real file there is nothing to measure, so the search
			// stops after showing the first encode it would run.
			if t.Spec.Lossy {
				res.Quality = att.Quality
			}
			return res, nil
		}

		size := fileSize(try)
		e.note("%s: %s produced %s", t.Input.Path, att, config.FormatSize(size))
		if t.Cons.Under == 0 || size <= t.Cons.Under {
			if err := os.Rename(try, best); err != nil {
				return res, err
			}
			haveBest = true
			if t.Spec.Lossy {
				res.Quality = att.Quality
			}
			res.Size = size
			res.Width, res.Height = scaled(t.Width, t.Height, att.Scale)
		}
		search.Record(size)
	}

	if !haveBest {
		return res, fmt.Errorf("cannot reach %s: still over after %d rescales at quality %d",
			config.FormatSize(t.Cons.Under), solve.MaxRescales, solve.QualityFloor)
	}
	return res, publish(best, t.Out)
}

func (e *Encoder) imageArgs(j Job, source string, quality int, xmp, out string) []string {
	t := j.Target
	a := []string{source}
	if t.Cons.Strip == "all" {
		// Stripping removes the EXIF orientation tag along with the GPS
		// coordinates, which is why -auto-orient already ran on the source.
		a = append(a, "-strip")
	}
	if t.Spec.Lossy {
		a = append(a, "-quality", strconv.Itoa(quality))
	}
	a = append(a, "-set", "comment", fingerprint.Marker(j.FP))
	if xmp != "" {
		a = append(a, "-profile", xmp)
	}
	return append(a, out)
}

func (e *Encoder) note(format string, args ...any) {
	if e.Verbose {
		e.Print(fmt.Sprintf(format, args...))
	}
}

func scaled(w, h int, scale float64) (int, int) {
	if scale >= 1 {
		return w, h
	}
	return max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
}
