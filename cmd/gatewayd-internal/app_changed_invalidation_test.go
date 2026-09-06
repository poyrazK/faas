package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

func TestAppChangedInvalidatesDecodedApp(t *testing.T) {
	for _, payload := range []string{
		`{"kind":"updated","slug":"function","app_id":"app-7","lifecycle_changed":true}`,
		`{"kind":"parked","app_id":"app-7"}`,
		`{"kind":"woken","app_id":"app-7"}`,
		"app-7",
		"  app-7\n",
	} {
		t.Run(payload, func(t *testing.T) {
			inv := &fakeInvalidator{}
			handleInvalidation(context.Background(), inv, db.Notification{Channel: db.NotifyAppChanged, Payload: payload}, testLogger())
			if !reflect.DeepEqual(inv.resetApps, []string{"app-7"}) {
				t.Fatalf("app invalidations = %q", inv.resetApps)
			}
			if !reflect.DeepEqual(inv.responseCacheByApp, []string{"app-7"}) {
				t.Fatalf("response invalidations = %q", inv.responseCacheByApp)
			}
			if inv.flushCnt != 0 || inv.responseCacheAll != 0 {
				t.Fatal("valid app notification flushed unrelated apps")
			}
		})
	}
}
func TestAppChangedUnknownAppFlushesCaches(t *testing.T) {
	for _, payload := range []string{"", " ", `{}`, `{"kind":"updated"}`, `{"app_id":42}`, `{"app_id":`, `null`, `[]`, `"app-7"`} {
		t.Run(payload, func(t *testing.T) {
			inv := &fakeInvalidator{}
			handleInvalidation(context.Background(), inv, db.Notification{Channel: db.NotifyAppChanged, Payload: payload}, testLogger())
			if inv.flushCnt != 1 || inv.responseCacheAll != 1 {
				t.Fatalf("flushes = %d/%d, want route and response invalidation", inv.flushCnt, inv.responseCacheAll)
			}
			if len(inv.resetApps) != 0 || len(inv.responseCacheByApp) != 0 {
				t.Fatal("used malformed payload as an app ID")
			}
		})
	}
}
