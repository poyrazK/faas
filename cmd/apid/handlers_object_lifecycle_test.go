package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectLifecycleControlValidationAndOwnership(t *testing.T) {
	e := setup(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "lifecycle")
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	b := bucketResponse(t, e.do(t, "POST", "/v1/apps/lifecycle/buckets", map[string]any{"name": "assets"}, nil), 201)
	path := "/v1/apps/lifecycle/buckets/" + b.ID + "/lifecycle"
	for _, body := range []any{map[string]any{}, map[string]any{"rules": []any{}}, map[string]any{"rules": nil}, map[string]any{"unknown": true, "rules": []any{}}, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "expiration": map[string]any{"days": 0}}}}, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "transition": true}}}} {
		if r := e.do(t, "PUT", path, body, nil); r.Code != 400 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	// A backend with only current-object operations cannot accept expiration.
	if r := e.do(t, "PUT", path, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "expiration": map[string]any{"days": 1}}}}, nil); r.Code != 501 {
		t.Fatal(r.Code, r.Body.String())
	}
	days := int32(1)
	in := api.ObjectBucketLifecycleRequest{Rules: []api.ObjectLifecycleRule{{Status: "Enabled", AbortIncompleteMultipartDays: &days}}}
	if r := e.do(t, "PUT", path, in, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "lifecycle-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 403 {
		t.Fatal("ungranted manager", r.Code, r.Body.String())
	}
	st := e.store
	if _, err = st.SetObjectBucketAccessGrant(t.Context(), e.acct.ID, b.ID, key.ID, state.ObjectBucketPermissionRead); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 403 {
		t.Fatal("read grant managed policy", r.Code, r.Body.String())
	}
	if _, err = st.SetObjectBucketAccessGrant(t.Context(), e.acct.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	other, err := e.store.CreateAccount(t.Context(), "other-lifecycle@example.test", api.PlanHobby)
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
		t.Fatal("cross-account policy", r.Code, r.Body.String())
	}
	if err = e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.do(t, "DELETE", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.do(t, "PUT", path, in, headers); r.Code != 503 {
		t.Fatal(r.Code, r.Body.String())
	}
}
