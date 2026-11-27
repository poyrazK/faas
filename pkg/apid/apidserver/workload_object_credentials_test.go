package apidserver_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// A service granted a sibling's bucket in gregale.yaml trades its
// gregale:bindings token for a one-hour, prefix-scoped S3 credential the
// gateway resolves like a compute key (ADR-20261122191423 slice 2).
func TestWorkloadObjectCredentialsFollowBucketGrants(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.Setup(t, api.PlanPro)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()

	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "assetforge"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, workload string, uses *capgrant.Uses) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: slug, WorkloadName: workload,
			Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Uses: uses}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	create("assetforge-worker", "worker", nil)
	web := create("assetforge-web", "web", &capgrant.Uses{
		Buckets: []capgrant.BucketGrant{{Bucket: "assets", App: "worker", Prefix: "rooms/", Access: "read"}},
	})
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/assetforge-worker/buckets", map[string]any{"name": "assets"}, nil), 201)

	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: web.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstanceWithMode(ctx, web.ID, dep.ID, string(state.StateRunning), 256, "default-local", "", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "credentials-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).WorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	token, err := signer.MintEnvironment(time.Now(), acct.ID, web.ID, instance.ID, "", workloadidentity.BindingsAudience)
	if err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + token.AccessToken}
	issue := func(bucket string) (int, api.ObjectWorkloadCredentialResponse, api.Problem) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/workload/object-storage/credentials", api.ObjectWorkloadCredentialRequest{Bucket: bucket}, auth)
		var resp api.ObjectWorkloadCredentialResponse
		var problem api.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		_ = json.Unmarshal(rec.Body.Bytes(), &problem)
		return rec.Code, resp, problem
	}

	if code, _, _ := issue(""); code != http.StatusNotFound {
		t.Fatalf("flag off: %d", code)
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.CrossAppBindings)

	code, resp, problem := issue("")
	if code != http.StatusOK || !strings.HasPrefix(resp.AccessKeyID, state.ObjectWorkloadCredentialAccessKeyPrefix) || resp.SecretAccessKey == "" ||
		resp.Bucket != "assets" || resp.KeyPrefix != "rooms/" || time.Until(resp.Expiration) > time.Hour || time.Until(resp.Expiration) < 59*time.Minute {
		t.Fatalf("issue: %d %+v %+v", code, resp, problem)
	}
	credential, resolved, err := store.ResolveObjectS3Credential(ctx, resp.AccessKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != bucket.ID || credential.Permission != "read" || credential.KeyPrefix != "rooms/" || credential.ManagedAppID != web.ID {
		t.Fatalf("resolved credential = %+v on bucket %s", credential, resolved.ID)
	}

	if code, _, problem := issue("thumbnails"); code != http.StatusForbidden || problem.Code != apidserver.CodeBindingNotGranted {
		t.Fatalf("ungranted bucket: %d %+v", code, problem)
	}

	// Expired credentials stop resolving and the sweep removes them.
	(*e.S()).SweepWorkloadCredentials(time.Now().Add(2 * time.Hour))
	if _, _, err := store.ResolveObjectS3Credential(ctx, resp.AccessKeyID); err == nil {
		t.Fatal("swept credential still resolves")
	}

	// Revocation does not wait for expiry.
	resolves := func(accessKeyID string) bool {
		t.Helper()
		_, _, err := store.ResolveObjectS3Credential(ctx, accessKeyID)
		return err == nil
	}
	fresh := func() string {
		t.Helper()
		code, resp, problem := issue("assets")
		if code != http.StatusOK {
			t.Fatalf("issue: %d %+v", code, problem)
		}
		return resp.AccessKeyID
	}
	// An apply that leaves the grant alone keeps the credential; one that
	// narrows its prefix or access rescopes the same key, so a guest's cached
	// copy keeps working within the new grant; one that drops it revokes it.
	kept := fresh()
	(*e.S()).ReconcileWorkloadCredentials([]state.App{web})
	if !resolves(kept) {
		t.Fatal("unchanged grant revoked")
	}
	narrowed := web
	narrowed.Manifest.Uses = &capgrant.Uses{Buckets: []capgrant.BucketGrant{{Bucket: "assets", App: "worker", Prefix: "rooms/a/", Access: "write"}}}
	(*e.S()).ReconcileWorkloadCredentials([]state.App{narrowed})
	if rescoped, _, err := store.ResolveObjectS3Credential(ctx, kept); err != nil || rescoped.KeyPrefix != "rooms/a/" || rescoped.Permission != "write" {
		t.Fatalf("narrowed grant: credential = %+v (%v), want the same key at rooms/a/ write", rescoped, err)
	}
	dropped := fresh()
	withoutGrant := web
	withoutGrant.Manifest.Uses = nil
	(*e.S()).ReconcileWorkloadCredentials([]state.App{withoutGrant})
	if resolves(dropped) {
		t.Fatal("credential outlived a dropped grant")
	}
	// A stopped instance's credential stops resolving.
	stopped := fresh()
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	if resolves(stopped) {
		t.Fatal("credential outlived its instance")
	}
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	// A deleted caller app's credential stops resolving.
	deleted := fresh()
	if _, err := store.SoftDeleteAppCascade(ctx, web.ID); err != nil {
		t.Fatal(err)
	}
	if resolves(deleted) {
		t.Fatal("credential outlived its app")
	}
}
