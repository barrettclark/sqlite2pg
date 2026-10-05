package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirmOverwrite reports whether a config may be written to path. A missing
// file is fine. A symlink is refused rather than written through. An existing
// file needs force, or a y answer at an interactive prompt; a non-interactive
// run is refused rather than prompting.
func confirmOverwrite(path string, force bool, in io.Reader, w io.Writer, interactive bool) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a symbolic link; refusing to write through it (remove the link or pass a different --out)", path)
	}
	if force {
		return true, nil
	}
	if !interactive {
		return false, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}
	fmt.Fprintf(w, "warning: %s already exists. Regenerating it discards reviewed column decisions and transform overrides, and removes its load state %s.state.json.\n", path, path)
	fmt.Fprintf(w, "Overwrite %s? [y/N] ", path)
	line, err := bufio.NewReader(in).ReadString('\n')
	if errors.Is(err, io.EOF) {
		fmt.Fprintf(w, "\nno answer; left %s unchanged\n", path)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	fmt.Fprintf(w, "left %s unchanged\n", path)
	return false, nil
}

// discardStaleState removes the load state beside configPath as the config is
// written. The state records which database a prior load filled from that
// config; a --resume against a regenerated config would skip tables by name
// and reconnect to the old database, so the state must not outlive it.
func discardStaleState(configPath string, w io.Writer) error {
	statePath := configPath + ".state.json"
	err := os.Remove(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("removing stale load state %s: %w", statePath, err)
	}
	fmt.Fprintf(w, "removed stale load state %s\n", statePath)
	return nil
}

// isTerminal reports whether r is an interactive terminal. Only *os.File
// readers can be one; character devices such as /dev/null or /dev/zero are
// not, so they never reach a prompt.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
