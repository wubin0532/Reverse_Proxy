package webproxy

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"andey-proxy/internal/config"
)

// Retain identity, not an open descriptor, for a handler's lifetime. Each
// request opens and verifies a rooted descriptor before using the directory.
type serviceDirectory struct {
	path string
	info os.FileInfo
	err  error
}

func bindServiceDirectory(name string) *serviceDirectory {
	d := &serviceDirectory{path: name}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || resolved != filepath.Clean(name) {
		d.err = fmt.Errorf("service directory unavailable or changed since validation")
		return d
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		d.err = err
		return d
	}
	defer root.Close()
	d.info, d.err = root.Stat(".")
	return d
}

func (d *serviceDirectory) open() (*os.Root, error) {
	if d.err != nil {
		return nil, d.err
	}
	root, err := os.OpenRoot(d.path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(d.info, info) {
		root.Close()
		return nil, fmt.Errorf("service directory identity changed")
	}
	return root, nil
}

// Bind service directories before the first request. Unchanged handlers keep
// their original identity across reloads; only an explicit rule edit rebinds.
// Caller holds handlerMu, or is constructing an unpublished siteServer.
func (ss *siteServer) prepareServiceHandlers(site config.Site) {
	if ss.staticHandler == nil {
		ss.staticHandler = make(map[string]http.Handler)
	}
	for _, rule := range site.Rules {
		if ss.staticHandler[rule.ID] != nil || !rule.Enabled {
			continue
		}
		switch rule.Type {
		case "webdav":
			ss.staticHandler[rule.ID] = webDAVHandler(rule)
		case "cgi":
			ss.staticHandler[rule.ID] = cgiHandler(rule)
		}
	}
}
