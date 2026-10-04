package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 552
func TestObjectNotificationControlOwnershipAndValidation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "notifications")
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	b := bucketResponse(t, e.do(t, "POST", "/v1/apps/notifications/buckets", map[string]any{"name": "assets"}, nil), 201)
	path := "/v1/apps/notifications/buckets/" + b.ID + "/notifications"
	a, err := e.store.AppBySlug(t.Context(), "notifications")
	if err != nil {
		t.Fatal(err)
	}
	rule := api.ObjectNotificationRule{ID: "images", Destination: "arn:gregale:lambda:" + b.Region + ":" + uuid.MustParse(e.acct.ID).String() + ":function:" + uuid.MustParse(a.ID).String(), Events: []string{"s3:ObjectCreated:Put"}}
	in := api.ObjectBucketNotificationsRequest{Rules: []api.ObjectNotificationRule{rule}}
	if r := e.do(t, "PUT", path, in, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, body := range []any{map[string]any{}, map[string]any{"rules": nil}, map[string]any{"unknown": true, "rules": []any{}}, map[string]any{"rules": []any{map[string]any{"destination": rule.Destination, "events": []string{"s3:Unknown:*"}}}}} {
		if r := e.do(t, "PUT", path, body, nil); r.Code != 400 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "notify-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 403 {
		t.Fatal("ungranted manager", r.Code, r.Body.String())
	}
	if _, err = e.store.SetObjectBucketAccessGrant(t.Context(), e.acct.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	other, err := e.store.CreateAccount(t.Context(), "other-notifications@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, map[string]string{"Authorization": "Bearer " + plain}); r.Code != 404 {
		t.Fatal("foreign account", r.Code, r.Body.String())
	}
	if err = e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if r := e.do(t, method, path, nil, headers); r.Code != 200 {
			t.Fatal(method, r.Code, r.Body.String())
		}
	}
	if r := e.do(t, "PUT", path, in, headers); r.Code != 503 {
		t.Fatal("disabled intent", r.Code, r.Body.String())
	}
}
