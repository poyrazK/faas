package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestScenarioTestConsumerIntentHTTP(t *testing.T) {
	// adr: 984 — authenticated test routes bind receipts to the registered app and input.
	e := apidserver.Setup(t, api.PlanPro)
	run := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rec := e.Do(t, "PUT", "/v1/dev/sessions/intent-http", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	path := "/v1/dev/test-runs/" + run + "/consumers/customer"
	req := api.ScenarioTestConsumerRequest{AppID: session.App.ID, ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
	if r := e.Do(t, "PUT", path, req, nil); r.Code != 404 {
		t.Fatal("unregistered app", r.Code, r.Body.String())
	}
	if r := e.Do(t, "PUT", "/v1/dev/test-runs/"+run, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{{Workload: "api", AppSlug: session.App.Slug}}}, nil); r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	var id string
	for i := 0; i < 2; i++ {
		r := e.Do(t, "PUT", path, req, nil)
		var receipt api.ScenarioTestConsumerReceipt
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &receipt) != nil || receipt.Consumer == nil {
			t.Fatal(r.Code, r.Body.String())
		}
		if id != "" && id != receipt.Consumer.ID {
			t.Fatal("new consumer on retry")
		}
		id = receipt.Consumer.ID
	}
	query := url.Values{"app_id": {req.AppID}, "expires_at": {req.ExpiresAt.Format(time.RFC3339Nano)}}
	if r := e.Do(t, "GET", path+"?"+query.Encode(), nil, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	missingPath := "/v1/dev/test-runs/" + run + "/consumers/missing"
	cancelled := e.Do(t, "DELETE", missingPath+"?"+query.Encode(), nil, nil)
	var proof api.ScenarioTestConsumerReceipt
	if cancelled.Code != 200 || json.Unmarshal(cancelled.Body.Bytes(), &proof) != nil || proof.SchemaVersion != 2 || !proof.Cancelled || proof.Consumer != nil {
		t.Fatal("cancellation route", cancelled.Code, cancelled.Body.String())
	}
	if late := e.Do(t, "PUT", missingPath, req, nil); late.Code != 409 {
		t.Fatal("late creation", late.Code, late.Body.String())
	}
	changed := req
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Hour)
	if r := e.Do(t, "PUT", path, changed, nil); r.Code != http.StatusConflict {
		t.Fatal("changed input", r.Code, r.Body.String())
	}
	if r := e.Do(t, "DELETE", "/v1/dev/sessions/intent-http?workspace_id="+run, nil, nil); r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	query.Set("consumer_id", session.App.ID)
	if r := e.Do(t, "DELETE", path+"?"+query.Encode(), nil, nil); r.Code != 409 {
		t.Fatal("wrong consumer ID", r.Code, r.Body.String())
	}
	query.Set("consumer_id", id)
	if r := e.Do(t, "DELETE", path+"?"+query.Encode(), nil, nil); r.Code != 200 {
		t.Fatal("post-teardown revoke", r.Code, r.Body.String())
	}
	r := e.Do(t, "GET", path+"?"+query.Encode(), nil, nil)
	var receipt api.ScenarioTestConsumerReceipt
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &receipt) != nil || receipt.Consumer == nil || receipt.Consumer.ID != id || receipt.Consumer.Status != "revoked" {
		t.Fatal("receipt after teardown", r.Code, r.Body.String())
	}
}
