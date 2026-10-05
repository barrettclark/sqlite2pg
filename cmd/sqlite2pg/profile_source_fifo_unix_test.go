//go:build unix

package main

import (
	"path/filepath"
	"syscall"
	"testing"
)

func makeFIFO(t *testing.T, dir string) string {
	t.Helper()
	fifo := filepath.Join(dir, "pipe.db")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	return fifo
}
