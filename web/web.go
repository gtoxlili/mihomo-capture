// Package web is the dashboard: the React app in this directory, built into
// dist by `pnpm --dir web build` and embedded in the service binary.
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// dist always holds its .gitignore, so the package builds before the app is.
//
//go:embed all:dist
var dist embed.FS

// The dashboard displays captured third-party content. The policy keeps any
// of it from running script, loading from elsewhere or framing the page.
const policy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

type file struct {
	body, gzipped []byte
	contentType   string
	etag          string
}

// Handler serves the built files, and index.html for the app's own routes.
func Handler() http.Handler {
	files := map[string]file{}
	_ = fs.WalkDir(dist, "dist", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		b, err := dist.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		f := file{body: b, contentType: mime.TypeByExtension(path.Ext(p)), etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		var z bytes.Buffer
		w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
		_, _ = w.Write(b)
		_ = w.Close()
		if z.Len() < len(b)*9/10 {
			f.gzipped = z.Bytes()
		}
		files[strings.TrimPrefix(p, "dist")] = f
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := path.Clean("/" + r.URL.Path)
		f, ok := files[name]
		if !ok && path.Ext(name) == "" {
			f, ok = files["/index.html"]
			if !ok {
				http.Error(w, "dashboard not built: run `pnpm --dir web build` before building the service", http.StatusServiceUnavailable)
				return
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Vite names assets by content hash; everything else is revalidated.
		if strings.HasPrefix(name, "/assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		h.Set("ETag", f.etag)
		h.Set("Vary", "Accept-Encoding")
		if f.contentType != "" {
			h.Set("Content-Type", f.contentType)
		}
		if r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body := f.body
		if f.gzipped != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.Set("Content-Encoding", "gzip")
			body = f.gzipped
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
