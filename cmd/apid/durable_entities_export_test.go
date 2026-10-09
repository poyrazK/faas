// adr: 851
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEntityExportRestoreOwnerFlow(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	hook := entityOutboxHook(t, e, app)
	request := entityRequest("export-source")
	seedAPIOutbox(t, e, app, request, hook.ID)
	bucket.mu.Lock()
	revision := bucket.rev
	bucket.mu.Unlock()
	path := "/v1/apps/" + app.Slug + "/entities/export?" + url.Values{"namespace": {request.Namespace}, "key": {request.Key}}.Encode()
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var exported api.DurableEntityStateExport
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &exported) != nil || exported.Version != 1 || exported.Checksum == "" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	bucket.mu.Lock()
	unchanged := bucket.rev == revision
	bucket.mu.Unlock()
	if !unchanged || dispatch.calls.Load() != 0 || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("export wrote, dispatched or cached")
	}
	body := api.DurableEntityRestoreRequest{Namespace: request.Namespace, Key: request.Key, RequestID: "restore-state", ExpectedVersion: 1, Export: exported}
	for _, replay := range []bool{false, true} {
		rec = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/restore", body, nil)
		var out api.DurableEntityRestoreResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Version != 2 || out.Replayed != replay {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("restore cached")
		}
	}
	body.RequestID = "stale-restore"
	rec = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/restore", body, nil)
	if rec.Code != http.StatusConflict {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body.ExpectedVersion = 2
	body.Export.Entity.Key = "foreign"
	rec = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/restore", body, nil)
	if rec.Code != http.StatusUnprocessableEntity || dispatch.calls.Load() != 0 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/restore", body, nil); rec.Code != http.StatusForbidden {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
