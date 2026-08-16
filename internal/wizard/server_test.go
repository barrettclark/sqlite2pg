package wizard

import (
	"strings"
	"testing"
)

func TestListen_BindsToLoopbackOnlyNeverAllInterfaces(t *testing.T) {
	// This is a security-relevant default for a single-user tool with no
	// auth: a regression here (e.g. someone changing the bind address for
	// "convenience") would make the review session reachable from the LAN.
	ln, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("expected listener bound to 127.0.0.1, got %q", addr)
	}
}
