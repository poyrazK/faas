// adr: 1524
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestInstanceRecoveryReusesKeyAfterLostAcceptance(t *testing.T) {
	const intentID = "11111111-1111-1111-1111-111111111111"
	for _, tc := range []struct {
		action, targetFlag, target string
		invoke                     func([]string) int
	}{
		{"force-park", "instance-id", "22222222-2222-2222-2222-222222222222", cmdInstancesForcePark},
		{"force-restart", "instance-id", "22222222-2222-2222-2222-222222222222", cmdInstancesForceRestart},
		{"force-cold-boot", "app-slug", "tenant-app", cmdInstancesForceColdBoot},
	} {
		t.Run(tc.action, func(t *testing.T) {
			posts, polls := 0, 0
			var originalKey string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					keys := r.Header.Values("Idempotency-Key")
					if len(keys) != 1 || keys[0] == "" {
						t.Errorf("ambiguous key: %v", keys)
					}
					if posts == 1 {
						originalKey = keys[0]
						// Simulate committed acceptance followed by a lost/truncated response.
						w.WriteHeader(http.StatusAccepted)
						_, _ = w.Write([]byte(`{"intent_id":`))
						return
					}
					if keys[0] != originalKey {
						t.Error("retry generated another request key")
					}
					writeTestJSON(w, http.StatusAccepted, api.OperatorIntentAcceptedResponse{OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID})
					return
				}
				polls++
				writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Status: "succeeded"})
			}))
			defer server.Close()
			installTestOperatorSession(t, server.URL, "opaque-session")
			stdout, stderr, restore := captureOperatorIO()
			defer restore()
			args := []string{"--" + tc.targetFlag + "=" + tc.target, "--reason=incident_42", "--yes", "--timeout=1s"}
			if code := tc.invoke(args); code != 1 || posts != 1 || polls != 0 {
				t.Fatalf("first exit=%d posts=%d polls=%d", code, posts, polls)
			}
			if !strings.Contains(stderr.String(), "request_key="+originalKey) {
				t.Fatal("lost response did not leave a recoverable key")
			}
			stdout.Reset()
			stderr.Reset()
			jsonOutput = true
			if code := tc.invoke(append(args, "--request-key="+originalKey)); code != 0 {
				t.Fatalf("retry exit=%d stderr=%s", code, stderr.String())
			}
			var result struct {
				RequestKey string                             `json:"request_key"`
				Operation  api.OperatorIntentAcceptedResponse `json:"operation"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.RequestKey != originalKey || result.Operation.IntentID != intentID {
				t.Fatalf("invalid JSON receipt: %s, %v", stdout.String(), err)
			}
			if posts != 2 || polls != 1 || !strings.Contains(stderr.String(), "instances wait --intent-id="+intentID) {
				t.Fatalf("posts=%d polls=%d stderr=%s", posts, polls, stderr.String())
			}
		})
	}
}

func TestInstancesWaitOnlyPollsExistingIntent(t *testing.T) {
	const intentID = "11111111-1111-1111-1111-111111111111"
	for _, status := range []string{"succeeded", "outcome_unknown", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			polls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/operator-intents/"+intentID || r.Header.Get("Idempotency-Key") != "" {
					t.Errorf("wait issued a mutation: %s %s", r.Method, r.URL)
				}
				polls++
				writeTestJSON(w, http.StatusOK, api.OperatorIntentResponse{IntentID: intentID, Kind: "force_restart", Status: status, TargetID: "original-instance", Error: "fixture outcome"})
			}))
			defer server.Close()
			installTestOperatorSession(t, server.URL, "opaque-session")
			stdout, stderr, restore := captureOperatorIO()
			defer restore()
			jsonOutput = true
			code := cmdInstancesDispatch([]string{"wait", "--intent-id=" + intentID, "--timeout=1s"})
			want := 1
			if status == "succeeded" {
				want = 0
			}
			if code != want || polls != 1 {
				t.Fatalf("exit=%d polls=%d stderr=%s", code, polls, stderr.String())
			}
			if status == "succeeded" || status == "outcome_unknown" {
				var outcome api.OperatorIntentResponse
				if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil || outcome.IntentID != intentID || outcome.Status != status {
					t.Fatalf("missing outcome: %s %v", stdout.String(), err)
				}
			}
		})
	}
}

func TestInstanceSubmissionRejectsInvalidRetryFlags(t *testing.T) {
	_, _, restore := captureOperatorIO()
	defer restore()
	for _, key := range []string{"bad key", "bad\nkey", strings.Repeat("x", api.OperatorRecoveryRequestKeyMaxBytes+1)} {
		if code := cmdInstancesForceRestart([]string{"--instance-id=22222222-2222-2222-2222-222222222222", "--yes", "--request-key=" + key}); code != 2 {
			t.Fatalf("invalid key exit=%d", code)
		}
	}
	if code := cmdInstancesForceRestart([]string{"--instance-id=22222222-2222-2222-2222-222222222222", "--yes", "--reason=incident_42", "--request-key=retry", "--break-glass-local"}); code != 2 {
		t.Fatalf("break glass accepted replay key: %d", code)
	}
	if code := cmdInstancesWait([]string{"--intent-id=bad"}); code != 2 {
		t.Fatalf("invalid wait ID exit=%d", code)
	}
}
