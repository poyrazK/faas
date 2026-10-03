// adr: 429
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRealVMScenarioCleanupPhaseReportsActualCleanupAfterFailure(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		registrationFails, destroyFails, namespaceFails bool
	}{
		{"registered cleanup succeeds", false, false, false},
		{"registration failure still cleans own workload", true, false, false},
		{"destroy failure retains namespace", false, true, false},
		{"namespace cleanup failure", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSuiteTestFile(t, filepath.Join(dir, "package.json"), `{"name":"cleanup-test","scripts":{"start":"node server.js"}}`)
			writeSuiteTestFile(t, filepath.Join(dir, "server.js"), "require('http').createServer((r,s)=>s.end('ok')).listen(8080);\n")
			destroyed, released := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/cleanup-api":
					_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "owned-cleanup-app"}})
				case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
					if tc.registrationFails {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(204)
					}
				case r.Method == "PATCH" && r.URL.Path == "/v1/apps/owned-cleanup-app":
					w.WriteHeader(500) // Fail before deployment; cleanup must still execute.
				case r.Method == "DELETE" && r.URL.Path == "/v1/dev/sessions/cleanup-api":
					destroyed++
					if r.URL.Query().Get("workspace_id") == "" {
						t.Error("destroy omitted exact workspace identity")
					}
					if tc.destroyFails {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(204)
					}
				case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
					released++
					if tc.namespaceFails {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(204)
					}
				default:
					_, _ = w.Write([]byte(`{}`)) // Bounded diagnostics, before teardown.
				}
			}))
			defer server.Close()
			receipt := runTestProfile(context.Background(), NewClient(server.URL, "synthetic-key"), "cleanup", testScenario{Project: "cleanup-api", Source: "."}, dir, "warm")
			if receipt.Status != "failed" || receipt.Error == "" || destroyed != 1 {
				t.Fatalf("receipt=%+v destroy=%d", receipt, destroyed)
			}
			wantReleased := 1
			if tc.registrationFails || tc.destroyFails {
				wantReleased = 0
			}
			if released != wantReleased {
				t.Fatalf("namespace deletes=%d, want %d", released, wantReleased)
			}
			cleanupStatus := "passed"
			if tc.destroyFails || tc.namespaceFails {
				cleanupStatus = "failed"
			}
			if (receipt.CleanupError != "") != (cleanupStatus == "failed") {
				t.Fatalf("cleanup error=%q, status=%s", receipt.CleanupError, cleanupStatus)
			}
			if len(receipt.Phases) != 2 || receipt.Phases[0].Name != "provision" || receipt.Phases[0].Status != "failed" || receipt.Phases[1].Name != "cleanup" || receipt.Phases[1].Status != cleanupStatus {
				t.Fatalf("phases=%+v", receipt.Phases)
			}
		})
	}
}

func TestWarmServiceEvidenceRejectsSameMinuteSmokeAndPlatformRejection(t *testing.T) {
	// A new exact timestamp must work even when baseline and trigger share a
	// minute. Late smoke, old ID and a platform response are never evidence.
	cutoff := time.Date(2026, 10, 2, 3, 1, 17, 0, time.UTC)
	requests := []api.DebugTelemetryRequestItem{
		{ID: "real", InstanceID: "selected", ReceivedAt: cutoff.Add(time.Millisecond).Format(time.RFC3339Nano)},
		{ID: "platform", ReceivedAt: cutoff.Add(time.Second).Format(time.RFC3339Nano)},
		{ID: "late-smoke", InstanceID: "selected", ColdBoot: true, ReceivedAt: cutoff.Add(-time.Millisecond).Format(time.RFC3339Nano)},
		{ID: "old", InstanceID: "selected", ReceivedAt: cutoff.Add(time.Millisecond).Format(time.RFC3339Nano)},
	}
	row, ok := firstNewTestServiceRequest(requests, map[string]bool{"old": true}, cutoff)
	if !ok || row.ID != "real" {
		t.Fatalf("accepted wrong evidence: %+v/%v", row, ok)
	}
	if _, ok := firstNewTestServiceRequest(requests[1:], map[string]bool{"old": true}, cutoff); ok {
		t.Fatal("nonhandled or pretrigger request satisfied evidence")
	}
}
