package review

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// Listen binds a TCP listener to 127.0.0.1 only — never 0.0.0.0 or an
// unqualified port, which would make the review session (no auth, since
// it's meant for a single local user) reachable from the LAN. port 0 lets
// the OS pick a free port.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

// Run serves the wizard on ln until either the request context is
// canceled or the human clicks "Finish Review" (State.Done()), then shuts
// the server down cleanly and returns.
func Run(ctx context.Context, ln net.Listener, st *State) error {
	srv := &http.Server{Handler: NewMux(st)}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-st.Done():
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	return srv.Shutdown(context.Background())
}
