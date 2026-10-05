//go:build !unix

package main

import "testing"

func makeFIFO(t *testing.T, dir string) string {
	t.Skip("FIFOs are not available on this platform")
	return ""
}
