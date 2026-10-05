package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// confirmOverwrite reports whether a config may be written to path. A missing
// file is always fine. An existing one needs force, or a y answer at an
// interactive prompt; a non-interactive run is refused rather than prompting.
func confirmOverwrite(path string, force bool, in io.Reader, w io.Writer, interactive bool) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking %s: %w", path, err)
	}
	if force {
		return true, nil
	}
	if !interactive {
		return false, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}
	fmt.Fprintf(w, "warning: %s already exists. Regenerating it discards reviewed column decisions and transform overrides.\n", path)
	fmt.Fprintf(w, "Overwrite %s? [y/N] ", path)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	fmt.Fprintf(w, "left %s unchanged\n", path)
	return false, nil
}

// isTerminal reports whether r is a character device, which is how a
// terminal shows up for os.Stdin. A /dev/null stdin also qualifies; it reads
// EOF, which counts as no.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
