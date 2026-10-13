package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// This opt-in journey uses the real apid subprocess and PostgreSQL. It proves
// the acceptance-only enforce worker repairs reviewed intent and keeps an
// unqualified workload from being reported as serving. Native VM and gateway
// qualification are a separate acceptance gate.
func TestEnvironmentGitOpsAcceptanceE2E(t *testing.T) {
	if os.Getenv("GREGALE_GITOPS_ACCEPTANCE") != "1" {
		t.Skip("isolated GitOps acceptance only")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	h := e2etest.Start(t, pool, e2etest.APID,
		"GREGALE_GITOPS_ACCEPTANCE=1", "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT=1",
		"FAAS_AUDIT_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "audit-hmac.key"),
		"FAAS_RECOVERY_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "recovery-hmac.key"))
	key := h.SeedAccount(t.Context(), api.PlanPro, "gitops-acceptance")
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(t.Context(), "e2e+pro+gitops-acceptance@test.example")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "gitops-acceptance",
		RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/env/MODE?scope=production"
	if body, status := doReq(t, h, key, http.MethodPut, path, api.PutAppEnvRequest{Value: "console"}); status != http.StatusOK {
		t.Fatalf("console write: %d %s", status, body)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
		ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: app.Slug, Variables: map[string]string{"MODE": "reviewed"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{
		AccountID: account.ID, SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired,
		ApprovedBy: "acceptance-owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption preview: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	if body, status := doReq(t, h, key, http.MethodPut, path, api.PutAppEnvRequest{Value: "second-console-edit"}); status != http.StatusConflict {
		t.Fatalf("Git-owned HTTP write was not fenced: %d %s", status, body)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		values, err := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), account.ID, source.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(values) == 1 && values[0].Value == "reviewed" && len(runs) == 1 && runs[0].CompletedAt != nil {
			if runs[0].Status != "partial" || runs[0].RevisionID != source.ApprovedRevisionID {
				t.Fatalf("unqualified workload run: %+v", runs[0])
			}
			current, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
			if err != nil || current.AppliedRevisionID != "" {
				t.Fatalf("unqualified workload claimed serving: %+v %v", current, err)
			}
			body, status := doReq(t, h, key, http.MethodGet,
				"/v1/projects/"+project.Slug+"/environments/production/gitops", nil)
			var visible api.EnvironmentGitOpsStatusResponse
			if status != http.StatusOK || json.Unmarshal(body, &visible) != nil ||
				visible.Source.AppliedRevisionID != "" || len(visible.Runs) == 0 || visible.Runs[0].Status != "partial" {
				t.Fatalf("HTTP status claimed an unqualified workload: %d %s", status, body)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	values, _ := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "production")
	runs, _ := store.ListEnvironmentGitOpsRuns(t.Context(), account.ID, source.ID, 1)
	status, _ := json.Marshal(runs)
	t.Fatalf("acceptance worker did not repair intent: values=%+v runs=%s", values, status)
}

func gitOpsAcceptanceHMACKeyFile(t *testing.T, name string) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
