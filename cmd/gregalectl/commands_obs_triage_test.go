package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdObsTriageUsesSessionAndRevision(t *testing.T) {
	ref := "platform_alert/" + strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/admin/obs/incidents/"+ref+"/triage" || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Authorization") != "" {
			t.Errorf("wrong mutation: %s %s", r.Method, r.URL)
		}
		if c, err := r.Cookie("faas_sid"); err != nil || c.Value != "opaque-session" {
			t.Errorf("missing session: %v", err)
		}
		var body api.ObsAlertTriageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ExpectedRevision == nil || *body.ExpectedRevision != 7 || body.Action != "claim" {
			t.Errorf("wrong request: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(api.ObsAlertTriageResponse{IncidentID: ref, Triage: api.ObsAlertTriage{Revision: 8, Owned: true}})
	}))
	defer srv.Close()
	installTestOperatorSession(t, srv.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdObsDispatch([]string{subObsTriage, "--incident", ref, "--action", "claim", "--expected-revision", "7", "--json"}); code != 0 {
		t.Fatalf("exit=%d err=%s", code, stderr)
	}
	var result api.ObsAlertTriageResponse
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Triage.Revision != 8 {
		t.Fatalf("receipt: %s %v", out, err)
	}
}

func TestCmdObsTriageRejectsUnsafeInput(t *testing.T) {
	base := []string{"--incident", "platform_alert/" + strings.Repeat("a", 64), "--action", "claim", "--expected-revision", "0"}
	for _, args := range [][]string{{}, base[:4], append(append([]string{}, base...), "--until", time.Now().Add(time.Hour).Format(time.RFC3339)), {"--incident", "deployment/11111111-1111-4111-8111-111111111111", "--action", "claim", "--expected-revision", "0"}, {"--incident", "platform_alert/" + strings.Repeat("a", 64), "--action", "snooze", "--expected-revision", "0", "--until", time.Now().Add(25 * time.Hour).Format(time.RFC3339)}} {
		if code := cmdObsTriage(args); code != 2 {
			t.Errorf("args=%v exit=%d", args, code)
		}
	}
}

func TestObsBundleTriageRedactsOwner(t *testing.T) {
	owner := "11111111-1111-4111-8111-111111111111"
	until := time.Now().UTC().Add(time.Hour)
	b := obsIncidentBundle{}
	b.Evidence.Data.Incident.Triage = &api.ObsAlertTriage{Revision: 3, Acknowledged: true, Owned: true, OwnerAccountID: owner, Snoozed: true, SnoozedUntil: &until}
	body, err := marshalObsBundle(b)
	if err != nil || strings.Contains(string(body), owner) || strings.Contains(string(body), "owner_account_id") || !strings.Contains(string(body), `"owned": true`) {
		t.Fatalf("redaction: %s %v", body, err)
	}
	if b.Evidence.Data.Incident.Triage.OwnerAccountID != owner {
		t.Fatal("redaction mutated caller")
	}
}

func TestCmdObsIncidentsTriageFiltersUseSessionForMine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, value := range map[string]string{"type": "platform_alert", "ownership": "mine", "acknowledgment": "unacknowledged", "snooze": "inactive", "alert_status": "firing"} {
			if r.URL.Query().Get(key) != value {
				t.Errorf("filter %s=%q", key, r.URL.Query().Get(key))
			}
		}
		if c, err := r.Cookie("faas_sid"); err != nil || c.Value != "opaque-session" {
			t.Errorf("session: %v", err)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("mine used bearer")
		}
		_ = json.NewEncoder(w).Encode(api.ObsIncidentListResponse{Items: []api.ObsIncident{}})
	}))
	defer srv.Close()
	installTestOperatorSession(t, srv.URL, "opaque-session")
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	previous := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = previous; _ = r.Close(); _ = w.Close() })
	code := cmdObsIncidents([]string{"--type", "platform_alert", "--ownership", "mine", "--acknowledgment", "unacknowledged", "--snooze", "inactive", "--alert-status", "firing", "--json"})
	_ = w.Close()
	var response api.ObsIncidentListResponse
	if err := json.NewDecoder(r).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
}
