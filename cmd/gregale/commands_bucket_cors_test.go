// adr: 20261025202805

package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBucketCORSCLI(t *testing.T) {
	const bucket = "00000000-0000-0000-0000-000000000001"
	rules, err := main.DecodeBucketCORSFile(strings.NewReader(`{"rules":[{"allowed_origins":["https://app.example.com"],"allowed_methods":["PUT","GET"],"allowed_headers":["content-type"],"max_age_seconds":600}]}`))
	if err != nil || len(rules) != 1 || rules[0].MaxAgeSeconds != 600 {
		t.Fatal(rules, err)
	}
	for _, body := range []string{`null`, `{"rules":[]}`, `{"rules":[{"allowed_origins":["https://a.test"]}]}`, `{"rules":[{"allowed_origins":["ftp://a.test"],"allowed_methods":["GET"]}]}`, `{"rules":[],"extra":1}`, `{"rules":[{"allowed_origins":["*"],"allowed_methods":["GET"]}]} {}`, strings.Repeat(" ", api.MaxObjectBucketCORSBodyBytes+1)} {
		if _, err = main.DecodeBucketCORSFile(strings.NewReader(body)); err == nil {
			t.Fatal("accepted invalid CORS document", body[:min(len(body), 80)])
		}
	}
	for _, args := range [][]string{{}, {"get", "bad/app", bucket}, {"set", "demo", bucket}, {"get", "demo", "not-a-uuid"}, {"clear", "demo", bucket, "extra"}, {"delete", "demo", bucket}} {
		if main.ValidBucketCORSArgs(args) {
			t.Fatal("accepted invalid arguments", args)
		}
	}
	for _, action := range []string{"get", "set", "clear"} {
		t.Run(action, func(t *testing.T) {
			method := map[string]string{"get": "GET", "set": "PUT", "clear": "DELETE"}[action]
			args := []string{action, "demo", bucket}
			if action == "set" {
				args = append(args, "-")
			}
			if !main.ValidBucketCORSArgs(args) {
				t.Fatal(args)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != "/v1/apps/demo/buckets/"+bucket+"/cors" {
					t.Error(r.Method, r.URL.Path)
				}
				if action == "set" {
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), `"allowed_origins":["https://app.example.com"]`) {
						t.Error("set sent", string(body))
					}
				}
				if action == "clear" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = fmt.Fprint(w, `{"bucket_id":"`+bucket+`","rules":[],"revision":2,"updated_at":"2026-10-26T00:00:00Z"}`)
			}))
			defer srv.Close()
			out, err := main.RunBucketCORS(t.Context(), api.NewClient(srv.URL, "token"), args, rules)
			if err != nil || out.BucketID != bucket {
				t.Fatal(out, err)
			}
		})
	}
}
