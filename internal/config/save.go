package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Save writes cfg to path as YAML. The write goes to a temp file beside path,
// is fsynced, and is renamed over path, so a failed write leaves the previous
// config intact. A new file gets 0644 before the umask, as os.WriteFile does; an
// existing file keeps its mode.
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

	if info, statErr := os.Stat(path); statErr == nil {
		if err = tmp.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
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
	return os.Rename(tmp.Name(), path)
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
