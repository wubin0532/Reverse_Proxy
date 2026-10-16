// Package adminweb serves the embedded management frontend.
package adminweb

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:embed dist
var distFS embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return assetHandler(sub)
}

// Precompressed representations are produced at build time. Only small metadata
// is retained here; requests read directly from embed.FS without runtime compression.
func assetHandler(files fs.FS) http.Handler {
	etags := map[string]string{}
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		f, err := files.Open(name)
		if err != nil {
			return err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, f)
		_ = f.Close()
		if err == nil {
			etags[name] = fmt.Sprintf(`"%x"`, sum.Sum(nil))
		}
		return err
	})
	if err != nil {
		panic(err)
	}
	if _, ok := etags["index.html"]; !ok {
		panic("embedded index.html missing")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".br") {
			http.NotFound(w, r)
			return
		}
		if _, ok := etags[name]; !ok {
			name = "index.html"
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		_, hasBR := etags[name+".br"]
		_, hasGzip := etags[name+".gz"]
		if hasBR || hasGzip {
			w.Header().Add("Vary", "Accept-Encoding")
		}
		encoding, ok := negotiateEncoding(r.Header.Get("Accept-Encoding"), hasBR, hasGzip, r.Header.Get("Range") != "")
		if !ok {
			http.Error(w, "No acceptable content encoding", http.StatusNotAcceptable)
			return
		}
		original := name
		switch encoding {
		case "br":
			name += ".br"
		case "gzip":
			name += ".gz"
		}
		f, err := files.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		seeker, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "Resource unavailable", 500)
			return
		}
		if encoding != "identity" {
			w.Header().Set("Content-Encoding", encoding)
			if info, err := f.Stat(); err == nil {
				w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
			}
		}
		if typ := mime.TypeByExtension(path.Ext(original)); typ != "" {
			w.Header().Set("Content-Type", typ)
		}
		w.Header().Set("ETag", etags[name])
		http.ServeContent(w, r, path.Base(original), time.Time{}, seeker)
	})
}
