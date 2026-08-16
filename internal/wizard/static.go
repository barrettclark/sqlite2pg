package wizard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/index.html static/app.js
var staticFS embed.FS

// registerStaticRoutes serves the embedded frontend, keeping the tool a
// single self-contained binary with no separate asset directory to ship.
func registerStaticRoutes(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed.FS with a compile-time-checked path; cannot fail at runtime
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Serve index.html's bytes directly rather than delegating to
		// http.FileServer with path "/index.html": FileServer treats that
		// path specially and 301-redirects it to "/", which would loop.
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))
}
