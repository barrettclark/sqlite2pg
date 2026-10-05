package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Save writes cfg to path as YAML. The write goes to a temp file beside path,
// is fsynced, and is renamed over path, so a failed write leaves the previous
// config intact. A symlink at path is followed and the target is replaced
// atomically, so the link stays; a dangling link is an error. A new file gets
// 0644 before the umask, as os.WriteFile does; an existing file keeps its mode.
func Save(cfg *MigrationConfig, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := writeAtomic(path, data, writeAll); err != nil {
		return fmt.Errorf("writing config to %s: %w", path, err)
	}
	return nil
}

func writeAll(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

// writeAtomic replaces path with data via a same-directory temp file. write is
// a parameter so tests can fail the write partway through.
func writeAtomic(path string, data []byte, write func(*os.File, []byte) error) (err error) {
	target, rerr := resolveTarget(path)
	if rerr != nil {
		return rerr
	}
	path = target
	tmp, err := createTemp(path)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if err != nil {
			if !closed {
				tmp.Close()
			}
			os.Remove(tmp.Name())
		}
	}()

	info, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		if err = tmp.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	case errors.Is(statErr, fs.ErrNotExist):
		// A new file keeps the 0644 from createTemp, with the umask applied.
	default:
		err = statErr
		return err
	}
	if err = write(tmp, data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	closed = true
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// syncDir fsyncs dir so a completed rename survives power loss. Windows can't
// fsync a directory handle, and some platforms return EINVAL for it; both are
// skipped.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

// resolveTarget returns the file to replace for path. A symlink is followed, so
// writing through it updates the target and keeps the link. A dangling link
// fails here, before any temp file or target is created.
func resolveTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving symlink %s: %w", path, err)
	}
	return resolved, nil
}

// createTemp opens a new temp file in path's directory with os.WriteFile's
// 0644 mode, so the umask applies as usual.
func createTemp(path string) (*os.File, error) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	for i := 0; ; i++ {
		name := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d.tmp", base, time.Now().UnixNano(), i))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) || i >= 100 {
			return nil, fmt.Errorf("creating temp file for %s: %w", path, err)
		}
	}
}
