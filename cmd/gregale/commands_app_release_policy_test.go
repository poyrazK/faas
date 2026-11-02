package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale app <slug> --release-policy immediate` printed the app's settings
// and changed nothing: the read-or-write check listed update fields by hand
// and missed the release fields, so the journey's v3 ran as a safe canary.
func TestCmdAppReleaseFlagsPatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		check func(api.UpdateAppRequest) bool
	}{
		{"release policy", []string{"--release-policy", api.ReleasePolicyImmediate}, func(r api.UpdateAppRequest) bool {
			return r.ReleasePolicy != nil && *r.ReleasePolicy == api.ReleasePolicyImmediate
		}},
		{"release probe paths", []string{"--release-probe-paths", "/a,/b"}, func(r api.UpdateAppRequest) bool {
			return r.ReleaseProbePaths != nil && slices.Equal(*r.ReleaseProbePaths, []string{"/a", "/b"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var got api.UpdateAppRequest
			var patches int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/"+main.ConstSlug {
					patches++
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Errorf("decode PATCH body: %v", err)
					}
				}
				main.WriteJSONTest(w, api.AppResponse{Slug: main.ConstSlug})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var stdout bytes.Buffer
			oldOut := (*main.OsStdout)
			(*main.OsStdout) = &stdout
			t.Cleanup(func() { (*main.OsStdout) = oldOut })
			args := append([]string{"app", main.ConstSlug}, tc.flags...)
			if code := main.Run(append(args, "--json")); code != 0 {
				t.Fatalf("run(%v) = %d; output: %s", args, code, stdout.String())
			}
			if patches != 1 || !tc.check(got) {
				t.Fatalf("PATCH calls = %d, body %+v; want one PATCH carrying %v", patches, got, tc.flags)
			}
		})
	}
}
