package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1036
func TestObjectComputeTriggerAPIMem(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	objectComputeTriggerAPISuite(t, devBucketsFixture{server: (*e.S()), store: (*e.Store()), account: (*e.Acct()), do: e.Do})
}
func objectComputeTriggerAPISuite(t *testing.T, e devBucketsFixture) {
	t.Helper()
	createdApp := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "trigger-worker"}, nil)
	if createdApp.Code != 201 {
		t.Fatal(createdApp.Code, createdApp.Body.String())
	}
	app, err := e.store.AppBySlug(t.Context(), "trigger-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	e.server.WithObjectStorage(apidserver.ObjectRegistry(t, newStorageTestObjectProvider(map[string][]byte{}, nil), newStorageTestObjectProvider(nil, nil), "external"))
	created := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/buckets", map[string]any{"name": "assets"}, nil)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var bucket api.ObjectBucket
	if err := json.Unmarshal(created.Body.Bytes(), &bucket); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/buckets/" + bucket.ID + "/compute-triggers"
	for _, bad := range []any{
		map[string]any{"triggers": []any{}}, map[string]any{"expected_revision": 0}, map[string]any{"expected_revision": 0, "triggers": nil},
		map[string]any{"expected_revision": -1, "triggers": []any{}}, map[string]any{"expected_revision": 0, "triggers": []any{}, "destination": "other"},
		map[string]any{"expected_revision": 0, "triggers": []any{map[string]any{"id": "x", "destination": "other"}}},
	} {
		response := e.do(t, http.MethodPut, path, bad, nil)
		if response.Code != 400 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	revision := int64(0)
	request := api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &revision, Triggers: []api.ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/"}}}
	response := e.do(t, http.MethodPut, path, request, nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var policy api.ObjectBucketNotifications
	if err := json.Unmarshal(response.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 1 || len(policy.ManagedRuleIDs) != 1 {
		t.Fatal(policy)
	}
	plain, hash, keyErr := api.GenerateAPIKey()
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	key, keyErr := e.store.CreateAPIKey(t.Context(), e.account.ID, hash, "trigger-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	request.ExpectedRevision = &policy.Revision
	if r := e.do(t, http.MethodPut, path, request, headers); r.Code != 403 {
		t.Fatal("missing bucket grant", r.Code, r.Body.String())
	}
	if _, keyErr = e.store.(state.ObjectBucketAccessStore).SetObjectBucketAccessGrant(t.Context(), e.account.ID, bucket.ID, key.ID, state.ObjectBucketPermissionRead); keyErr != nil {
		t.Fatal(keyErr)
	}
	if r := e.do(t, http.MethodPut, path, request, headers); r.Code != 403 {
		t.Fatal("read-only grant", r.Code, r.Body.String())
	}
	if _, keyErr = e.store.(state.ObjectBucketAccessStore).SetObjectBucketAccessGrant(t.Context(), e.account.ID, bucket.ID, key.ID, state.ObjectBucketPermissionWrite); keyErr != nil {
		t.Fatal(keyErr)
	}
	if r := e.do(t, http.MethodPut, path, request, headers); r.Code != 200 {
		t.Fatal("write grant", r.Code, r.Body.String())
	}
	request.ExpectedRevision = &revision
	if response = e.do(t, http.MethodPut, path, request, nil); response.Code != 409 {
		t.Fatal("stale", response.Code, response.Body.String())
	}
	if err := (*e.server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	revision = policy.Revision
	if response = e.do(t, http.MethodPut, path, request, nil); response.Code != 503 {
		t.Fatal("disabled configure", response.Code)
	}
	request.Triggers = []api.ObjectStorageComputeTrigger{}
	if response = e.do(t, http.MethodPut, path, request, nil); response.Code != 200 {
		t.Fatal("disabled clear", response.Code, response.Body.String())
	}
	if p, err := e.store.(state.ObjectNotificationStore).GetObjectBucketNotifications(t.Context(), e.account.ID, app.ID, bucket.ID); err != nil || len(p.Rules) != 0 {
		t.Fatal(p, err)
	}
}
