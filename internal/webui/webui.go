// Package webui serves the SvelteKit (adapter-static) build embedded from dist/.
//
// Build order: `cd web && npm run build` (copies web/build -> internal/webui/dist), then `go build`.
// dist/ is git-ignored. When it is missing (fresh clone), the package still builds and serves
// placeholder/index.html so `go build ./...` always works.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// all:* (not all:dist) so the pattern always matches something even when dist/ does not exist.
// The leading "all:" keeps SvelteKit's "_app" directory, which a plain pattern would skip.
//
//go:embed all:*
var embedded embed.FS

var site = pickFS()

func pickFS() fs.FS {
	if sub, err := fs.Sub(embedded, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	sub, _ := fs.Sub(embedded, "placeholder")
	return sub
}

// Built reports whether a real web build (not the placeholder) is embedded.
func Built() bool {
	_, err := fs.Stat(embedded, "dist/index.html")
	return err == nil
}

// Handler serves the embedded SPA. Paths without a matching file fall back to index.html.
// Cache headers: /_app/immutable/* one year immutable; index.html, service-worker.js,
// manifest.webmanifest and the SPA fallback no-cache; other static files (icons) one day.
func Handler() http.Handler { return handler{site} }

type handler struct{ fsys fs.FS }

var startTime = time.Now()

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	f, err := h.fsys.Open(p)
	if err == nil {
		if st, serr := f.Stat(); serr != nil || st.IsDir() {
			f.Close()
			err = fs.ErrNotExist
			// directory: try dir/index.html
			if serr == nil {
				if f2, err2 := h.fsys.Open(path.Join(p, "index.html")); err2 == nil {
					f, err, p = f2, nil, path.Join(p, "index.html")
				}
			}
		}
	}
	if err != nil {
		// Missing asset under /_app/ is a real 404 (stale client after deploy), not the SPA.
		if strings.HasPrefix(p, "_app/") || path.Ext(p) != "" && path.Ext(p) != ".html" {
			w.Header().Set("Cache-Control", "no-cache")
			http.NotFound(w, r)
			return
		}
		f, err = h.fsys.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p = "index.html"
	}
	defer f.Close()

	switch {
	case strings.HasPrefix(p, "_app/immutable/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case p == "index.html", p == "service-worker.js", p == "manifest.webmanifest",
		strings.HasSuffix(p, ".html"), p == "_app/version.json":
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	if p == "service-worker.js" {
		w.Header().Set("Service-Worker-Allowed", "/")
	}
	ctype := mime.TypeByExtension(path.Ext(p))
	switch path.Ext(p) {
	case ".webmanifest":
		ctype = "application/manifest+json"
	case ".js", ".mjs":
		ctype = "text/javascript; charset=utf-8"
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, p, startTime, rs)
		return
	}
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, f)
}
