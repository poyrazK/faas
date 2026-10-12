package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type devBucketsFixture struct {
	server         *apidserver.Server
	store          state.Store
	account        state.Account
	do             func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder
	advanceCleanup func(string)
}

func TestDevBucketsMem(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	devBucketsSuite(t, devBucketsFixture{(*e.S()), (*e.Store()), (*e.Acct()), e.Do, func(string) { (*e.Store()).SetClockForTest(func() time.Time { return time.Now().Add(time.Minute) }) }})
}

func devBucketsSuite(t *testing.T, e devBucketsFixture) {
	t.Helper()
	if err := (*e.server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	provider := newStorageTestObjectProvider(map[string][]byte{}, nil)
	e.server.WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	const path = "/v1/dev/sessions/storage-app"
	request := api.UpsertDevSessionRequest{WorkspaceID: strings.Repeat("a", 32), Buckets: []api.DevBucketRequest{{Name: "assets"}}}
	decode := func(response *httptest.ResponseRecorder, code int) api.DevSessionResponse {
		t.Helper()
		if response.Code != code {
			t.Fatalf("response %d: %s", response.Code, response.Body.String())
		}
		var session api.DevSessionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(response.Body.String(), "ciphertext") || strings.Contains(response.Body.String(), "physical_name") {
			t.Fatal("private data leaked")
		}
		return session
	}
	first := decode(e.do(t, http.MethodPut, path, request, nil), http.StatusCreated)
	if len(first.Buckets) != 1 || first.Buckets[0].Bucket.Public || first.Buckets[0].Binding.Prefix != "GREGALE_S3_ASSETS" {
		t.Fatalf("receipt = %+v", first)
	}
	bucket := first.Buckets[0]
	// Refresh and an omitted declaration retain the exact bucket and credentials.
	second := decode(e.do(t, http.MethodPut, path, request, nil), http.StatusOK)
	if second.Buckets[0].Bucket.ID != bucket.Bucket.ID || second.Buckets[0].Binding.ID != bucket.Binding.ID || len(*provider.Created()) != 1 {
		t.Fatal("refresh replaced storage")
	}
	omitted := request
	omitted.Buckets = nil
	unchanged := decode(e.do(t, http.MethodPut, path, omitted, nil), http.StatusOK)
	if len(unchanged.Buckets) != 1 {
		t.Fatal("omission detached bucket")
	}
	read := decode(e.do(t, http.MethodGet, path+"?workspace_id="+request.WorkspaceID, nil, nil), http.StatusOK)
	if !read.ExpiresAt.Equal(unchanged.ExpiresAt) || len(*provider.Created()) != 1 {
		t.Fatal("inspection renewed or provisioned")
	}
	// Same account and project, independent source workspace: separate resources.
	otherRequest := request
	otherRequest.WorkspaceID = strings.Repeat("b", 32)
	other := decode(e.do(t, http.MethodPut, path, otherRequest, nil), http.StatusCreated)
	if other.App.ID == first.App.ID || other.Buckets[0].Bucket.ID == bucket.Bucket.ID || other.Buckets[0].Binding.ID == bucket.Binding.ID {
		t.Fatal("workspace resources shared")
	}
	credentials := e.store.(state.ObjectS3CredentialBindingStore)
	if _, err := credentials.GetObjectS3Credential(t.Context(), e.account.ID, other.Buckets[0].Bucket.ID, bucket.Binding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-bucket credential = %v", err)
	}
	// A fresh server object recovers ownership from the existing durable rows.
	app, err := e.store.AppByID(t.Context(), first.App.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh := apidserver.NewServer(e.store, apidserver.TestLogger(), "gregale.dev", apidserver.NoopNotifier{})
	fresh.WithObjectStorage((*e.server.ObjectStorage()))
	if err := (*fresh.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	normalized, err := api.NormalizeDevBuckets(request.Buckets)
	if err != nil {
		t.Fatal(err)
	}
	if prob, err := (*fresh.Object()).EnsureDevBuckets(t.Context(), e.account, app, normalized); prob != nil || err != nil {
		t.Fatalf("restart = %v %v", prob, err)
	}
	if len(*provider.Created()) != 2 {
		t.Fatal("restart reprovisioned")
	}
	// Permission changes and revocation fail closed without replacing rights.
	changed := request
	changed.Buckets = []api.DevBucketRequest{{Name: "assets", Permission: "read"}}
	if response := e.do(t, http.MethodPut, path, changed, nil); response.Code != http.StatusConflict {
		t.Fatalf("permission drift = %d", response.Code)
	}
	if _, err := credentials.RevokeObjectS3ComputeBinding(t.Context(), e.account.ID, bucket.Bucket.ID, bucket.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if response := e.do(t, http.MethodPut, path, request, nil); response.Code != http.StatusConflict {
		t.Fatalf("revoked retry = %d: %s", response.Code, response.Body.String())
	}
	rows, err := e.store.ListAppSecretsInScope(t.Context(), e.account.ID, app.ID, api.DefaultEnvScope)
	if err != nil || len(rows) != 0 {
		t.Fatalf("revoked secrets recreated: %d %v", len(rows), err)
	}
	// Failed cleanup retains the session and journal, then an ordinary retry finishes.
	*provider.DeleteErr() = errors.New("provider temporarily offline")
	if response := e.do(t, http.MethodDelete, path+"?workspace_id="+request.WorkspaceID, nil, nil); response.Code < 400 {
		t.Fatalf("failed cleanup = %d", response.Code)
	}
	app, err = e.store.AppByID(t.Context(), first.App.ID)
	if err != nil || app.Status == state.AppDeleted {
		t.Fatal("session lost before cleanup")
	}
	*provider.DeleteErr() = nil
	e.advanceCleanup(bucket.Bucket.ID)
	if response := e.do(t, http.MethodDelete, path+"?workspace_id="+request.WorkspaceID, nil, nil); response.Code != http.StatusNoContent {
		t.Fatalf("cleanup retry = %d: %s", response.Code, response.Body.String())
	}
	// Expiry uses the same worker and revokes the remaining workspace credentials.
	janitor := apidserver.NewPreviewJanitor(e.store, apidserver.NoopNotifier{}, nil, apidserver.TestLogger(), true).WithClock(func() time.Time { return time.Now().Add(25 * time.Hour) }).WithResourceCleanup(fresh.CleanupDevSessionResources)
	if err := janitor.SweepOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	remaining, err := e.store.(state.ObjectBucketStore).ListObjectBuckets(t.Context(), e.account.ID, other.App.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("expired buckets = %v %v", remaining, err)
	}
	if _, _, err := credentials.ResolveObjectS3Credential(t.Context(), other.Buckets[0].Binding.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired credential still resolves: %v", err)
	}
}

func TestDevBucketsRejectBeforeCreatingSession(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	for _, tc := range []struct {
		name    string
		buckets []api.DevBucketRequest
		code    int
	}{
		{"disabled", []api.DevBucketRequest{{Name: "assets"}}, http.StatusServiceUnavailable},
		{"duplicate", []api.DevBucketRequest{{Name: "assets"}, {Name: "assets"}}, http.StatusBadRequest},
		{"invalid", []api.DevBucketRequest{{Name: "../assets"}}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := e.Do(t, http.MethodPut, "/v1/dev/sessions/reject-storage", api.UpsertDevSessionRequest{Buckets: tc.buckets}, nil)
			if response.Code != tc.code {
				t.Fatalf("response = %d: %s", response.Code, response.Body.String())
			}
			if _, err := (*e.Store()).AppBySlug(t.Context(), apidserver.DevSessionSlug((*e.Acct()).ID, "reject-storage", "")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("created rejected app: %v", err)
			}
		})
	}
}

func TestDevBucketsScopedKeys(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	apidserver.SetS3Flag(t, e, true)
	provider := newStorageTestObjectProvider(map[string][]byte{}, nil)
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	key := func(scopes ...string) map[string]string {
		t.Helper()
		plain, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "developer scopes", scopes); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + plain}
	}
	path := "/v1/dev/sessions/scoped-storage"
	request := api.UpsertDevSessionRequest{Buckets: []api.DevBucketRequest{{Name: "assets"}}}
	deployOnly := key(api.ScopeDeployWrite)
	for _, headers := range []map[string]string{deployOnly, key(api.ScopeStorageManage)} {
		if response := e.Do(t, http.MethodPut, path, request, headers); response.Code != http.StatusForbidden {
			t.Fatalf("scope bypass = %d: %s", response.Code, response.Body.String())
		}
	}
	if len(*provider.Created()) != 0 {
		t.Fatal("unauthorized provisioning")
	}
	if _, err := (*e.Store()).AppBySlug(t.Context(), apidserver.DevSessionSlug((*e.Acct()).ID, "scoped-storage", "")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unauthorized app creation: %v", err)
	}
	response := e.Do(t, http.MethodPut, path, request, key(api.ScopeDeployWrite, api.ScopeStorageManage))
	var created api.DevSessionResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || len(created.Buckets) != 1 {
		t.Fatalf("authorized provisioning = %d: %s", response.Code, response.Body.String())
	}
	for _, check := range []struct {
		method  string
		headers map[string]string
		body    any
	}{
		{http.MethodPut, deployOnly, api.UpsertDevSessionRequest{}},
		{http.MethodGet, key(api.ScopeAppsRead), nil},
	} {
		response := e.Do(t, check.method, path, check.body, check.headers)
		var receipt api.DevSessionResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || len(receipt.Buckets) != 0 {
			t.Fatalf("storage metadata disclosure = %d: %s", response.Code, response.Body.String())
		}
	}
	if len(*provider.Created()) != 1 {
		t.Fatal("ordinary refresh recreated storage")
	}
}

func TestDevBucketsPartialFailureAndSecretCustody(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	apidserver.SetS3Flag(t, e, true)
	provider := newStorageTestObjectProvider(map[string][]byte{}, errors.New("temporary provider failure"))
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	const path = "/v1/dev/sessions/retry-storage"
	request := api.UpsertDevSessionRequest{Buckets: []api.DevBucketRequest{{Name: "assets"}}}
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 503 {
		t.Fatalf("provider failure = %d: %s", response.Code, response.Body.String())
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), apidserver.DevSessionSlug((*e.Acct()).ID, "retry-storage", ""))
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := (*e.Store()).ListObjectBuckets(t.Context(), (*e.Acct()).ID, app.ID)
	if err != nil || len(buckets) != 1 || buckets[0].State != "provisioning" || buckets[0].LastErrorCode != "temporary" {
		t.Fatalf("reservation = %+v %v", buckets, err)
	}
	*provider.CreateErr() = nil
	(*e.Store()).SetClockForTest(func() time.Time { return time.Now().Add(time.Minute) })
	response := e.Do(t, http.MethodPut, path, request, nil)
	var session api.DevSessionResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &session) != nil || len(session.Buckets) != 1 || session.Buckets[0].Bucket.ID != buckets[0].ID {
		t.Fatalf("retry = %d: %s", response.Code, response.Body.String())
	}
	// The current default cannot relocate a bucket on a later source reload.
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "ceph"))
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 200 {
		t.Fatalf("placement retry = %d: %s", response.Code, response.Body.String())
	}
	placed, err := (*e.Store()).GetObjectBucket(t.Context(), (*e.Acct()).ID, app.ID, buckets[0].ID)
	if err != nil || placed.BackendID != "external" {
		t.Fatalf("placement changed = %+v %v", placed, err)
	}
	key := "GREGALE_S3_ARCHIVE_ENDPOINT"
	secretPath := "/v1/apps/" + app.Slug + "/secrets/" + key
	if response := e.Do(t, http.MethodPut, secretPath, api.PutAppSecretRequest{Value: "customer-owned"}, nil); response.Code != 200 {
		t.Fatalf("secret = %d: %s", response.Code, response.Body.String())
	}
	request.Buckets = []api.DevBucketRequest{{Name: "archive"}}
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 409 {
		t.Fatalf("secret collision = %d: %s", response.Code, response.Body.String())
	}
	rows, err := (*e.Store()).ListAppSecretsInScope(t.Context(), (*e.Acct()).ID, app.ID, api.DefaultEnvScope)
	if err != nil || len(rows) != 7 {
		t.Fatalf("collision changed secret rows: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if row.Key == key && row.ManagedObjectStorageCredentialID != "" {
			t.Fatal("customer secret adopted by binding")
		}
	}
	if response := e.Do(t, http.MethodDelete, secretPath, nil, nil); response.Code != 204 {
		t.Fatalf("explicit repair = %d", response.Code)
	}
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 200 {
		t.Fatalf("repaired retry = %d: %s", response.Code, response.Body.String())
	}
	// Explicit placement drift conflicts rather than relocating existing data.
	request.Buckets = []api.DevBucketRequest{{Name: "assets", Region: "eu-west-1"}}
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 409 {
		t.Fatalf("region drift = %d", response.Code)
	}
	// Preserve the operational failure class: validation does not bypass disablement.
	apidserver.SetS3Flag(t, e, false)
	if response := e.Do(t, http.MethodPut, path, request, nil); response.Code != 503 {
		t.Fatalf("disablement = %d", response.Code)
	}

}

