package docsassets

import (
	"crypto/sha512"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The vendored files must be the release the API reference page's SRI
// digests name, or browsers refuse them.
func TestVendoredSwaggerUIMatchesItsIntegrity(t *testing.T) {
	for file, want := range map[string]string{
		"swagger-ui-bundle-5.17.14.js": "sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep",
		"swagger-ui-5.17.14.css":       "sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn",
	} {
		body, err := files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha512.Sum384(body)
		if got := "sha384-" + base64.StdEncoding.EncodeToString(sum[:]); got != want {
			t.Errorf("%s digest = %s, want %s", file, got, want)
		}
	}
}

func TestServeAnswersOnlyTheVendoredFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /docs/assets/{file}", Serve)
	for path, wantType := range map[string]string{
		BundlePath: "text/javascript; charset=utf-8",
		CSSPath:    "text/css; charset=utf-8",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != wantType || rec.Body.Len() == 0 {
			t.Errorf("%s = %d %q (%d bytes)", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
	for _, path := range []string{"/docs/assets/docsassets.go", "/docs/assets/README.md", "/docs/assets/..%2Fopenapi_handler.go"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}
