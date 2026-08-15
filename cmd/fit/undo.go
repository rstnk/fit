package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// lastRunPath is a single file listing the paths the most recent invocation
// wrote. One file, not a database.
func lastRunPath() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "fit", "last-run")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".fit", "last-run")
	}
	return filepath.Join(home, ".local", "state", "fit", "last-run")
}

func writeLastRun(paths []string) error {
	p := lastRunPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	abs := make([]string, 0, len(paths))
	for _, s := range paths {
		a, err := filepath.Abs(s)
		if err != nil {
			a = s
		}
		abs = append(abs, a)
	}
	return os.WriteFile(p, []byte(strings.Join(abs, "\n")+"\n"), 0o644)
}

func cmdUndo(o options) int {
	f, err := os.Open(lastRunPath())
	if err != nil {
		// No file and an empty file mean the same thing to the user: there is
		// nothing to undo. Only one of those two cases used to say so.
		if os.IsNotExist(err) {
			fmt.Println("nothing to undo")
			return exitOK
		}
		return failRun(err)
	}
	defer f.Close()

	trash, err := trashDir()
	if err != nil {
		return failRun(err)
	}

	moved, missing := 0, 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			missing++
			continue
		}
		if o.dryRun {
			fmt.Printf("would trash %s\n", p)
			moved++
			continue
		}
		if err := trashFile(p, trash); err != nil {
			return failRun(err)
		}
		fmt.Printf("trashed %s\n", displayName(p))
		moved++
	}
	if moved == 0 && missing == 0 {
		fmt.Println("nothing to undo")
	}
	if missing > 0 {
		fmt.Printf("%d output(s) were already gone\n", missing)
	}
	if !o.dryRun && moved > 0 {
		_ = os.Remove(lastRunPath())
	}
	return exitOK
}

func trashDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".Trash")
	if runtime.GOOS != "darwin" {
		dir = filepath.Join(home, ".local", "share", "Trash", "files")
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			dir = filepath.Join(d, "Trash", "files")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// trashFile moves a file into the trash, never deleting it outright. The
// destination name is claimed with O_EXCL before anything moves, so two
// files trashed with the same basename in the same second cannot silently
// overwrite one another the way a stat-then-rename would allow.
func trashFile(path, trash string) error {
	dst, err := claimTrashName(path, trash)
	if err != nil {
		return err
	}
	if err := os.Rename(path, dst); err == nil {
		return nil
	}
	// A rename across filesystems needs a streamed copy. dst is safe to
	// overwrite: claimTrashName's O_EXCL means nothing else could have taken
	// that name in the meantime.
	return copyThenRemove(path, dst)
}

// claimTrashName finds a name in trash nothing already occupies. O_EXCL
// makes the claim atomic, unlike a stat followed by a separate rename, which
// leaves a window for two trashed files to pick the same name.
func claimTrashName(path, trash string) (string, error) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	dst := filepath.Join(trash, base)
	for i := 1; ; i++ {
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			return dst, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		dst = filepath.Join(trash, fmt.Sprintf("%s %s-%d%s", stem, time.Now().Format("15.04.05"), i, ext))
	}
}

// copyThenRemove streams path onto the already-claimed dst and preserves its
// mode, rather than reading the whole file into memory the way os.ReadFile
// would.
func copyThenRemove(path, dst string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, st.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, st.Mode()); err != nil {
		return err
	}
	return os.Remove(path)
}
