package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionRecoveryCLIRequiresMatchingReceipt(t *testing.T) {
	for _, match := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "mismatched"}[match], func(t *testing.T) {
			resetJSONOut(t)
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var out, errOut bytes.Buffer
			osStdout, osStderr, jsonOutput = &out, &errOut, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/recover-migration") {
					t.Error("unexpected recovery route")
				}
				var request api.ProjectPromotionMigrationRecoveryRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if !request.Valid() {
					t.Error("invalid recovery body")
				}
				if !match {
					request.EvidenceHash = strings.Repeat("c", 64)
				}
				writeJSONTest(w, api.ProjectPromotionMigrationRecovery{Request: request, Status: "quiescing", OperatorAccountID: "account", RequestedAt: time.Now(), Deadline: time.Now().Add(time.Minute)})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			code := cmdProjectsEnvironmentRecoverMigration([]string{"shop", "00000000-0000-0000-0000-000000000002", "--to", "production", "--action", "resume", "--task-id", "00000000-0000-0000-0000-000000000001", "--generation", "5", "--database-state-checked", "--schema-compatible", "--evidence-sha256", strings.Repeat("a", 64)})
			if match && code != 0 || !match && code != 1 {
				t.Fatalf("exit=%d output=%s errors=%s", code, &out, &errOut)
			}
		})
	}
}
