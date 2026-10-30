// Package webui serves the React build embedded in the binary (A7).
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The Dockerfile copies the Vite build into dist before `go build`; in the
// repository dist holds only .keep, so the "all:" prefix is needed to embed it.
//
//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded build.
func Dist() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // "dist" is a constant, valid path
	}
	return dist
}

// Handler serves files from dist and answers every other path with index.html,
// so client-side routes work on reload. A directory with its own index.html (the
// Swagger UI at /swagger/) gets that page. Missing files under assets/ (Vite's build
// output) get a 404 instead, so a stale bundle fails clearly. Mount it after the API routes.
func Handler(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		info, err := fs.Stat(dist, name)
		if err != nil && strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		if err == nil && info.IsDir() {
			if _, err := fs.Stat(dist, path.Join(name, "index.html")); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		if err != nil || info.IsDir() {
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}
