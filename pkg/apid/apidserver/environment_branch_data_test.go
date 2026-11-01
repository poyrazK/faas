package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func primedDeployment(notifier *captureNotifier, deploymentID string) bool {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	for _, n := range notifier.notif {
		if n.channel == db.NotifySnapshotPrime && strings.Contains(n.payload, deploymentID) {
			return true
		}
	}
	return false
}

// ADR-20261031193635 slice 2: a branch of a parent that binds managed
// PostgreSQL copies the database as of the branch point, binds the copy into
// the branch with new credentials, and only then activates its workloads.
// Deleting the branch deletes the copy and its binding.
func TestEnvironmentBranchCopiesParentDatabases(t *testing.T) {
	_, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	notifier := &captureNotifier{}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier)
	srv.runtimeConfig = newRuntimeConfigManager(nil)
	if err := srv.runtimeConfig.apply(runtimeConfigEnvironmentBranches, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	provider := &environmentClonePostgresProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "environment-branch"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{
			"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
				return provider, nil
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	databaseStore := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, databaseStore, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := newAppSecretCredentialSink(store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("0123456789abcdef0123456789abcdef") })
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, databaseStore, databaseStore, sink, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	source, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: acct.ID, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600,
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Production's values live in its live deployment's scope.
	if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: source.ID, AppID: app.ID,
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:orders"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/orders.ext4", "layers/orders.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // the branch point follows the source's creation

	req, rec := environmentBranchRequest(http.MethodPost, "production", `{"slug":"pr-9","ttl":"2h"}`)
	srv.createProjectEnvironmentBranch(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("branch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Branch == nil || response.Branch.Data != state.EnvironmentBranchDataPending || len(response.Branch.Workloads) != 1 {
		t.Fatalf("branch response = %s", rec.Body.String())
	}
	branchDeployment := response.Branch.Workloads[0].DeploymentID
	if primedDeployment(notifier, branchDeployment) {
		t.Fatal("a branch with pending data primed its workload")
	}
	if ready, err := store.EnvironmentBranchDataReady(ctx, branchDeployment); err != nil || ready {
		t.Fatalf("pending data ready = %v, %v", ready, err)
	}
	if secret, err := store.GetAppSecretInScope(ctx, acct.ID, app.ID, "pr-9", "DATABASE_URL"); err == nil {
		t.Fatalf("the parent's credential was copied into the branch: %+v", secret)
	}

	if err := srv.reconcileEnvironmentBranchData(ctx, store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-9")
	if err != nil || branch.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("branch after reconcile = %+v, %v", branch, err)
	}
	if len(provider.restores) != 1 || provider.restores[0].SourceResourceID != source.DataResourceID || !provider.restores[0].PointInTime.Equal(branch.BranchPoint) {
		t.Fatalf("restores = %+v; want one copy of %s at %s", provider.restores, source.DataResourceID, branch.BranchPoint)
	}
	secret, err := store.GetAppSecretInScope(ctx, acct.ID, app.ID, "pr-9", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := bindings.Get(ctx, acct.ID, secret.ManagedPostgresBindingID)
	if err != nil || binding.DatabaseID == source.ID || binding.Scope != "pr-9" || binding.AppID != app.ID || binding.State != managedpostgres.BindingStateReady {
		t.Fatalf("branch binding = %+v, %v", binding, err)
	}
	copies, err := store.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID)
	if err != nil || len(copies) != 1 || copies[0].CopyDatabaseID != binding.DatabaseID || copies[0].SourceDatabaseID != source.ID {
		t.Fatalf("recorded copies = %+v, %v", copies, err)
	}
	if ready, err := store.EnvironmentBranchDataReady(ctx, branchDeployment); err != nil || !ready {
		t.Fatalf("data ready = %v, %v", ready, err)
	}
	if !primedDeployment(notifier, branchDeployment) {
		t.Fatalf("ready data did not prime the branch's workload: %+v", notifier.notif)
	}
	if err := srv.reconcileEnvironmentBranchData(ctx, store, time.Now().UTC()); err != nil || len(provider.restores) != 1 {
		t.Fatalf("a ready branch was copied again: restores=%d err=%v", len(provider.restores), err)
	}
	if original, err := service.Get(ctx, acct.ID, source.ID); err != nil || original.State != managedpostgres.StateReady {
		t.Fatalf("source database = %+v, %v", original, err)
	}

	// Deleting the branch leaves its copy to the reconciler, which deletes it.
	req, rec = projectRequest(http.MethodDelete, "/v1/projects/shop/environments/pr-9", "shop", nil)
	req.SetPathValue("environment", "pr-9")
	srv.deleteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := srv.reconcileEnvironmentBranchData(ctx, store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	copied, err := service.Get(ctx, acct.ID, binding.DatabaseID)
	if err != nil || (copied.State != managedpostgres.StateDeleted && copied.State != managedpostgres.StateDeleting) {
		t.Fatalf("branch copy after delete = %+v, %v", copied, err)
	}
	if b, err := bindings.Get(ctx, acct.ID, binding.ID); err == nil && b.State == managedpostgres.BindingStateReady {
		t.Fatalf("branch binding survived delete: %+v", b)
	}
	if copies, err := store.ListProjectEnvironmentBranchDatabases(ctx, branch.EnvironmentID); err != nil || len(copies) != 0 {
		t.Fatalf("deleted copy still recorded: %+v, %v", copies, err)
	}
	if original, err := service.Get(ctx, acct.ID, source.ID); err != nil || original.State != managedpostgres.StateReady {
		t.Fatalf("source database after branch delete = %+v, %v", original, err)
	}
}
