package main_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `cache GET … --app h12-py` worked but `cache purge --app h12-py` failed
// with "unknown flag --app" (Hunt 12 H12-21). Purge now takes the same
// spellings as the declaration: <slug>, --app, or the linked app.
func TestCachePurgeAcceptsAppFlagAndLinkedApp(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		linked   bool
		wantCode int
		wantPath string
	}{
		{"positional slug", []string{"purge", "web"}, false, 0, "/v1/apps/web/cache"},
		{"--app", []string{"purge", "--app", "web"}, false, 0, "/v1/apps/web/cache"},
		{"--app after --path", []string{"purge", "--path", "/p/*", "--app", "web"}, false, 0, "/v1/apps/web/cache"},
		{"same app twice", []string{"purge", "web", "--app", "web"}, false, 0, "/v1/apps/web/cache"},
		{"linked app", []string{"purge"}, true, 0, "/v1/apps/products/cache"},
		{"--app beats linked app", []string{"purge", "--app", "web"}, true, 0, "/v1/apps/web/cache"},
		{"two different apps", []string{"purge", "web", "--app", "api"}, false, 1, ""},
		{"no app and no link", []string{"purge"}, false, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			root := t.TempDir()
			if tt.linked {
				if _, err := main.SaveProjectContext(root, main.LocalProjectContext{
					Version: main.ProjectContextVersion,
					Project: "shop",
					App:     "products",
				}); err != nil {
					t.Fatalf("save project context: %v", err)
				}
				t.Chdir(filepath.Join(root, ".gregale"))
			} else {
				t.Chdir(root)
			}
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					http.NotFound(w, r)
					return
				}
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			_, restore := main.CaptureStdout(t)
			defer restore()
			if code := main.CmdCache(tt.args); code != tt.wantCode || gotPath != tt.wantPath {
				t.Fatalf("cache %q: exit=%d path=%q, want exit=%d path=%q", tt.args, code, gotPath, tt.wantCode, tt.wantPath)
			}
		})
	}
}
