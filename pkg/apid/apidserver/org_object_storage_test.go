package apidserver_test

// adr: 202610171907

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgObjectStorageMemHTTPAndS3(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	orgObjectStorageJourney(t, *e.S(), *e.Store(), *e.Acct(), *e.Key())
}

func orgObjectStorageJourney(t *testing.T, server *apidserver.Server, store state.Store, creator state.Account, personalBearer string) {
	ctx := t.Context()
	identity, teardown := apidserver.WithTestIdentities(t)
	defer teardown()
	if err := store.MarkAccountEmailVerified(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "object-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_object_journey"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_object_journey"); err != nil {
		t.Fatal(err)
	}
	admin, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	developer, developerHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billingHeaders := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	credentialHeaders := func(actor state.Account, scopes []string) map[string]string {
		t.Helper()
		bearer, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateOrgAPIKey(ctx, org.ID, actor.ID, hash, "storage journey", scopes, nil); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + bearer}
	}
	creatorHeaders := credentialHeaders(creator, api.ScopesAdminOnly)
	readHeaders := credentialHeaders(admin, []string{api.ScopeStorageRead})
	personalOrg, err := store.OrgByPersonalAccount(ctx, creator.ID)
	if errors.Is(err, state.ErrNotFound) {
		personalOrg, err = store.CreateOrg(ctx, state.Org{Slug: "object-personal-owner", Name: "Personal", Personal: true, PersonalOwnerAccountID: &creator.ID, Plan: creator.Plan})
		if err == nil {
			err = store.AddOrgMember(ctx, personalOrg.ID, creator.ID, state.OrgRoleOwner, nil)
		}
	}
	if err != nil {
		t.Fatal("personal organization fixture:", err)
	}
	personalKey, personalHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(ctx, personalOrg.ID, creator.ID, personalHash, "personal storage", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "company-object-app"})
	if err != nil {
		t.Fatal(err)
	}
	native := &signedURLNative{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Count(strings.Trim(r.URL.Path, "/"), "/") == 0 && r.URL.Query().Has("versioning") {
			_, _ = io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`)
			return
		}
		if r.URL.Query().Get("list-type") == "2" {
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>file</Key><Size>3</Size><LastModified>2026-10-17T12:00:00Z</LastModified></Contents></ListBucketResult>`)
			return
		}
		native.serve(w, r)
	}))
	defer upstream.Close()
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100,
		MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1",
		Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1",
			Namespace: "company", Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true,
			AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET"}}}, func(string) string { return "fixture-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	server.WithObjectStorage(registry)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	buckets := store.(state.ObjectBucketStore)
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: creator.ID, AppID: app.ID,
		Name: "assets", Scope: "default", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "company-physical", Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buckets.ClaimObjectBucket(ctx, creator.ID, app.ID, bucket.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := buckets.FinishObjectBucket(ctx, bucket.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	accounting := store.(state.ObjectStorageAccountingStore)
	if err := accounting.ClaimObjectInventory(ctx, bucket.ID, "baseline"); err != nil {
		t.Fatal(err)
	}
	if err := accounting.FinishObjectInventory(ctx, bucket.ID, "baseline", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := accounting.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: org.ID, BackendID: backend.ID,
		BackendFingerprint: backend.Fingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: apidserver.RuntimeConfigS3, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: apidserver.BoolJSON(true), ApplyMode: state.RuntimeConfigApplyHot, Reason: "company object journey"}); err != nil {
		t.Fatal(err)
	}
	if err := (*server.RuntimeConfig()).Reconcile(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := (*server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, apidserver.BoolJSON(true)); err != nil {
		t.Fatal(err)
	}
	public := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer public.Close()
	registry.PublicEndpoint = public.URL
	control := server.Handler()
	send := func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		control.ServeHTTP(w, r)
		return w
	}
	base := "/v1/orgs/" + org.Slug + "/apps/" + app.Slug + "/buckets"
	path := base + "/" + bucket.ID
	bindingRequest := api.CreateObjectStorageComputeBindingRequest{Permission: state.ObjectBucketPermissionReadWrite}
	orgKeys, err := store.ListOrgAPIKeys(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range orgKeys {
		if len(key.Scopes) == 1 && key.Scopes[0] == api.ScopeStorageRead {
			grant := send("PUT", path+"/access-grants/"+key.ID, api.SetObjectBucketAccessGrantRequest{Permission: "read"}, adminHeaders)
			if grant.Code != 200 {
				t.Fatalf("teammate key grant across retained namespace: %d %s", grant.Code, grant.Body)
			}
		}
	}
	for _, tc := range []struct {
		method, path string
		body         any
		headers      map[string]string
		want         int
	}{
		{"GET", base, nil, viewerHeaders, 200},
		{"GET", path + "/objects", nil, viewerHeaders, 200},
		{"GET", path + "/objects", nil, readHeaders, 200},
		{"GET", base, nil, billingHeaders, 403},
		{"GET", "/v1/orgs/" + org.Slug + "/object-storage-usage", nil, billingHeaders, 200},
		{"POST", base, nil, developerHeaders, 403},
		{"DELETE", path, nil, developerHeaders, 403},
		{"POST", path + "/s3-credentials", nil, developerHeaders, 403},
		{"POST", path + "/compute-bindings", bindingRequest, viewerHeaders, 403},
		{"POST", path + "/compute-bindings", bindingRequest, readHeaders, 403},
		{"GET", strings.Replace(base, org.Slug, "another-company", 1), nil, adminHeaders, 404},
		{"GET", base, nil, map[string]string{"Authorization": "Bearer " + personalKey}, 404},
		{"GET", "/v1/account/object-storage-usage", nil, adminHeaders, 404},
		{"GET", "/v1/apps/" + app.Slug + "/buckets/" + bucket.ID + "/encryption", nil, creatorHeaders, 403},
		{"POST", "/v1/apps/" + app.Slug + "/buckets/" + bucket.ID + "/signed-url", nil, creatorHeaders, 403},
	} {
		if rec := send(tc.method, tc.path, tc.body, tc.headers); rec.Code != tc.want {
			t.Fatalf("%s %s: %d %s; want %d", tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
	}
	created := send("POST", path+"/compute-bindings", bindingRequest, developerHeaders)
	var binding api.ObjectStorageComputeBinding
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &binding) != nil {
		t.Fatalf("create company binding: %d %s", created.Code, created.Body)
	}
	credentials := store.(state.ObjectS3CredentialBindingStore)
	managed, err := credentials.GetObjectS3Credential(ctx, creator.ID, bucket.ID, binding.ID)
	if err != nil || managed.IssuerAccountID != "" {
		t.Fatalf("managed binding authority: %+v %v", managed, err)
	}
	beforeSecret, err := store.GetAppSecretInScope(ctx, creator.ID, app.ID, "default", binding.SecretKeys.SecretAccessKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	user := send("POST", path+"/s3-credentials", api.CreateObjectS3CredentialRequest{Label: "creator key", Permission: "read_write"}, creatorHeaders)
	var userKey api.ObjectS3CredentialSecret
	if user.Code != 201 || json.Unmarshal(user.Body.Bytes(), &userKey) != nil {
		t.Fatalf("create user credential: %d %s", user.Code, user.Body)
	}
	open := func(blob []byte) (string, error) {
		namespace, plain, err := secretbox.OpenBytes(identity, blob)
		if err != nil || namespace != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid credential envelope")
		}
		return string(plain), nil
	}
	gateway, err := s3gateway.New(s3gateway.Config{Registry: registry, Host: "s3.gregale.dev", Store: store.(s3gateway.Store), RequestMetrics: store.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: open})
	if err != nil {
		t.Fatal(err)
	}
	managedSecret, err := open(managed.SecretSealed)
	if err != nil {
		t.Fatal(err)
	}
	signed := func(method, access, secret string) *http.Request {
		t.Helper()
		r := httptest.NewRequest(method, "https://s3.gregale.dev/assets/file", strings.NewReader("abc"))
		if method != "PUT" {
			r.Body, r.ContentLength = nil, 0
		}
		r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		if err := awsv4.NewSigner().SignHTTP(ctx, aws.Credentials{AccessKeyID: access, SecretAccessKey: secret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now()); err != nil {
			t.Fatal(err)
		}
		return r
	}
	assertS3 := func(r *http.Request, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("S3 %s: %d %s; want %d", r.Method, w.Code, w.Body, want)
		}
		return w
	}
	assertS3(signed("PUT", managed.AccessKeyID, managedSecret), 200)
	assertS3(signed("GET", userKey.AccessKeyID, userKey.SecretAccessKey), 200)
	beforeRemoval := signed("GET", userKey.AccessKeyID, userKey.SecretAccessKey)
	encoded, err := json.Marshal(map[string]string{"bucket_id": bucket.ID, "marker": "company-object-private-audit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "object_storage.legacy", &creator.ID, encoded); err != nil {
		t.Fatal(err)
	}
	exported := send("GET", "/v1/account/export", nil, map[string]string{"Authorization": "Bearer " + personalBearer})
	if exported.Code != 200 || strings.Contains(exported.Body.String(), "company-object-private-audit") || strings.Contains(exported.Body.String(), bucket.ID) {
		t.Fatalf("personal export included company storage: %d", exported.Code)
	}
	// A request authenticated before offboarding must recheck its authority
	// before admitting the provider write, even after its body has arrived.
	pausedWrite := signed("PUT", userKey.AccessKeyID, userKey.SecretAccessKey)
	removedDuringBody := false
	pausedWrite.Body = io.NopCloser(&signedURLBody{reader: strings.NewReader("abc"), before: func() {
		if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
			t.Fatal(err)
		}
		removedDuringBody = true
	}})
	assertS3(pausedWrite, 403)
	if !removedDuringBody {
		t.Fatal("request did not authenticate before the body-time offboarding")
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	assertS3(beforeRemoval, 403)
	if got := assertS3(signed("GET", managed.AccessKeyID, managedSecret), 200); got.Body.String() != "abc" {
		t.Fatal("company object changed after creator erasure")
	}
	assertS3(signed("PUT", managed.AccessKeyID, managedSecret), 200)
	remaining := send("POST", path+"/s3-credentials", api.CreateObjectS3CredentialRequest{Label: "teammate key", Permission: "read_write"}, adminHeaders)
	var teammateKey api.ObjectS3CredentialSecret
	if remaining.Code != 201 || json.Unmarshal(remaining.Body.Bytes(), &teammateKey) != nil {
		t.Fatalf("teammate credential after erasure: %d %s", remaining.Code, remaining.Body)
	}
	assertS3(signed("GET", teammateKey.AccessKeyID, teammateKey.SecretAccessKey), 200)
	afterSecret, err := store.GetAppSecretInScope(ctx, creator.ID, app.ID, "default", binding.SecretKeys.SecretAccessKey)
	if err != nil || !bytes.Equal(beforeSecret.Ciphertext, afterSecret.Ciphertext) || afterSecret.ManagedObjectStorageCredentialID != binding.ID {
		t.Fatal("creator erasure changed sealed application binding", err)
	}
	afterBucket, err := buckets.GetObjectBucket(ctx, creator.ID, app.ID, bucket.ID)
	if err != nil || afterBucket.PhysicalName != bucket.PhysicalName || afterBucket.BackendFingerprint != bucket.BackendFingerprint || afterBucket.OrgID != org.ID {
		t.Fatal("creator erasure changed bucket identity", err)
	}
	if rec := send("POST", "/v1/apps/"+app.Slug+"/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("c", 64)}, developerHeaders); rec.Code != 202 {
		t.Fatalf("deploy retained binding: %d %s", rec.Code, rec.Body)
	}
	if rec := send("DELETE", "/v1/orgs/"+org.Slug, nil, adminHeaders); rec.Code != 409 {
		t.Fatalf("organization deletion orphaned bucket: %d %s", rec.Code, rec.Body)
	}
	if rec := send("POST", path+"/compute-bindings/"+binding.ID+"/rotate", nil, adminHeaders); rec.Code != 200 {
		t.Fatalf("rotate retained binding: %d %s", rec.Code, rec.Body)
	}
	if err := store.UpdateOrgMemberRole(ctx, org.ID, developer.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if rec := send("POST", path+"/compute-bindings", bindingRequest, developerHeaders); rec.Code != 403 {
		t.Fatalf("binding retry bypassed demotion: %d %s", rec.Code, rec.Body)
	}
}
