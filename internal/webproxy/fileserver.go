package webproxy

import (
	"net/http"
	"os"

	"andey-proxy/internal/config"
)

// fileServerHandler confines all file opens, including symlinks, to RootDir.
// A root handle lives for one request; replacements are resolved on the next
// request and hot reload does not leave directory descriptors behind.
func fileServerHandler(rule config.SubRule) http.Handler {
	fs := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(rule.RootDir)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		http.FileServerFS(root.FS()).ServeHTTP(w, r)
	})
	prefix := rule.FrontendPath
	if prefix == "" || prefix == "/" {
		return fs
	}
	return http.StripPrefix(prefix, fs)
}