// adr: 1036
func TestDevBucketComputeTriggersMem(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	devBucketComputeTriggersSuite(t, devBucketsFixture{server: (*e.S()), store: (*e.Store()), account: (*e.Acct()), do: e.Do})
}
func devBucketComputeTriggersSuite(t *testing.T, e devBucketsFixture) {
	t.Helper()
	if err := (*e.server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	e.server.WithObjectStorage(apidserver.ObjectRegistry(t, newStorageTestObjectProvider(map[string][]byte{}, nil), newStorageTestObjectProvider(nil, nil), "external"))
	triggers := []api.ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/"}}
	request := api.UpsertDevSessionRequest{WorkspaceID: strings.Repeat("c", 32), Buckets: []api.DevBucketRequest{{Name: "assets", Triggers: &triggers}}}
	do := func(r api.UpsertDevSessionRequest) api.DevSessionResponse {
		t.Helper()
		response := e.do(t, http.MethodPut, "/v1/dev/sessions/image-worker", r, nil)
		if response.Code != 200 && response.Code != 201 {
			t.Fatal(response.Code, response.Body.String())
		}
		var out api.DevSessionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := do(request)
	b := first.Buckets[0].Bucket
	s := e.store.(state.ObjectComputeTriggerStore)
	p, err := s.GetObjectBucketNotifications(t.Context(), e.account.ID, first.App.ID, b.ID)
	if err != nil || len(p.ManagedRuleIDs) != 1 || !strings.HasSuffix(p.Rules[0].Destination, ":function:"+uuid.MustParse(first.App.ID).String()) {
		t.Fatal(p, err)
	}
	second := do(request)
	if second.App.ID != first.App.ID {
		t.Fatal("workspace changed")
	}
	omitted := request
	omitted.Buckets = []api.DevBucketRequest{{Name: "assets"}}
	do(omitted)
	preserved, err := s.GetObjectBucketNotifications(t.Context(), e.account.ID, first.App.ID, b.ID)
	if err != nil || preserved.Revision != p.Revision {
		t.Fatal("omitted intent", preserved, err)
	}
	other := request
	other.WorkspaceID = strings.Repeat("d", 32)
	isolated := do(other)
	separate, err := s.GetObjectBucketNotifications(t.Context(), e.account.ID, isolated.App.ID, isolated.Buckets[0].Bucket.ID)
	if err != nil || separate.Rules[0].ID == p.Rules[0].ID || !strings.HasSuffix(separate.Rules[0].Destination, ":function:"+uuid.MustParse(isolated.App.ID).String()) {
		t.Fatal("cross-workspace trigger", separate, err)
	}
	triggers = []api.ObjectStorageComputeTrigger{}
	do(request)
	cleared, err := s.GetObjectBucketNotifications(t.Context(), e.account.ID, first.App.ID, b.ID)
	if err != nil || len(cleared.Rules) != 0 || cleared.Revision != p.Revision+1 {
		t.Fatal("explicit clear", cleared, err)
	}
}

func newStorageTestObjectProvider(objects map[string][]byte, createErr error) *apidserver.FakeObjectProvider {
	provider := &apidserver.FakeObjectProvider{}
	*provider.Objects() = objects
	*provider.CreateErr() = createErr
	return provider
}
