package fingerprint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const testFP = "0123456789abcdef"

// writeAt builds a file of size bytes with the marker planted at off.
func writeAt(t *testing.T, size, off int) string {
	t.Helper()
	buf := bytes.Repeat([]byte{'x'}, size)
	copy(buf[off:], Marker(testFP))
	path := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScan_FindsMarkerAtEitherEnd(t *testing.T) {
	size := scanWindow*3 + 1000
	for _, c := range []struct {
		name string
		off  int
	}{
		{"head", 100},
		{"tail", size - 100},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := scan(writeAt(t, size, c.off))
			if !ok || got != testFP {
				t.Errorf("scan = %q, %v; want %q, true", got, ok, testFP)
			}
		})
	}
}

// TestScan_MissesMarkerInTheMiddle pins the limitation the ffprobe fallback
// exists to cover. Cover art shares the metadata region with the tags and
// pushes them in by its own size, which is routinely past both windows.
func TestScan_MissesMarkerInTheMiddle(t *testing.T) {
	size := scanWindow * 3
	if _, ok := scan(writeAt(t, size, size/2)); ok {
		t.Error("scan found a marker beyond both windows; the fallback test below is then moot")
	}
}

func TestScan_SmallFileIsSearchedWhole(t *testing.T) {
	got, ok := scan(writeAt(t, 2048, 1000))
	if !ok || got != testFP {
		t.Errorf("scan = %q, %v; want %q, true", got, ok, testFP)
	}
}

func TestRead_MissingFile(t *testing.T) {
	if got, ok := Read(filepath.Join(t.TempDir(), "absent.mp3")); ok {
		t.Errorf("Read(absent) = %q, true; want \"\", false", got)
	}
}

// TestRead_CoverArtDisplacedTag is the regression test for fit failing to
// recognise its own output: a FLAC writes its picture block ahead of the
// comment, so a cover larger than the scan window leaves the marker
// unreachable by bytes alone and only ffprobe can still find it.
func TestRead_CoverArtDisplacedTag(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()

	// Noise rather than a flat colour, so the cover does not compress down to
	// less than the scan window it has to exceed.
	cover := filepath.Join(dir, "cover.png")
	run(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi",
		"-i", "nullsrc=s=1400x1400,geq=random(1)*255:128:128", "-frames:v", "1", cover)
	if st, err := os.Stat(cover); err != nil || st.Size() <= scanWindow {
		t.Skipf("generated cover is %d bytes, not larger than the %d byte window", st.Size(), scanWindow)
	}

	// Noise for the audio too, and enough of it. FLAC of a sine wave is tiny,
	// which would leave the marker within a window of the file's end and the
	// byte scan would reach it after all, testing nothing.
	out := filepath.Join(dir, "tagged.flac")
	run(t, ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "anoisesrc=duration=20:sample_rate=44100",
		"-i", cover, "-map", "0:a:0", "-map", "1:v:0",
		"-c:v", "copy", "-disposition:v:0", "attached_pic", "-c:a", "flac",
		"-metadata", "comment="+Marker(testFP), out)

	if _, ok := scan(out); ok {
		st, _ := os.Stat(out)
		t.Fatalf("byte scan still reaches the marker in a %d byte fixture, so the "+
			"ffprobe fallback goes unexercised: grow the cover or the audio", st.Size())
	}
	got, ok := Read(out)
	if !ok || got != testFP {
		t.Errorf("Read = %q, %v; want %q, true", got, ok, testFP)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v: %s", name, err, stderr.String())
	}
}
