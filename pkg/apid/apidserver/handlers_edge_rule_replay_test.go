package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestEdgeRulePatchGuardedReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "cache-replay")
	req := api.CreateEdgeRuleRequest{Kind: "cache", MatchHost: api.EdgeRuleAppHosts, MatchPath: "/catalog/*", Action: json.RawMessage(`{"max_age_seconds":30,"stale_if_error_seconds":0}`)}
	rec := e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created api.EdgeRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	action := json.RawMessage(`{"max_age_seconds":120,"stale_if_error_seconds":0}`)
	patch := api.UpdateEdgeRuleRequest{Action: &action}
	headers := map[string]string{"If-Match": rec.Header().Get("ETag"), "Idempotency-Key": "cache-replay-key"}
	path := "/v1/edge-rules/" + created.ID
	first := e.Do(t, http.MethodPatch, path, patch, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", first.Code, first.Body.String())
	}
	// The successful update advanced the version; a retry with the same key
	// must recover the original response before the now-stale guard is checked.
	again := e.Do(t, http.MethodPatch, path, patch, headers)
	if again.Code != http.StatusOK || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", again.Code, again.Body.String())
	}
	headers["Idempotency-Key"] = "another-review"
	stale := e.Do(t, http.MethodPatch, path, patch, headers)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body.String())
	}
	versions := e.Do(t, http.MethodGet, "/v1/apps/"+slug+"/edge-rules/versions", nil, nil)
	var history []api.EdgeRuleSetVersionResponse
	if err := json.Unmarshal(versions.Body.Bytes(), &history); err != nil || len(history) != 2 {
		t.Fatalf("replay created a version: %s", versions.Body.String())
	}
}
