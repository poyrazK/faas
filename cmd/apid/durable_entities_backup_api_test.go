// adr: 942
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBackupReadsAndRestorePreviewRemainReadOnly(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	request := entityRequest("backup-source")
	hook := entityOutboxHook(t, e, app)
	seedAPIOutbox(t, e, app, request, hook.ID)
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
	if problem != nil {
		t.Fatal(problem)
	}
	info, err := e.s.durableEntities.BackupState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	bucket.mu.Lock()
	revision := bucket.rev
	bucket.mu.Unlock()
	query := url.Values{"namespace": {request.Namespace}, "key": {request.Key}}
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/entities/backups?"+query.Encode(), nil, nil)
	var list api.DurableEntityBackupPage
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].ID != info.ID {
		t.Fatal(rec.Code, rec.Body.String())
	}
	query.Set("backup_id", info.ID)
	rec = e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/entities/backups/get?"+query.Encode(), nil, nil)
	var backup api.DurableEntityBackup
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &backup) != nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body := api.DurableEntityRestoreRequest{Namespace: request.Namespace, Key: request.Key, RequestID: "preview", ExpectedVersion: 1, Export: backup.Export}
	rec = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/restore/preview", body, nil)
	var preview api.DurableEntityRestorePreview
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &preview) != nil || !preview.ExpectedVersionMatches || preview.OutboxPending != 1 || preview.Compatibility != "unverified" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || dispatch.calls.Load() != 0 {
		t.Fatal("preview cached or invoked guest")
	}
	bucket.mu.Lock()
	unchanged := bucket.rev == revision
	bucket.mu.Unlock()
	if !unchanged {
		t.Fatal("backup read or preview wrote bucket")
	}
	query.Set("backup_id", "../manifest")
	if rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/entities/backups/get?"+query.Encode(), nil, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
