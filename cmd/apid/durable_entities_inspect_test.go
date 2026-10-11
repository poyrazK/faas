// adr: 935
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEntityInspectionReadScopeAndMetadata(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	hook := entityOutboxHook(t, e, app)
	request := entityRequest("inspection")
	work := seedAPIOutbox(t, e, app, request, hook.ID)
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	bucket.mu.Lock()
	before := bucket.rev
	bucket.mu.Unlock()
	path := "/v1/apps/" + app.Slug + "/entities/inspect?" + url.Values{"namespace": {request.Namespace}, "key": {request.Key}}.Encode()
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var out api.DurableEntityInspectResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Version != 1 || !out.StateCommitted || out.Outbox.Pending != 1 || out.Outbox.HeadID != work.MessageID || out.Outbox.HeadDelivery == nil || out.Outbox.HeadDelivery.Status != "unknown" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || dispatch.calls.Load() != 0 {
		t.Fatal("inspection cached or dispatched guest")
	}
	bucket.mu.Lock()
	after := bucket.rev
	bucket.mu.Unlock()
	if before != after {
		t.Fatal("inspection wrote bucket")
	}
	for _, forbidden := range []string{`"data"`, `"payload"`, `"claim_token"`, `"snapshot_key"`, `"owner"`, `"receipts"`} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatal("private state exposed", forbidden)
		}
	}
	for _, query := range []string{"namespace=x", "namespace=x&key=y&key=z", "namespace=x&key=y&account_id=other", "namespace=x&key=y&environment="} {
		bad := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/entities/inspect?"+query, nil, nil)
		if bad.Code != http.StatusBadRequest {
			t.Fatal(query, bad.Code, bad.Body.String())
		}
	}
	missing := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/entities/inspect?namespace=x&key=absent", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatal(missing.Code, missing.Body.String())
	}
}
