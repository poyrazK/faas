package localpreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fixtureFile struct {
	Path        string `json:"path"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}
type fixtureRequest struct {
	Name                string            `json:"name"`
	Path                string            `json:"path"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers"`
	Status              int               `json:"status"`
	Body                *string           `json:"body"`
	Code                string            `json:"code"`
	ResponseHeaders     map[string]string `json:"response_headers"`
	AbsentHeaders       []string          `json:"absent_headers"`
	IfNoneMatchFile     string            `json:"if_none_match_file"`
	WeakIfNoneMatchFile string            `json:"weak_if_none_match_file"`
}

func manifest(files []fixtureFile, spa bool, cfg *api.HostingReleaseConfig) (api.HostingManifest, map[string][]byte) {
	m := api.HostingManifest{SPAFallback: spa, Config: cfg}
	bodies := map[string][]byte{}
	for _, file := range files {
		sum := sha256.Sum256([]byte(file.Body))
		m.Files = append(m.Files, api.HostingFile{Path: file.Path, Size: int64(len(file.Body)), SHA256: hex.EncodeToString(sum[:]), ContentType: file.ContentType})
		bodies[file.Path] = []byte(file.Body)
	}
	return m, bodies
}
func TestWorkerServingContract(t *testing.T) {
	// adr: 202610171311 — a single fixture locks content semantics to the actual Worker.
	raw, err := os.ReadFile("../../../deploy/cloudflare/hosting-worker/local-preview-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Files     []fixtureFile `json:"files"`
		Scenarios []struct {
			Name     string                    `json:"name"`
			SPA      bool                      `json:"spa"`
			Config   *api.HostingReleaseConfig `json:"config"`
			Requests []fixtureRequest          `json:"requests"`
		} `json:"scenarios"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range fixture.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			m, bodies := manifest(fixture.Files, scenario.SPA, scenario.Config)
			snapshot, err := New(m, func(f api.HostingFile) ([]byte, error) { return bodies[f.Path], nil })
			if err != nil {
				t.Fatal(err)
			}
			handler, err := snapshot.Handler("127.0.0.1:4173")
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range scenario.Requests {
				t.Run(item.Name, func(t *testing.T) {
					method := item.Method
					if method == "" {
						method = "GET"
					}
					r := httptest.NewRequest(method, "http://127.0.0.1:4173"+item.Path, nil)
					r.RemoteAddr = "127.0.0.1:12345"
					for k, v := range item.Headers {
						r.Header.Set(k, v)
					}
					tagFile := item.IfNoneMatchFile
					if tagFile == "" {
						tagFile = item.WeakIfNoneMatchFile
					}
					if tagFile != "" {
						sum := sha256.Sum256(bodies[tagFile])
						etag := `"sha256:` + hex.EncodeToString(sum[:]) + `"`
						if item.WeakIfNoneMatchFile != "" {
							etag = "W/" + etag
						}
						r.Header.Set("If-None-Match", etag)
					}
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != item.Status {
						t.Fatalf("status %d: %s", w.Code, w.Body)
					}
					for k, v := range item.ResponseHeaders {
						if w.Header().Get(k) != v {
							t.Errorf("%s: %q != %q", k, w.Header().Get(k), v)
						}
					}
					for _, k := range item.AbsentHeaders {
						if w.Header().Get(k) != "" {
							t.Errorf("unexpected %s", k)
						}
					}
					if item.Body != nil && w.Body.String() != *item.Body {
						t.Errorf("body %q != %q", w.Body, *item.Body)
					}
					if item.Code != "" {
						var p struct {
							Code string `json:"code"`
						}
						if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Code != item.Code {
							t.Fatal(w.Body)
						}
					}
				})
			}
		})
	}
}
func TestSnapshotValidationAndIsolation(t *testing.T) {
	files := []fixtureFile{{Path: "index.html", Body: "ORIGINAL", ContentType: "text/html"}}
	cfg := &api.HostingReleaseConfig{Version: 1, Headers: []api.HostingHeaderRule{{Path: "/*", Values: map[string]string{"x-frame-options": "DENY"}}}}
	m, bodies := manifest(files, false, cfg)
	if _, err := New(m, func(api.HostingFile) ([]byte, error) { return []byte("wrong"), nil }); err == nil {
		t.Fatal("mismatched bytes admitted")
	}
	snapshot, err := New(m, func(f api.HostingFile) ([]byte, error) { return bodies[f.Path], nil })
	if err != nil {
		t.Fatal(err)
	}
	bodies["index.html"][0] = 'X'
	cfg.Headers[0].Values["x-frame-options"] = "SAMEORIGIN"
	m.Files[0].ContentType = "text/plain"
	h, err := snapshot.Handler("127.0.0.1:4173")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			r := httptest.NewRequest("GET", "http://127.0.0.1:4173/", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != "ORIGINAL" || w.Header().Get("X-Frame-Options") != "DENY" {
				t.Error(w.Code, w.Body, w.Header())
			}
		})
	}
	wg.Wait()
	for _, authority := range []string{"0.0.0.0:4173", "localhost:4173", "127.0.0.1:0", "[::]:4173", "127.0.0.1:04173"} {
		if _, err = snapshot.Handler(authority); err == nil {
			t.Fatal("unsafe authority", authority)
		}
	}
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Host = "evil.example:4173" }, func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, func(r *http.Request) { r.Header.Set("Origin", "null") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:4173/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		change(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || strings.Contains(w.Body.String(), "ORIGINAL") {
			t.Fatal("local content leaked", w.Code, w.Body)
		}
	}
}
