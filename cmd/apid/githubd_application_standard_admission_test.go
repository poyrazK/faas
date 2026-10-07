// adr: 595. GitHub builds install captured intent before durable enqueue.
package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestMemGithubApplicationStandardsDeployment(t *testing.T) {
	standardGithubDeploymentTests(t, func(t *testing.T) standardProjectBoundaryStore { return state.NewMemStore() })
}

func standardGithubDeploymentTests(t *testing.T, newStore func(*testing.T) standardProjectBoundaryStore) {
	t.Helper()
	for _, name := range []string{"push", "preview", "interrupted", "worker lease", "blocked", "ownership changed", "account mismatch"} {
		t.Run(name, func(t *testing.T) { standardGithubDeployment(t, newStore(t), name) })
	}
}

// Use the generated client and real receiver over an in-memory gRPC transport.
// These tests stop at durable build enqueue, before builder or VM execution.
func standardGithubClient(t *testing.T, bridge *githubdBridge) githubdpb.GithubdClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	githubdpb.RegisterGithubdServer(server, bridge)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection, err := grpc.NewClient("passthrough:///standards-bridge", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return githubdpb.NewGithubdClient(connection)
}

func standardGithubDeployment(t *testing.T, base standardProjectBoundaryStore, name string) {
	t.Helper()
	e := standardProjectSetup(t, base)
	project, err := base.CreateProject(t.Context(), state.Project{AccountID: e.owner.Account.ID, Slug: "github-standard-services"})
	if err != nil {
		t.Fatal(err)
	}
	input := state.App{AccountID: e.owner.Account.ID, OrgID: e.owner.PersonalOrg.ID, ProjectID: project.ID,
		Slug: "github-standard-api", WorkloadName: "api", RootDir: "apps/api", RAMMB: 128,
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP}
	kind := githubdpb.EnqueueBuildEventKind_EVENT_KIND_PUSH
	var app state.App
	if name == "preview" {
		batch, ok := base.(state.PRPreviewSetBatchStore)
		if !ok {
			t.Fatal("store lacks the production preview reservation entry point")
		}
		expiry := time.Now().Add(time.Hour)
		input.PreviewOfSlug, input.PreviewPrNumber = "api", 17
		input.PreviewPrState, input.PreviewExpiresAt = state.PreviewPrStateOpen, &expiry
		apps, reserveErr := batch.ReservePRPreviewSet(t.Context(), state.PRPreviewHead{InstallationID: 7, RepoFullName: "company/services", PRNumber: 17, CommitSHA: strings.Repeat("a", 40)}, []state.App{input}, api.MustLimitsFor(api.PlanPro))
		if reserveErr != nil || len(apps) != 1 {
			t.Fatalf("reserve first source-backed preview: %+v %v", apps, reserveErr)
		}
		app = apps[0]
		kind = githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST
	} else {
		app, err = base.CreateApp(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
	}
	row, err := base.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || row.State != "pending" || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
		t.Fatalf("new GitHub service should start with captured, uninstalled intent: %+v %v", row, err)
	}
	unrelated, err := base.CreateApp(t.Context(), state.App{AccountID: app.AccountID, OrgID: app.OrgID, Slug: "github-unrelated-pending", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	var store githubdBridgeStore = base
	var interrupted *interruptedProjectStandardStore
	var held state.ApplicationStandardEnrollmentClaim
	want := codes.OK
	switch name {
	case "interrupted":
		interrupted = &interruptedProjectStandardStore{standardProjectBoundaryStore: base, interrupted: true}
		store, want = interrupted, codes.Unavailable
	case "worker lease":
		held, err = base.ClaimApplicationStandardEnrollmentForApp(t.Context(), state.ApplicationStandardEnrollmentClaimRequest{OrgID: app.OrgID, AppID: app.ID, DesiredRevision: row.DesiredRevision, Owner: "background-worker"})
		if err != nil {
			t.Fatal(err)
		}
		want = codes.Unavailable
	case "blocked":
		if err := base.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		want = codes.FailedPrecondition
	case "ownership changed":
		store = &detachedProjectStandardStore{standardProjectBoundaryStore: base}
		want = codes.FailedPrecondition
	case "account mismatch":
		want = codes.NotFound
	}
	notifier := &bridgeStubNotifier{}
	bridge := newBridge(t, store, notifier)
	path := filepath.Join(bridge.stagingRoot, "source.tar.gz")
	source := standardProjectSource(t, "api")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	request := &githubdpb.EnqueueBuildRequest{AccountId: app.AccountID, AppId: app.ID, CommitSha: strings.Repeat("a", 40),
		SourcePath: path, SourceBytes: int64(len(source)), SourceUrl: "https://codeload.example.com/company/services/tar.gz/" + strings.Repeat("a", 40),
		RepoFullName: "company/services", Branch: "main", EventKind: kind}
	if name == "preview" {
		request.PullRequestNumber = 17
	}
	if name == "account mismatch" {
		request.AccountId = e.owner.PersonalOrg.ID
	}
	client := standardGithubClient(t, bridge)
	response, err := client.EnqueueBuild(t.Context(), request)
	if status.Code(err) != want {
		t.Fatalf("first GitHub build: response=%+v code=%s, want=%s: %v", response, status.Code(err), want, err)
	}
	if want != codes.OK {
		standardGithubNoBuild(t, base, app, notifier)
		switch name {
		case "interrupted":
			// The failed request releases its own generation without a TTL wait.
			claim, err := base.ClaimApplicationStandardEnrollmentForApp(t.Context(), state.ApplicationStandardEnrollmentClaimRequest{OrgID: app.OrgID, AppID: app.ID, DesiredRevision: row.DesiredRevision, Owner: "after-interruption"})
			if err != nil {
				t.Fatalf("request stranded its worker lease: %v", err)
			}
			if err := base.ReleaseApplicationStandardEnrollmentWorker(t.Context(), claim); err != nil {
				t.Fatal(err)
			}
			interrupted.interrupted = false
		case "worker lease":
			// Interactive enqueue cannot steal or release a background claim.
			if _, err := base.MaterializeApplicationStandardEnrollment(t.Context(), held); err != nil {
				t.Fatalf("request damaged the background worker claim: %v", err)
			}
		default:
			row, err := base.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
			if err != nil || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
				t.Fatalf("refused build installed controls or observation: %+v %v", row, err)
			}
			if name == "blocked" && (row.State != "blocked" || row.ErrorCode == "") {
				t.Fatalf("missing durable blocker: %+v", row)
			}
			return
		}
		response, err = client.EnqueueBuild(t.Context(), request)
		if err != nil {
			t.Fatalf("immediate first-build recovery: %v", err)
		}
	}
	response = mustEnqueueBuildResponse(t, response, "successful first build returned no response")
	standardProjectInstalled(t, e, app.ID, 1)
	deployment, err := base.DeploymentByID(t.Context(), response.DeploymentId)
	expectedKind := state.DeploymentKindGitHub
	if name == "preview" {
		expectedKind = state.DeploymentKindPreview
	}
	if err != nil || deployment.AppID != app.ID || deployment.Status != state.DeployBuilding || deployment.Kind != expectedKind {
		t.Fatalf("durable GitHub deployment: %+v %v", deployment, err)
	}
	build, err := base.BuildByID(t.Context(), response.BuildId)
	if err != nil || build.DeploymentID != deployment.ID || build.Status != state.BuildQueued {
		t.Fatalf("durable first build: %+v %v", build, err)
	}
	rate, err := base.ReadAccountDeployRate(t.Context(), app.AccountID, api.PlanPro.DeploysPerHour(), time.Now().UTC())
	if err != nil || rate.Used != 1 {
		t.Fatalf("one accepted build should consume one admission: %+v %v", rate, err)
	}
	row, err = base.GetApplicationStandardEnrollment(t.Context(), unrelated.OrgID, unrelated.ID)
	if err != nil || row.State != "pending" || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
		t.Fatalf("interactive build repaired an unrelated service: %+v %v", row, err)
	}
}

func standardGithubNoBuild(t *testing.T, store standardProjectBoundaryStore, app state.App, notifier *bridgeStubNotifier) {
	t.Helper()
	deployments, err := store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("refused build leaked deployments: %+v %v", deployments, err)
	}
	rate, err := store.ReadAccountDeployRate(t.Context(), app.AccountID, api.PlanPro.DeploysPerHour(), time.Now().UTC())
	if err != nil || rate.Used != 0 {
		t.Fatalf("refused build consumed deploy rate: %+v %v", rate, err)
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.channels) != 0 {
		t.Fatalf("refused build notified a consumer: %+v", notifier.channels)
	}
}
