package adminweb

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestAllBuiltAssetReferencesAreEmbedded(t *testing.T) {
	assetRef := regexp.MustCompile(`assets/[A-Za-z0-9_.-]+`)
	err := fs.WalkDir(distFS, "dist", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (!strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".js")) {
			return nil
		}
		data, err := fs.ReadFile(distFS, path)
		if err != nil {
			return err
		}
		for _, ref := range assetRef.FindAllString(string(data), -1) {
			if _, err := fs.Stat(distFS, "dist/"+ref); err != nil {
				return fmt.Errorf("%s references missing embedded asset %s: %w", path, ref, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
