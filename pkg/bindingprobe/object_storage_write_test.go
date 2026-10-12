// adr: 1027 — bounded conditional transfers and cleanup never qualify ambiguous writes.
package bindingprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectStorageWriteModesAndCleanup(t *testing.T) {
	for _, mode := range []string{"write", "read_write"} {
		for _, failure := range []string{"", "collision", "upload", "download", "contents", "cleanup", "still present", "wrong missing code", "cancel"} {
			if mode == "write" && (failure == "download" || failure == "contents" || failure == "still present" || failure == "wrong missing code" || failure == "cancel") {
				continue
			}
			t.Run(mode+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var payload []byte
				key := ""
				var methods []string
				deleted := false
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					methods = append(methods, r.Method)
					if !strings.Contains(r.Header.Get("Authorization"), "Credential=private-access-key/") {
						t.Error("missing selected credentials")
					}
					if r.Method == "PUT" {
						key = r.URL.Path
						if !strings.HasPrefix(key, "/private-bucket/.gregale/binding-probes/") || len(strings.TrimPrefix(key, "/private-bucket/.gregale/binding-probes/")) != 64 || r.Header.Get("If-None-Match") != "*" {
							t.Error("unsafe probe key or missing create-only condition")
						}
						payload, _ = io.ReadAll(r.Body)
						if len(payload) > 64 {
							t.Error("unbounded upload")
						}
						if failure == "collision" {
							w.WriteHeader(412)
							_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
							return
						}
						if failure == "upload" {
							w.WriteHeader(503)
							return
						}
						w.Header().Set("X-Amz-Version-Id", "private-version")
						w.Header().Set("ETag", `"fixture"`)
						return
					}
					if r.URL.Path != key {
						t.Error("probe touched another key")
					}
					if failure != "upload" && r.URL.Query().Get("versionId") != "private-version" {
						t.Error("probe failed to pin exact created version")
					}
					switch r.Method {
					case "DELETE":
						if failure == "cleanup" {
							w.WriteHeader(403)
							_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>private-secret-key</Message></Error>")
							return
						}
						deleted = true
						w.WriteHeader(204)
					case "GET":
						if mode == "write" {
							t.Error("write-only probe attempted GET")
						}
						if deleted && failure != "still present" {
							w.WriteHeader(404)
							code := "NoSuchVersion"
							if failure == "wrong missing code" {
								code = "NoSuchBucket"
							}
							_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code></Error>", code)
							return
						}
						if failure == "cancel" {
							cancel()
							<-r.Context().Done()
							return
						}
						if failure == "download" {
							w.WriteHeader(503)
							return
						}
						if failure == "contents" {
							_, _ = io.WriteString(w, "private-provider-error")
							return
						}
						_, _ = w.Write(payload)
					default:
						t.Error("unexpected probe operation")
					}
				}))
				defer srv.Close()
				env := objectStorageTestEnv(srv.URL)
				report := ObjectStorageWrite(ctx, "ASSETS", mode, func(k string) string { return env[k] })
				if report.Passed() != (failure == "") {
					t.Fatalf("failure=%s report=%+v", failure, report)
				}
				if failure == "collision" && len(methods) != 1 {
					t.Fatal("collision caused customer-object cleanup")
				}
				if failure != "collision" && failure != "cleanup" && !deleted {
					t.Fatal("probe omitted cleanup after failure/cancellation")
				}
				if failure == "" {
					expected := "PUT,DELETE"
					if mode == "read_write" {
						expected = "PUT,GET,DELETE,GET"
					}
					if strings.Join(methods, ",") != expected {
						t.Fatalf("operations=%v", methods)
					}
				}
				body, _ := json.Marshal(report)
				for _, sensitive := range []string{key, "private-", srv.URL} {
					if sensitive != "" && strings.Contains(string(body), sensitive) {
						t.Fatal("probe leaked private data")
					}
				}
			})
		}
	}
}

func TestObjectStorageWriteRejectsModeAndIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"", "read", "all", "read_write/escape"} {
		r := ObjectStorageWrite(context.Background(), "ASSETS", mode, func(string) string { t.Fatal("invalid mode read environment"); return "" })
		if r.Passed() || r.Error == "" {
			t.Fatal("invalid mode accepted")
		}
	}
	passed := api.ObjectStorageBindingProbeCheck{Status: "passed"}
	r := api.ObjectStorageBindingProbeReport{Mode: "read_write", Environment: passed, Configuration: passed, Connection: passed, Authorization: passed, BucketAccess: passed}
	if r.Passed() {
		t.Fatal("legacy evidence qualified a write probe")
	}
}
