package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve existing ancestors too: a directory may be provisioned after its
// configuration is saved, but a symlink above it must not hide an overlap.
func resolveServiceRoot(name string) (string, error) {
	name = filepath.Clean(name)
	resolved, err := filepath.EvalSymlinks(name)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, linkErr := os.Lstat(name); !os.IsNotExist(linkErr) {
		return "", err // Do not accept dangling symlinks or inaccessible paths.
	}
	parent := filepath.Dir(name)
	if parent == name {
		return "", err
	}
	resolved, err = resolveServiceRoot(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(name)), nil
}

// Persist canonical paths so changing an alias after saving cannot silently
// redirect a service. CGI trees and writable DAV trees must be disjoint.
func validateServiceRoots(sites []Site) error {
	type directory struct {
		path string
		info os.FileInfo
	}
	var writable, scripts []directory
	for _, site := range sites {
		for i := range site.Rules {
			rule := &site.Rules[i]
			if rule.Type != "webdav" && rule.Type != "cgi" {
				continue
			}
			root, err := resolveServiceRoot(rule.RootDir)
			if err != nil || root == string(filepath.Separator) {
				return fmt.Errorf("WebDAV/CGI根目录无法安全解析或指向系统根目录")
			}
			rule.RootDir = root
			info, _ := os.Stat(root)
			dir := directory{root, info}
			if rule.Type == "webdav" && rule.WebDAVWritable {
				writable = append(writable, dir)
			}
			if rule.Type == "cgi" {
				scripts = append(scripts, dir)
			}
		}
	}
	for _, root := range writable {
		for _, script := range scripts {
			if serviceRootContains(root.path, script.path) || serviceRootContains(script.path, root.path) ||
				(root.info != nil && script.info != nil && os.SameFile(root.info, script.info)) {
				return fmt.Errorf("CGI目录与可写WebDAV目录不可重叠")
			}
		}
	}
	return nil
}

func serviceRootContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}
