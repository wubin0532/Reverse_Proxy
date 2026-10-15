package adminweb

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
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

// 目录请求不应泄漏前端 chunk 清单，而应回退到 SPA 入口。
func TestHandlerDirectoryRequestFallsBackToIndex(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("目录请求应回退 index.html, got content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("index.html 不应被缓存, got %q", cc)
	}
}

// 带内容哈希的 assets/ 资源应返回长期不可变缓存头。
func TestHandlerCachesHashedAssets(t *testing.T) {
	var asset string
	if err := fs.WalkDir(distFS, "dist/assets", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || asset != "" {
			return walkErr
		}
		asset = strings.TrimPrefix(path, "dist/")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if asset == "" {
		t.Skip("构建产物中没有 assets/ 资源")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+asset, nil))
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("%s 应可长期缓存, got %q", asset, cc)
	}
}
