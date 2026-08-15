// Package fingerprint records how an output was made, inside the output
// itself. There is no database: a fingerprint in the file survives renaming
// and moving, and answers "how was this made" by reading the file.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/rstnk/fit/internal/config"
)

// Prefix marks a fingerprint wherever it is stored.
const Prefix = "fit:"

// Length is how many hex characters of the digest are kept.
const Length = 16

// scanWindow is how much of each end of a file is searched for the marker.
// Container metadata lives at one end or the other.
const scanWindow = 256 << 10

var markerRE = regexp.MustCompile(`fit:[0-9a-f]{` + fmt.Sprint(Length) + `}`)

// Compute hashes the resolved constraints together with the input's size and
// modification time. Hashing the constraints is the point: editing a preset's
// cap makes every output made under the old cap stale, which an mtime
// comparison cannot see.
func Compute(c config.Constraints, inputSize int64, inputModTime time.Time) string {
	h := sha256.New()
	fmt.Fprintln(h, c.Canonical())
	fmt.Fprintf(h, "input_size=%d\ninput_mtime=%d\n", inputSize, inputModTime.UnixNano())
	return hex.EncodeToString(h.Sum(nil))[:Length]
}

// Marker is the string embedded in an output's metadata.
func Marker(fp string) string { return Prefix + fp }

// ContentHash is the first 12 hex characters of the SHA-256 of a file's
// contents, for the {hash} name template variable.
func ContentHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// Read looks for a fit fingerprint in an existing output. Formats carry the
// marker in different places (a JPEG comment, a PNG text chunk, an XMP packet,
// an MP4 udta atom), all of which sit at one end of the file, so scanning both
// ends finds it without spending a process on the common case.
//
// Embedded cover art breaks that assumption. A picture shares the metadata
// region with the tags and displaces them by its own size, which is routinely
// a megabyte or more: an MP3's marker stays at the head, but FLAC writes the
// picture block ahead of the comment and MP4 leaves it sitting between the
// comment atom and the end. No fixed window is safe against a cover that can
// be arbitrarily large, so a miss falls back to asking ffprobe outright.
func Read(path string) (string, bool) {
	if fp, ok := scan(path); ok {
		return fp, true
	}
	return readTag(path)
}

func scan(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	size := st.Size()

	if fp, ok := scanAt(f, 0, min(size, scanWindow)); ok {
		return fp, true
	}
	if size > scanWindow {
		off := size - scanWindow
		if fp, ok := scanAt(f, off, scanWindow); ok {
			return fp, true
		}
	}
	return "", false
}

// readTag asks ffprobe for the comment tag wherever the container keeps it.
// This is the exact answer the scan approximates, and costs a process, so it
// runs only once the cheap path has already missed.
func readTag(path string) (string, bool) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format_tags=comment:stream_tags=comment",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return "", false
	}
	m := markerRE.Find(out)
	if m == nil {
		return "", false
	}
	return string(m[len(Prefix):]), true
}

func scanAt(f *os.File, off, n int64) (string, bool) {
	if n <= 0 {
		return "", false
	}
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, off)
	if read == 0 && err != nil {
		return "", false
	}
	m := markerRE.Find(buf[:read])
	if m == nil {
		return "", false
	}
	return string(m[len(Prefix):]), true
}
