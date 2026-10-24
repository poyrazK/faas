// github_webhook_deploy_e2e_test.go — GitHub deployments and pull-request
// previews from a signed webhook to a queued deployment and build.
//
// Every other GitHub test stops at one seam: the webhook handler, the
// dispatcher with a recording enqueuer, or the apid bridge with a stub store.
// This one connects them. A real apid process listens on its githubd bridge
// socket; githubd's production pieces run in process around it: the
// App-secret HMAC webhook handler, the PostgreSQL delivery inbox and its
// worker, the push and pull-request dispatcher with real repository scan and
// reconcile, source staging, and the gRPC enqueuer. Only GitHub itself is
// replaced, by a fixed source tree; githubd's GitHub API base URL is not
// configurable, so the real githubd binary cannot be pointed at a fake.
//
// The chain stops at the queued build: building and serving it needs a
// builder microVM (the native metal source-deploy tests cover that half).
package e2e_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	githubE2ERepo         = "octo/e2e-api"
	githubE2EInstallation = int64(9001)
	githubE2ESecret       = "e2e-github-app-webhook-secret"
)

// TestE2E_GitHubWebhookEnqueuesDeployments is the acceptance evidence for
// GitHub deployments and pull-request previews.
func TestE2E_GitHubWebhookEnqueuesDeployments(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sockDir, err := os.MkdirTemp("", "ghb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	bridgeSock := filepath.Join(sockDir, "bridge.sock")
	workDir := t.TempDir()
	h := e2etest.Start(t, pool, e2etest.APID,
		"FAAS_APID_GITHUBD_BRIDGE_SOCK="+bridgeSock,
		"FAAS_GITHUBD_WORK_DIR="+workDir)
	waitForSocket(t, bridgeSock, 30*time.Second)

	key := h.SeedAccount(ctx, api.PlanPro)
	accountID := accountIDFromKey(t, ctx, pool, key)
	store := state.NewPgStore(pool)
	project, err := store.CreateProject(ctx, state.Project{AccountID: accountID, Slug: "gh-e2e",
		InstallID: githubE2EInstallation, RepoFullName: githubE2ERepo, ProductionBranch: "main"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: accountID,
		InstallationID: githubE2EInstallation, DefaultBranch: "main"}); err != nil {
		t.Fatalf("seed installation: %v", err)
	}

	webhookURL := startGitHubd(t, pool, store, workDir, bridgeSock)

	// -- push to the production branch ------------------------------------
	pushSHA := strings.Repeat("a1", 20)
	postGitHubWebhook(t, webhookURL, "push", fmt.Sprintf(`{"ref":"refs/heads/main","before":"%s","after":"%s",
		"repository":{"full_name":%q,"name":"e2e-api"},"pusher":{"name":"alice"},"installation":{"id":%d}}`,
		strings.Repeat("0", 40), pushSHA, githubE2ERepo, githubE2EInstallation))
	pushed := waitForGitHubDeployment(t, pool, project.ID, pushSHA, state.DeploymentKindGitHub)
	if pushed.scope != "" && pushed.scope != "default" {
		t.Errorf("push deployment scope = %q, want the production default", pushed.scope)
	}
	t.Logf("push: app %s deployment %s build %s (%s)", pushed.appSlug, pushed.deploymentID, pushed.buildID, pushed.buildStatus)

	// -- pull request opened -----------------------------------------------
	// A preview is created for the app bound to the repository.
	if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: pushed.appID, AccountID: accountID,
		BindingID: "bind-" + pushed.appID, InstallID: githubE2EInstallation, RepoFullName: githubE2ERepo,
		ProductionBranch: "main"}); err != nil {
		t.Fatalf("bind app: %v", err)
	}
	prSHA := strings.Repeat("b2", 20)
	postGitHubWebhook(t, webhookURL, "pull_request", fmt.Sprintf(`{"action":"opened","number":7,
		"pull_request":{"state":"open","head_sha":%q,"head_ref":"feat/x","base":{"ref":"main"},
		"head":{"ref":"feat/x","sha":%q,"repo":{"full_name":%q}}},
		"repository":{"full_name":%q,"name":"e2e-api"},"installation":{"id":%d},"sender":{"login":"octocat"}}`,
		prSHA, prSHA, githubE2ERepo, githubE2ERepo, githubE2EInstallation))
	preview := waitForGitHubDeployment(t, pool, project.ID, prSHA, state.DeploymentKindPreview)
	if preview.appID == pushed.appID || !strings.HasPrefix(preview.appSlug, "pr-7-") {
		t.Fatalf("preview deployment on app %s (%s), want a pr-7- preview app separate from %s", preview.appSlug, preview.appID, pushed.appID)
	}
	t.Logf("pull request: preview app %s deployment %s build %s (%s)", preview.appSlug, preview.deploymentID, preview.buildID, preview.buildStatus)
}

