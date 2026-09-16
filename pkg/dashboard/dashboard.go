// Package dashboard serves the operator interface.
//
// The bundle is embedded in the API service's binary rather than built and
// shipped separately, so that `docker compose up` yields a working URL with no
// frontend container and no build step for the user. That constraint is why
// there is no toolchain here: the sources are plain ES modules the browser
// loads directly, which is what makes "no build step" true rather than
// "a build step someone else already ran".
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web
var bundle embed.FS

// Handler serves the dashboard at the root of the API service.
func Handler() http.Handler {
	root, err := fs.Sub(bundle, "web")
	if err != nil {
		// The bundle is embedded at compile time, so a missing subtree is a
		// build fault rather than a runtime condition.
		panic(err)
	}

	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Screens are addressed by fragment, so every URL the browser asks for
		// is a real file. Anything else is a mistyped path or a stale
		// bookmark, and lands on the dashboard rather than on a bare 404.
		if !exists(root, r.URL.Path) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}

		// The bundle changes only when the binary does, and an operator who
		// redeploys must not be served yesterday's JavaScript.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

func exists(root fs.FS, path string) bool {
	name := strings.TrimPrefix(path, "/")
	if name == "" {
		return true
	}
	if _, err := fs.Stat(root, name); err != nil {
		return false
	}
	return true
}
