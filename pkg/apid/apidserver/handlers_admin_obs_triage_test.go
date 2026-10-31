package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestObsAlertTriageMutationAndFilters(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	episode := seedObsAlert(t, store, now, true)
	path := "/v1/admin/obs/incidents/platform_alert/" + episode.ID + "/triage"
	send := func(body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		e.AddAdminSession(t, req)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	for _, body := range []string{`{"action":"acknowledge"}`, `{"action":"resolve","expected_revision":0}`, `{"action":"snooze","expected_revision":0}`, `{"action":"claim","expected_revision":0,"owner_account_id":"someone"}`} {
		if rec := send(body, body); rec.Code != 400 {
			t.Fatalf("invalid: %d %s", rec.Code, rec.Body)
		}
	}
	body := `{"action":"acknowledge","expected_revision":0}`
	first := send(body, "triage-ack")
	if first.Code != 200 {
		t.Fatalf("ack: %d %s", first.Code, first.Body)
	}
	retry := send(body, "triage-ack")
	if retry.Code != 200 || retry.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", retry.Code, retry.Body)
	}
	if rec := send(body, "triage-stale"); rec.Code != 409 {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body)
	}
	if rec := send(`{"action":"claim","expected_revision":1}`, "triage-claim"); rec.Code != 200 {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"acknowledgment=acknowledged", "ownership=owned", "ownership=mine", "snooze=inactive"} {
		rec := e.DoOperator(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&"+query, nil, nil)
		var out api.ObsIncidentListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || len(out.Items) != 1 || out.Items[0].Triage == nil || out.Items[0].Triage.Revision != 2 || out.Items[0].Status != "firing" {
			t.Fatalf("filter %s: %d %s", query, rec.Code, rec.Body)
		}
	}
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/incidents/platform_alert/"+episode.ID+"/evidence", nil, nil)
	var evidence api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Incident.Triage == nil || !evidence.Incident.Triage.Acknowledged {
		t.Fatalf("missing evidence triage: %s", rec.Body)
	}
	for _, query := range []string{"acknowledgment=acknowledged", "type=deployment&ownership=owned", "type=platform_alert&snooze=forever"} {
		if rec := e.DoOperator(t, "GET", "/v1/admin/obs/incidents?"+query, nil, nil); rec.Code != 400 {
			t.Fatalf("bad filter %s: %d", query, rec.Code)
		}
	}
	path = "/v1/admin/obs/incidents/platform_alert/" + episode.SeriesID + "/triage"
	if rec := send(`{"action":"acknowledge","expected_revision":0}`, "triage-alias"); rec.Code != 404 {
		t.Fatalf("alias: %d %s", rec.Code, rec.Body)
	}
}

func TestObsAlertTriageSecurity(t *testing.T) {
	e := gapEnv(t)
	path := "/v1/admin/obs/incidents/platform_alert/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/triage"
	rec := e.Do(t, "POST", path, nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodeStepUpRequired)
	for _, tc := range []struct {
		origin, key string
		status      int
	}{{"https://attacker.example", "a", 403}, {"", "", 400}} {
		req := httptest.NewRequest("POST", path, bytes.NewBufferString(`{"action":"claim","expected_revision":0}`))
		e.AddAdminSession(t, req)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Idempotency-Key", tc.key)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("security: %d %s", rec.Code, rec.Body)
		}
	}
	e = apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithAdminAllowlist("another@example.test")
	req := httptest.NewRequest("POST", path, nil)
	e.AddAdminSession(t, req)
	req.Header.Set("Idempotency-Key", "forbidden")
	rec = httptest.NewRecorder()
	(*e.S()).Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("allowlist: %d %s", rec.Code, rec.Body)
	}
}
