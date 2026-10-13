package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectLifecycleControlValidationAndOwnership(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "lifecycle")
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	b := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/lifecycle/buckets", map[string]any{"name": "assets"}, nil), 201)
	path := "/v1/apps/lifecycle/buckets/" + b.ID + "/lifecycle"
	for _, body := range []any{map[string]any{}, map[string]any{"rules": []any{}}, map[string]any{"rules": nil}, map[string]any{"unknown": true, "rules": []any{}}, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "expiration": map[string]any{"days": 0}}}}, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "transition": true}}}} {
		if r := e.Do(t, "PUT", path, body, nil); r.Code != 400 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	// A backend with only current-object operations cannot accept expiration.
	if r := e.Do(t, "PUT", path, map[string]any{"rules": []any{map[string]any{"status": "Enabled", "expiration": map[string]any{"days": 1}}}}, nil); r.Code != 501 {
		t.Fatal(r.Code, r.Body.String())
	}
	days := int32(1)
	in := api.ObjectBucketLifecycleRequest{Rules: []api.ObjectLifecycleRule{{Status: "Enabled", AbortIncompleteMultipartDays: &days}}}
	if r := e.Do(t, "PUT", path, in, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "lifecycle-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	if r := e.Do(t, "GET", path, nil, headers); r.Code != 403 {
		t.Fatal("ungranted manager", r.Code, r.Body.String())
	}
	st := (*e.Store())
	if _, err = st.SetObjectBucketAccessGrant(t.Context(), (*e.Acct()).ID, b.ID, key.ID, state.ObjectBucketPermissionRead); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "GET", path, nil, headers); r.Code != 403 {
		t.Fatal("read grant managed policy", r.Code, r.Body.String())
	}
	if _, err = st.SetObjectBucketAccessGrant(t.Context(), (*e.Acct()).ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "GET", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "other-lifecycle@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "GET", path, nil, map[string]string{"Authorization": "Bearer " + plain}); r.Code != 404 {
		t.Fatal("cross-account policy", r.Code, r.Body.String())
	}
	if err = (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "GET", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.Do(t, "DELETE", path, nil, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.Do(t, "PUT", path, in, headers); r.Code != 503 {
		t.Fatal(r.Code, r.Body.String())
	}
}