// startGitHubd runs githubd's production webhook path in process against
// the harness database and apid bridge, and returns the webhook URL.
func startGitHubd(t *testing.T, pool *pgxpool.Pool, store *state.PgStore, workDir, bridgeSock string) string {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := grpc.NewClient("unix://"+bridgeSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial apid bridge: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	svc := githubd.NewService(log)
	svc.Bindings = githubE2EBindings{store}
	svc.Installs = githubE2EInstalls{store}
	svc.Source = githubE2ESource{fsys: fstest.MapFS{
		"package.json": &fstest.MapFile{Data: []byte(`{"name":"e2e-api","version":"1.0.0","scripts":{"start":"node index.js"}}` + "\n")},
		"index.js":     &fstest.MapFile{Data: []byte("require('http').createServer((_, res) => res.end('ok')).listen(process.env.PORT || 8080);\n")},
	}}
	svc.Reconcile = reconcile.NewService(store, audit.New(store, log, nil, "githubd_e2e"), log)
	svc.WorkDir = workDir
	svc.Enqueuer = githubd.NewApidEnqueuer(githubE2EBridge{githubdpb.NewGithubdClient(conn)}, log)

	deliveries := githubd.NewPGWebhookDeliveryStore(pool)
	server := &githubd.Server{Service: svc, Log: log, WebhookSecret: []byte(githubE2ESecret), Deliveries: deliveries}
	ts := httptest.NewServer(server.WebhookLoopbackHandler())
	t.Cleanup(ts.Close)
	workerCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		githubd.RunWebhookDeliveryWorker(workerCtx, deliveries, svc, log)
	}()
	t.Cleanup(func() { stop(); <-done })
	return ts.URL + "/webhooks/github"
}

// postGitHubWebhook signs body with the App webhook secret as GitHub does and
// requires the durable inbox to accept it.
func postGitHubWebhook(t *testing.T, url, event, body string) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(githubE2ESecret))
	_, _ = mac.Write([]byte(body))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s webhook: %v", event, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s webhook: status=%d body=%s", event, resp.StatusCode, raw)
	}
}

type githubE2EDeployment struct {
	appID, appSlug, deploymentID, scope, buildID, buildStatus string
}

// waitForGitHubDeployment waits for the worker to process the delivery and
// apid to record a deployment of kind for sha in the project, with its build.
func waitForGitHubDeployment(t *testing.T, pool *pgxpool.Pool, projectID, sha string, kind state.DeploymentKind) githubE2EDeployment {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var got githubE2EDeployment
	for {
		err := pool.QueryRow(context.Background(), `
			select a.id::text, a.slug, d.id::text, coalesce(d.scope, ''), b.id::text, b.status
			  from deployments d
			  join apps a on a.id = d.app_id
			  join builds b on b.deployment_id = d.id
			 where a.project_id = $1 and d.commit_sha = $2 and d.kind = $3
			 order by d.created_at desc limit 1`,
			projectID, sha, string(kind)).Scan(&got.appID, &got.appSlug, &got.deploymentID, &got.scope, &got.buildID, &got.buildStatus)
		if err == nil {
			return got
		}
		if time.Now().After(deadline) {
			var deliveries string
			_ = pool.QueryRow(context.Background(), `select coalesce(string_agg(event_type || ':' || status || ':' || coalesce(last_error, ''), '; '), '')
				from github_webhook_deliveries`).Scan(&deliveries)
			t.Fatalf("no %s deployment with build for %s in project %s: %v (deliveries: %s)", kind, sha, projectID, err, deliveries)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("apid githubd bridge socket %s never appeared", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type githubE2EBindings struct{ store *state.PgStore }

func (b githubE2EBindings) GetAppBinding(ctx context.Context, repo, branch string) (state.GitHubBinding, error) {
	return b.store.GithubInstallBindingForRepoBranch(ctx, repo, branch)
}

func (b githubE2EBindings) GetAppBindingForInstallation(ctx context.Context, repo, branch string, installationID int64) (state.GitHubBinding, error) {
	return b.store.GithubInstallBindingForRepoBranchInstallation(ctx, repo, branch, installationID)
}

type githubE2EInstalls struct{ store *state.PgStore }

func (i githubE2EInstalls) ForAccount(ctx context.Context, accountID string) (state.GitHubInstall, error) {
	return i.store.GitHubInstallForAccount(ctx, accountID)
}

func (i githubE2EInstalls) ForAccountInstallation(ctx context.Context, accountID string, installationID int64) (state.GitHubInstall, error) {
	return i.store.GitHubInstallForAccountInstallation(ctx, accountID, installationID)
}

// githubE2ESource stands in for GitHub's tarball download.
type githubE2ESource struct{ fsys fs.FS }

func (s githubE2ESource) Fetch(context.Context, string, int64, string, string) (githubd.SourceTree, error) {
	return githubE2ETree{s.fsys}, nil
}

type githubE2ETree struct{ fsys fs.FS }

func (t githubE2ETree) FS() fs.FS  { return t.fsys }
func (githubE2ETree) Close() error { return nil }

// githubE2EBridge adapts the generated client to githubd.ApidBuildClient.
type githubE2EBridge struct{ client githubdpb.GithubdClient }

func (b githubE2EBridge) EnqueueBuild(ctx context.Context, in *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error) {
	return b.client.EnqueueBuild(ctx, in)
}
