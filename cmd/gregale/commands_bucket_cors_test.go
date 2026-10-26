package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const bucketCORSTestID = "4f1c2a7e-0b9d-4c8e-9a51-2f6d3e8b7c10"

func TestDecodeBucketCORSFile(t *testing.T) {
	rules, err := main.DecodeBucketCORSFile(strings.NewReader(`{"rules":[{"allowed_origins":["https://app.example.com"],"allowed_methods":["PUT","GET"],"expose_headers":["ETag"],"max_age_seconds":3600}]}`))
	if err != nil || len(rules) != 1 || rules[0].MaxAgeSeconds != 3600 {
		t.Fatalf("rules = %#v, %v", rules, err)
	}
	for _, body := range []string{
		`{}`,
		`{"rules":[]}`,
		`{"rules":[{"allowed_origins":["ftp://x"],"allowed_methods":["GET"]}]}`,
		`{"rules":[{"allowed_origins":["*"],"allowed_methods":["PATCH"]}]}`,
		`{"rules":[{"allowed_origins":["*"],"allowed_methods":["GET"]}],"extra":1}`,
		`{"rules":[{"allowed_origins":["*"],"allowed_methods":["GET"]}]} {}`,
		`{"rules":[{"allowed_origins":["*"],"allowed_methods":["GET"]}]}` + strings.Repeat(" ", 256<<10),
	} {
		if _, err := main.DecodeBucketCORSFile(strings.NewReader(body)); err == nil {
			t.Errorf("accepted invalid rules %.80q", body)
		}
	}
}

func TestRunBucketCORS(t *testing.T) {
	rules := []api.ObjectBucketCORSRule{{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}}}
	for action, method := range map[string]string{"get": http.MethodGet, "set": http.MethodPut, "delete": http.MethodDelete} {
		t.Run(action, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != "/v1/apps/demo/buckets/"+bucketCORSTestID+"/cors" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				if method == http.MethodPut {
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), `"allowed_origins":["*"]`) {
						t.Errorf("PUT body = %s", body)
					}
				}
				if method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = fmt.Fprintf(w, `{"bucket_id":%q,"revision":3,"rules":[{"allowed_origins":["*"],"allowed_methods":["GET"]}]}`, bucketCORSTestID)
			}))
			defer srv.Close()
			out, err := main.RunBucketCORS(t.Context(), api.NewClient(srv.URL, "token"), []string{action, "demo", bucketCORSTestID}, rules)
			if err != nil || out.BucketID != bucketCORSTestID {
				t.Fatalf("%s = %#v, %v", action, out, err)
			}
		})
	}
}

func TestBucketCORSCommand(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprintf(w, `{"bucket_id":%q,"revision":4,"rules":[{"id":"site","allowed_origins":["https://app.example.com"],"allowed_methods":["PUT","GET"],"max_age_seconds":600}]}`, bucketCORSTestID)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	file := filepath.Join(t.TempDir(), "cors.json")
	if err := os.WriteFile(file, []byte(`{"rules":[{"id":"site","allowed_origins":["https://app.example.com"],"allowed_methods":["PUT","GET"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "bucket", "cors", "get", "demo", bucketCORSTestID)
	if code != 0 || !strings.Contains(stdout, "revision 4") || !strings.Contains(stdout, "origins https://app.example.com · methods PUT, GET · max age 600s") {
		t.Fatalf("get = %d\n%s%s", code, stdout, stderr)
	}
	if code, stdout, stderr = runCLI(t, "bucket", "cors", "set", "demo", bucketCORSTestID, file); code != 0 || !strings.Contains(stdout, "Saved 1 CORS rule(s)") {
		t.Fatalf("set = %d\n%s%s", code, stdout, stderr)
	}
	if code, stdout, stderr = runCLI(t, "bucket", "cors", "clear", "demo", bucketCORSTestID); code != 0 || !strings.Contains(stdout, "Removed CORS rules") {
		t.Fatalf("clear = %d\n%s%s", code, stdout, stderr)
	}
	if strings.Join(methods, " ") != "GET PUT DELETE" {
		t.Fatalf("requests = %v", methods)
	}
	if code, _, stderr = runCLI(t, "bucket", "cors", "get", "demo", "not-a-uuid"); code == 0 || !strings.Contains(stderr, "usage: gregale bucket cors") {
		t.Fatalf("bad bucket id = %d %q", code, stderr)
	}
}
