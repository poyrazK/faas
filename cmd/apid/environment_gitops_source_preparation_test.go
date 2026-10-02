package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQualificationNotifier struct {
	Notifier
	mu       sync.Mutex
	payloads []string
}

func (n *environmentQualificationNotifier) Notify(ctx context.Context, channel, payload string) error {
	if channel == db.NotifyEnvironmentWorkloadQualify {
		n.mu.Lock()
		n.payloads = append(n.payloads, payload)
		n.mu.Unlock()
	}
	return n.Notifier.Notify(ctx, channel, payload)
}

func environmentGitOpsBuildArchive(t *testing.T, definition string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"shop-commit/environments/production.yaml": definition,
		"shop-commit/apps/api/package.json":        `{"name":"reviewed-api"}`,
		"shop-commit/apps/api/deploy/Dockerfile":   "FROM scratch\nCOPY . /app\n",
	} {
		if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestEnvironmentGitOpsHTTPSourceBuildCandidateUsesApprovedArchive(t *testing.T) {
	t.Setenv(sourceSpoolRootEnv, t.TempDir())
	t.Setenv(scanSpoolRootEnv, t.TempDir())
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	qualificationNotifier := &environmentQualificationNotifier{Notifier: srv.notif}
	srv.notif = qualificationNotifier
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	definition := "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    source:\n      kind: dockerfile\n      directory: apps/api\n      dockerfile: deploy/Dockerfile\n    runtime:\n      port: 8080\n"
	github := &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, archive: environmentGitOpsBuildArchive(t, definition), resolvedSHA: sha}
	srv.githubd = github
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "gitops source test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.handler())
	defer httpServer.Close()
	client := api.NewClient(httpServer.URL, key)
	if _, err := client.CreateEnvironmentGitSource(t.Context(), "shop", "production", api.CreateEnvironmentGitSourceRequest{ManifestPath: "environments/production.yaml", Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	review, err := client.PreviewEnvironmentGitRevision(t.Context(), "shop", "production", api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApproveEnvironmentGitRevision(t.Context(), "shop", "production", api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation}); err != nil {
		t.Fatal(err)
	}
	plan, err := client.PreviewEnvironmentGitOpsAdoption(t.Context(), "shop", "production")
	if err != nil || !plan.CanApply() {
		t.Fatalf("adoption: %+v %v", plan, err)
	}
	if _, err := client.AdoptEnvironmentGitOps(t.Context(), "shop", "production", api.AdoptEnvironmentGitOpsRequest{PlanHash: plan.Hash}); err != nil {
		t.Fatal(err)
	}
	// A different archive returned for the same SHA is rejected before queue
	// publication. Approval itself was read from the original exact bytes.
	github.archive = environmentGitOpsBuildArchive(t, strings.ReplaceAll(definition, "port: 8080", "port: 9090"))
	worker := gitOpsBackendWorker(srv, store)
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	failed, err := client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || len(failed.Runs) != 1 || failed.Runs[0].Status != "partial" || failed.Runs[0].ErrorCode != "environment_runtime_verification_failed" {
		t.Fatalf("worker accepted a substituted reviewed archive: %+v %v", failed, err)
	}
	deployments, err := store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("substituted archive created work: %+v %v", deployments, err)
	}
	// Use an explicitly issued fresh lease to retry immediately after the
	// worker's durable backoff, preserving the same approved revision.
	github.archive = environmentGitOpsBuildArchive(t, definition)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "source-recovery", time.Now().Add(time.Minute), worker.LeaseDuration)
	if err != nil {
		t.Fatal(err)
	}
	var desiredDefinition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &desiredDefinition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(desiredDefinition)
	if err != nil {
		t.Fatal(err)
	}
	backend := &environmentGitOpsBackend{server: srv, intent: store, effects: store}
	observed, err := backend.Observe(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: sha, Generation: lease.Source.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || ready {
		t.Fatalf("source preparation claimed serving: %v %v", ready, err)
	}
	deployments, err = store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("source candidate: %+v %v", deployments, err)
	}
	dep := deployments[0]
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen.Source.Kind != "dockerfile" || frozen.Source.Dockerfile != "deploy/Dockerfile" || dep.SourceRoot != "apps/api" || dep.CommitSHA != sha || dep.SourceURL != "github://example/shop@"+sha || dep.BuildID == "" {
		t.Fatalf("frozen source: %+v %v", dep, err)
	}
	if _, err := os.Stat(dep.SourcePath); err != nil {
		t.Fatal(err)
	}
	fetches := github.fetches
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || ready || github.fetches != fetches {
		t.Fatalf("retry fetched a second archive or qualified the candidate: %v %v fetches=%d", ready, err, github.fetches)
	}
	status, err := client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || status.Source.AppliedRevisionID != "" {
		t.Fatalf("unqualified build advanced applied revision: %+v %v", status, err)
	}
	// Completing the artifact hands the exact reviewed graph to its runtime
	// owner. Publication and even a claim are still not serving proof.
	if err := store.SetDeploymentRootfs(t.Context(), dep.ID, "/reviewed-source.ext4", "reviewed-source", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || ready {
		t.Fatalf("prepared source claimed serving: %v %v", ready, err)
	}
	qualificationNotifier.mu.Lock()
	payloads := append([]string(nil), qualificationNotifier.payloads...)
	qualificationNotifier.mu.Unlock()
	if len(payloads) != 1 {
		t.Fatalf("prepared source qualification handoff count: %d", len(payloads))
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(payloads[0]), &payload); err != nil || len(payload) != 4 || payload["qualification_id"] == "" || payload["graph_id"] == "" || payload["app_id"] != app.ID || payload["deployment_id"] != dep.ID {
		t.Fatalf("qualification handoff identities: %s %v", payloads[0], err)
	}
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || ready || github.fetches != fetches {
		t.Fatalf("qualification retry changed preparation: %v %v", ready, err)
	}
	qualificationNotifier.mu.Lock()
	retryPayload := qualificationNotifier.payloads[len(qualificationNotifier.payloads)-1]
	qualificationNotifier.mu.Unlock()
	if retryPayload != payloads[0] {
		t.Fatal("retry changed the durable qualification identity")
	}
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), payload["qualification_id"], "scheduler", time.Minute)
	if err != nil || claimed.DeploymentID != dep.ID || claimed.ReservedInstanceID == "" {
		t.Fatalf("claim prepared source qualification: %+v %v", claimed, err)
	}
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || ready {
		t.Fatalf("claimed source qualification became serving proof: %v %v", ready, err)
	}
	status, err = client.GetEnvironmentGitOps(t.Context(), "shop", "production")
	if err != nil || status.Source.AppliedRevisionID != "" {
		t.Fatalf("qualification claim advanced applied revision: %+v %v", status, err)
	}
}
