package encode

import (
	"slices"
	"testing"

	"github.com/rstnk/fit/internal/config"
	"github.com/rstnk/fit/internal/plan"
	"github.com/rstnk/fit/internal/probe"
)

func imageTarget(format string, strip string) *plan.Target {
	spec, err := config.LookupFormat(probe.KindImage, format)
	if err != nil {
		panic(err)
	}
	return &plan.Target{
		Input: probe.Info{Path: "in.png", Width: 800, Height: 600},
		Cons:  config.Constraints{Strip: strip},
		Spec:  spec,
		Out:   "out." + spec.Ext,
	}
}

// TestImageArgs_QualitySkippedForLosslessFormats is the regression test for
// bug 1.4: PNG's -quality means something ImageMagick-specific and unrelated
// to what the search believed it was bisecting, so it must not be sent at
// all for a lossless format.
func TestImageArgs_QualitySkippedForLosslessFormats(t *testing.T) {
	e := newEncoder()

	png := imageTarget("png", "all")
	args := e.imageArgs(Job{Target: png}, "src.mpc", 90, "", "out.png")
	if slices.Contains(args, "-quality") {
		t.Errorf("imageArgs(png) = %v, must not include -quality for a lossless format", args)
	}

	jpeg := imageTarget("jpeg", "all")
	args2 := e.imageArgs(Job{Target: jpeg}, "src.mpc", 82, "", "out.jpg")
	if !hasFlagValue(args2, "-quality", "82") {
		t.Errorf("imageArgs(jpeg) = %v, want -quality 82 for a lossy format", args2)
	}
}

func TestImageArgs_StripFlag(t *testing.T) {
	e := newEncoder()
	stripped := imageTarget("jpeg", "all")
	args := e.imageArgs(Job{Target: stripped}, "src.mpc", 90, "", "out.jpg")
	if !slices.Contains(args, "-strip") {
		t.Errorf("imageArgs = %v, want -strip when Cons.Strip = all", args)
	}

	kept := imageTarget("jpeg", "none")
	args2 := e.imageArgs(Job{Target: kept}, "src.mpc", 90, "", "out.jpg")
	if slices.Contains(args2, "-strip") {
		t.Errorf("imageArgs = %v, must not strip when Cons.Strip = none", args2)
	}
}

func TestImageArgs_FingerprintEmbeddedAsComment(t *testing.T) {
	e := newEncoder()
	tgt := imageTarget("jpeg", "all")
	j := Job{Target: tgt, FP: "cafebabecafebabe"}
	args := e.imageArgs(j, "src.mpc", 90, "", "out.jpg")
	if !hasFlagValue(args, "-set", "comment") {
		t.Fatalf("imageArgs = %v, want -set comment ...", args)
	}
	// The comment value is the argument right after "comment".
	found := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "comment" && args[i+1] == "fit:cafebabecafebabe" {
			found = true
		}
	}
	if !found {
		t.Errorf("imageArgs = %v, want the comment value to be fit:cafebabecafebabe", args)
	}
}

func TestImageArgs_XMPProfileForFormatsThatDropComments(t *testing.T) {
	e := newEncoder()
	tgt := imageTarget("webp", "all")
	args := e.imageArgs(Job{Target: tgt}, "src.mpc", 90, "fp.xmp", "out.webp")
	if !hasFlagValue(args, "-profile", "fp.xmp") {
		t.Errorf("imageArgs = %v, want -profile fp.xmp when an XMP path is supplied", args)
	}

	noXMP := e.imageArgs(Job{Target: tgt}, "src.mpc", 90, "", "out.webp")
	if slices.Contains(noXMP, "-profile") {
		t.Errorf("imageArgs = %v, must not add -profile when no XMP path is supplied", noXMP)
	}
}

func TestNeedsXMP(t *testing.T) {
	for _, ext := range []string{"webp", "avif", "heic"} {
		if !needsXMP(ext) {
			t.Errorf("needsXMP(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{"jpg", "png", "tiff"} {
		if needsXMP(ext) {
			t.Errorf("needsXMP(%q) = true, want false", ext)
		}
	}
}

func TestScaled(t *testing.T) {
	if w, h := scaled(1000, 500, 1); w != 1000 || h != 500 {
		t.Errorf("scaled at 1.0 = %d,%d, want unchanged 1000,500", w, h)
	}
	if w, h := scaled(1000, 500, 0.5); w != 500 || h != 250 {
		t.Errorf("scaled at 0.5 = %d,%d, want 500,250", w, h)
	}
	if w, h := scaled(1, 1, 0.01); w < 1 || h < 1 {
		t.Errorf("scaled must never floor to zero, got %d,%d", w, h)
	}
}
