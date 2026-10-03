package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type responseCachePurgeRecorderFake struct {
	state.Store
	appID    string
	pathGlob string
	tag      string
	calls    int
}

func (f *responseCachePurgeRecorderFake) CreateResponseCachePurge(_ context.Context, appID, pathGlob, tag string) (int64, error) {
	f.appID, f.pathGlob, f.tag = appID, pathGlob, tag
	f.calls++
	return int64(f.calls), nil
}

func TestPurgeAppCacheUsesDurablePurgeStore(t *testing.T) {
	e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedApp(t, e, "catalog")
	store := &responseCachePurgeRecorderFake{Store: e.s.store}
	e.s.store = store
	e.s.notif = nil // the durable store commits the notification with the ledger row
	req := httptest.NewRequest(http.MethodDelete, "/v1/apps/catalog/cache?tag=Product%3A42", nil)
	req.SetPathValue("slug", "catalog")
	rec := httptest.NewRecorder()
	e.s.purgeAppCache(rec, req, e.acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.appID != app.ID || store.pathGlob != "" || store.tag != "product:42" {
		t.Fatalf("durable purge call = %+v; want app %s and normalized tag", store, app.ID)
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.emitted) != 0 {
		t.Fatalf("notifier fallback emitted %v despite durable store", notifier.emitted)
	}
}

func TestPurgeAppCacheTagNotification(t *testing.T) {
	e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedApp(t, e, "catalog")
	for _, tc := range []struct {
		query      string
		wantStatus int
		wantTag    string
	}{
		{"tag=Product%3A42", http.StatusNoContent, "product:42"},
		{"tag=", http.StatusBadRequest, ""},
		{"tag=bad+tag", http.StatusBadRequest, ""},
		{"tag=product%3A42&tag=other", http.StatusBadRequest, ""},
		{"tag=product%3A42&path=%2Fproducts%2F%2A", http.StatusBadRequest, ""},
		{"tag=product%3A42&path=", http.StatusBadRequest, ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			notifier.mu.Lock()
			notifier.emitted = nil
			notifier.mu.Unlock()
			req := httptest.NewRequest(http.MethodDelete, "/v1/apps/catalog/cache?"+tc.query, nil)
			req.SetPathValue("slug", "catalog")
			rec := httptest.NewRecorder()
			e.s.purgeAppCache(rec, req, e.acct)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			notifier.mu.Lock()
			defer notifier.mu.Unlock()
			if tc.wantTag == "" {
				if len(notifier.emitted) != 0 {
					t.Errorf("invalid purge emitted %v", notifier.emitted)
				}
				return
			}
			if len(notifier.emitted) != 1 || notifier.emitted[0].Channel != db.NotifyCachePurge {
				t.Fatalf("notifications = %v", notifier.emitted)
			}
			var payload struct {
				AppID string `json:"app_id"`
				Tag   string `json:"tag"`
			}
			if err := json.Unmarshal([]byte(notifier.emitted[0].Payload), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.AppID != app.ID || payload.Tag != tc.wantTag {
				t.Errorf("payload = %+v, want app %s tag %s", payload, app.ID, tc.wantTag)
			}
		})
	}
}
