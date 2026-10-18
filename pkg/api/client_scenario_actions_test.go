package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterScenarioTestRequiresCaptureAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"legacy-no-content", "", 204, true},
		{"missing-policy", `{}`, 200, true},
		{"wrong-version", `{"webhook_mode":"capture","policy_version":2}`, 200, true},
		{"wrong-mode", `{"webhook_mode":"live","policy_version":1}`, 200, true},
		{"capture-applied", `{"webhook_mode":"capture","policy_version":1}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WebhookMode != "capture" {
					t.Errorf("capture request lost: %+v %v", req, err)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewClient(server.URL, "synthetic-key")
			err := client.RegisterScenarioTest(t.Context(), "abababababababababababababababab", RegisterScenarioTestRequest{WebhookMode: "capture", Members: []ScenarioTestWorkload{{Workload: "api", AppSlug: "test-app"}}})
			if (err != nil) != tc.wantError {
				t.Fatalf("registration error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}

func TestRegisterScenarioTestRequiresSealedEvidenceAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"old-capture-server", `{"webhook_mode":"capture","policy_version":1}`, true},
		{"unsupported-evidence", `{"webhook_mode":"capture","policy_version":1,"action_evidence_version":2}`, true},
		{"snapshot-supported", `{"webhook_mode":"capture","policy_version":1,"action_evidence_version":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ActionEvidenceVersion != ScenarioActionSnapshotVersion {
					t.Errorf("evidence capability request lost: %+v %v", req, err)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			err := NewClient(server.URL, "synthetic-key").RegisterScenarioTest(t.Context(), strings.Repeat("a", 32), RegisterScenarioTestRequest{WebhookMode: "capture", ActionEvidenceVersion: 1})
			if (err != nil) != tc.wantError {
				t.Fatalf("registration error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
	client := NewClient("http://127.0.0.1:1", "synthetic-key")
	for _, req := range []RegisterScenarioTestRequest{{ActionEvidenceVersion: 1}, {WebhookMode: "capture", ActionEvidenceVersion: 3}} {
		if err := client.RegisterScenarioTest(t.Context(), strings.Repeat("a", 32), req); err == nil || !strings.Contains(err.Error(), "supported evidence version") {
			t.Fatalf("unsupported local request sent: %v", err)
		}
	}
}

func TestSealScenarioTestActionsRejectsMissingStaleOrInvalidAcknowledgements(t *testing.T) {
	const runID = "abababababababababababababababab"
	for _, mode := range []string{"complete", "pending", "incomplete", "no-content", "empty", "stale", "scope", "version", "unknown-state", "unsealed", "false-pending"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := ScenarioActionSnapshot{Version: ScenarioActionSnapshotVersion, Scope: ScenarioActionScope, RunID: runID, Status: "complete", SealedAt: "2026-10-16T21:30:00Z", Workloads: []string{"primary"}, Intents: []ScenarioCapturedAction{}}
			status := 200
			switch mode {
			case "pending":
				snapshot.Status, snapshot.SealedAt, snapshot.PendingManagedWork, status = "pending", "", 1, 202
			case "incomplete":
				snapshot.Status, snapshot.IncompleteManagedEffects = "incomplete", 1
			case "stale":
				snapshot.RunID = strings.Repeat("c", 32)
			case "scope":
				snapshot.Scope = "all_actions"
			case "version":
				snapshot.Version++
			case "unknown-state":
				snapshot.Status = "okay"
			case "unsealed":
				snapshot.SealedAt = ""
			case "false-pending":
				snapshot.Status, snapshot.SealedAt = "pending", ""
			case "no-content":
				status = 204
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/dev/test-runs/"+runID+"/actions/seal" {
					t.Errorf("wrong seal request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
				if mode != "no-content" && mode != "empty" {
					_ = json.NewEncoder(w).Encode(snapshot)
				}
			}))
			defer server.Close()
			out, err := NewClient(server.URL, "synthetic-key").SealScenarioTestActions(t.Context(), runID)
			valid := mode == "complete" || mode == "pending" || mode == "incomplete"
			if (err == nil) != valid || valid && out.Status != snapshot.Status {
				t.Fatalf("seal %+v error=%v valid=%v", out, err, valid)
			}
		})
	}
}
