// adr: 465 — CLI resolves source/target names and keeps cutover output credential-free.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresCutoverActions(t *testing.T) {
	for _, action := range []string{"prepare", "get", "verify", "cancel"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/postgres/databases":
						_, _ = w.Write([]byte(`{"items":[{"id":"source-id","name":"source"},{"id":"target-id","name":"target"}]}`))
						return
					case "/v1/apps/api":
						_, _ = w.Write([]byte(`{"id":"app-id","slug":"api"}`))
						return
					}
					expected := "/v1/postgres/cutovers/cutover-id"
					method := http.MethodPost
					if action == "prepare" {
						expected = "/v1/postgres/cutovers"
					} else if action == "get" {
						method = http.MethodGet
					} else {
						expected += "/" + action
					}
					if r.URL.Path != expected || r.Method != method {
						t.Errorf("request %s %s", r.Method, r.URL.Path)
					}
					if action == "prepare" {
						var req api.PrepareManagedPostgresCutoverRequest
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Fatal(err)
						}
						if req.SourceDatabaseID != "source-id" || req.TargetDatabaseID != "target-id" || req.AppID != "app-id" || req.Scope != "staging" {
							t.Fatalf("request %+v", req)
						}
					}
					_, _ = w.Write([]byte(`{"id":"cutover-id","source_database_id":"source-id","target_database_id":"target-id","app_id":"app-id","scope":"staging","state":"verified","verification_fresh":true,"verification_max_age_seconds":300,"verified_at":"2026-10-01T12:00:00Z","members":[{"source_binding_id":"binding-id","environment_key":"DATABASE_URL","access":"read_write","state":"sealed","verified_at":"2026-10-01T12:00:00Z"}],"ciphertext":"private-secret","provider_identity_id":"private-role"}`))
				}))
				defer srv.Close()
				t.Setenv("FAAS_API", srv.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				previousOut, previousJSON := osStdout, jsonOutput
				t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
				var out bytes.Buffer
				osStdout, jsonOutput = &out, asJSON
				args := []string{"cutover", action, "cutover-id"}
				if action == "prepare" {
					args = []string{"cutover", "prepare", "source", "target", "api", "--scope", "staging"}
				}
				if code := cmdPostgres(args); code != 0 {
					t.Fatal("command failed", code)
				}
				if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "ciphertext") {
					t.Fatal("secret exposed")
				}
				if asJSON {
					var got api.ManagedPostgresCutover
					if err := json.Unmarshal(out.Bytes(), &got); err != nil || !got.VerificationFresh || len(got.Members) != 1 {
						t.Fatal("status lost", err)
					}
				} else if !strings.Contains(out.String(), "Workloads continue using the source") {
					t.Fatal("activation boundary missing")
				}
			})
		}
	}
}
func TestCmdPostgresCutoverRejectsMalformedArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"get"}, {"verify", "id", "extra"}, {"prepare", "source", "target"}, {"prepare", "source", "target", "app", "--unexpected", "flag"}} {
		if code := cmdPostgresCutover(args); code == 0 {
			t.Fatal("malformed arguments accepted")
		}
	}
}
