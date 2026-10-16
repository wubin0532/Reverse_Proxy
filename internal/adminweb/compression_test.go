package adminweb

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEncodingNegotiation(t *testing.T) {
	for _, tc := range []struct {
		header string
		ranged bool
		want   string
		ok     bool
	}{
		{"", false, "identity", true}, {"gzip, br", false, "br", true}, {"br;q=0,gzip", false, "gzip", true},
		{"*;q=0", false, "", false}, {"gzip;q=0.2,identity;q=0", false, "gzip", true},
		{"gzip;q=0,br;q=0,identity;q=0", false, "", false}, {"br,gzip", true, "identity", true},
		{"br, identity;q=0", true, "br", true}, {"gzip;q=invalid", false, "identity", true},
	} {
		t.Run(tc.header, func(t *testing.T) {
			got, ok := negotiateEncoding(tc.header, true, true, tc.ranged)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPrecompressedServingAndConditionalRanges(t *testing.T) {
	content := []byte(strings.Repeat("console.log('hello');", 100))
	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	zw.Write(content)
	zw.Close()
	handler := assetHandler(fstest.MapFS{"index.html": {Data: []byte("<html>index</html>")}, "assets/app.js": {Data: content}, "assets/app.js.gz": {Data: packed.Bytes()}})
	get := func(method, encoding, etag, ranged string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/assets/app.js", nil)
		req.Header.Set("Accept-Encoding", encoding)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if ranged != "" {
			req.Header.Set("Range", ranged)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	compressed := get("GET", "gzip", "", "")
	if compressed.Code != 200 || compressed.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(compressed.Header().Get("Vary"), "Accept-Encoding") || !strings.Contains(compressed.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("headers: %v", compressed.Header())
	}
	zr, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := io.ReadAll(zr)
	zr.Close()
	if !bytes.Equal(decoded, content) {
		t.Fatal("compressed contents changed")
	}
	if conditional := get("GET", "gzip", compressed.Header().Get("ETag"), ""); conditional.Code != 304 || conditional.Body.Len() != 0 {
		t.Fatalf("conditional: %d", conditional.Code)
	}
	identity := get("GET", "", "", "")
	if identity.Header().Get("ETag") == compressed.Header().Get("ETag") {
		t.Fatal("representations share ETag")
	}
	head := get("HEAD", "gzip", "", "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal("invalid HEAD")
	}
	ranged := get("GET", "gzip", "", "bytes=0-6")
	if ranged.Code != 206 || ranged.Header().Get("Content-Encoding") != "" || ranged.Body.String() != string(content[:7]) {
		t.Fatalf("range: %d %q", ranged.Code, ranged.Body.String())
	}
}
