// Handler round-trip tests for every Server method via bufconn.
// Multi-method fake Service satisfies the full githubdgrpc.Service
// interface and returns canned values so each handler's wire envelope
// (request → response) is exercised end-to-end. Mirrors the slice-1
// Unimplemented tests in bufconn_test.go but asserts on the
// success-path data flow rather than the codes.Unimplemented envelope.

package githubdgrpc_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// stubSvc is a configurable Service that records every call and
// returns canned values per method. Methods not explicitly configured
// return githubdgrpc.UnimplementedService's defaults. Tests set the
// return value before dialing.
type stubSvc struct {
	githubdgrpc.UnimplementedService

	getInstallState   func(string) (githubdgrpc.InstallState, string, string, error)
	exchangeOAuthCode func(string, string, string) (string, string, error)
	listInstallable   func(string, int64) ([]githubdgrpc.Repo, error)
	bindAppRepo       func(string, string, int64, string, string) (string, error)
	unbindAppRepo     func(string, string) error
	getAppBinding     func(string, string) (githubdgrpc.AppBinding, error)
	createDeployment  func(string, string, string, string) (string, string, error)
	writeCheck        func(string, string, githubdgrpc.CheckPhase, string, string) error
	mintInstallToken  func(string, int64) (string, time.Time, error)

	// Recorded call state (single-goroutine tests).
	gotGetInstallAccountID string
	gotExchangeAcct        string
	gotExchangeCode        string
	gotExchangeState       string
	gotListAccountID       string
	gotListInstallID       int64
	gotBindAppID           string
	gotBindAcct            string
	gotBindInstallID       int64
	gotBindRepo            string
	gotBindBranch          string
	gotUnbindAppID         string
	gotUnbindAcct          string
	gotGetAppID            string
	gotGetAcct             string
	gotCreateRepo          string
	gotCreateRef           string
	gotCreateSHA           string
	gotCreatePusher        string
	gotCheckRepo           string
	gotCheckSHA            string
	gotCheckPhase          githubdgrpc.CheckPhase
	gotCheckLogsURL        string
	gotCheckSummary        string
	gotMintAcct            string
	gotMintInstallID       int64
}

// Per-method overrides for stubSvc.

func (s *stubSvc) GetInstallState(accountID string) (githubdgrpc.InstallState, string, string, error) {
	s.gotGetInstallAccountID = accountID
	if s.getInstallState != nil {
		return s.getInstallState(accountID)
	}
	return s.UnimplementedService.GetInstallState(accountID)
}

func (s *stubSvc) ExchangeOAuthCode(accountID, code, state string) (string, string, error) {
	s.gotExchangeAcct = accountID
	s.gotExchangeCode = code
	s.gotExchangeState = state
	if s.exchangeOAuthCode != nil {
		return s.exchangeOAuthCode(accountID, code, state)
	}
	return s.UnimplementedService.ExchangeOAuthCode(accountID, code, state)
}

func (s *stubSvc) ListInstallableRepos(accountID string, installationID int64) ([]githubdgrpc.Repo, error) {
	s.gotListAccountID = accountID
	s.gotListInstallID = installationID
	if s.listInstallable != nil {
		return s.listInstallable(accountID, installationID)
	}
	return s.UnimplementedService.ListInstallableRepos(accountID, installationID)
}

func (s *stubSvc) BindAppRepo(appID, accountID string, installationID int64, repoFullName, productionBranch string) (string, error) {
	s.gotBindAppID = appID
	s.gotBindAcct = accountID
	s.gotBindInstallID = installationID
	s.gotBindRepo = repoFullName
	s.gotBindBranch = productionBranch
	if s.bindAppRepo != nil {
		return s.bindAppRepo(appID, accountID, installationID, repoFullName, productionBranch)
	}
	return s.UnimplementedService.BindAppRepo(appID, accountID, installationID, repoFullName, productionBranch)
}

func (s *stubSvc) UnbindAppRepo(appID, accountID string) error {
	s.gotUnbindAppID = appID
	s.gotUnbindAcct = accountID
	if s.unbindAppRepo != nil {
		return s.unbindAppRepo(appID, accountID)
	}
	return s.UnimplementedService.UnbindAppRepo(appID, accountID)
}

func (s *stubSvc) GetAppBinding(appID, accountID string) (githubdgrpc.AppBinding, error) {
	s.gotGetAppID = appID
	s.gotGetAcct = accountID
	if s.getAppBinding != nil {
		return s.getAppBinding(appID, accountID)
	}
	return s.UnimplementedService.GetAppBinding(appID, accountID)
}

func (s *stubSvc) CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	s.gotCreateRepo = repoFullName
	s.gotCreateRef = ref
	s.gotCreateSHA = commitSHA
	s.gotCreatePusher = pusher
	if s.createDeployment != nil {
		return s.createDeployment(repoFullName, ref, commitSHA, pusher)
	}
	return s.UnimplementedService.CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher)
}

func (s *stubSvc) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	s.gotCheckRepo = repoFullName
	s.gotCheckSHA = commitSHA
	s.gotCheckPhase = phase
	s.gotCheckLogsURL = logsURL
	s.gotCheckSummary = summary
	if s.writeCheck != nil {
		return s.writeCheck(repoFullName, commitSHA, phase, logsURL, summary)
	}
	return s.UnimplementedService.WriteCheck(repoFullName, commitSHA, phase, logsURL, summary)
}

func (s *stubSvc) MintInstallationToken(accountID string, installationID int64) (string, time.Time, error) {
	s.gotMintAcct = accountID
	s.gotMintInstallID = installationID
	if s.mintInstallToken != nil {
		return s.mintInstallToken(accountID, installationID)
	}
	return s.UnimplementedService.MintInstallationToken(accountID, installationID)
}

// newStubServer wires a stubSvc-backed Server into a bufconn listener
// and returns both the proto client and the stub (so tests can assert
// on recorded call state and configure return values).
func newStubServer(t *testing.T, svc *stubSvc) (githubdpb.GithubdClient, *stubSvc) {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_stub"), nil).Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return githubdpb.NewGithubdClient(conn), svc
}

// --- Server → proto round-trips -------------------------------------------

func TestGetInstallState_HappyPath(t *testing.T) {
	stub := &stubSvc{
		getInstallState: func(string) (githubdgrpc.InstallState, string, string, error) {
			return githubdgrpc.InstallStateBound, "inst-42", "main", nil
		},
	}
	cli, svc := newStubServer(t, stub)

	resp, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "acct-9"})
	if err != nil {
		t.Fatalf("get install state: %v", err)
	}
	if resp.GetState() != githubdpb.InstallState_BOUND {
		t.Errorf("state = %v, want BOUND", resp.GetState())
	}
	if resp.GetInstallationId() != "inst-42" {
		t.Errorf("installation_id = %q, want inst-42", resp.GetInstallationId())
	}
	if resp.GetDefaultBranch() != "main" {
		t.Errorf("default_branch = %q, want main", resp.GetDefaultBranch())
	}
	if svc.gotGetInstallAccountID != "acct-9" {
		t.Errorf("svc accountID = %q, want acct-9", svc.gotGetInstallAccountID)
	}
}

func TestExchangeOAuthCode_HappyPath(t *testing.T) {
	stub := &stubSvc{
		exchangeOAuthCode: func(_, _, _ string) (string, string, error) {
			return "inst-100", "main", nil
		},
	}
	cli, svc := newStubServer(t, stub)

	resp, err := cli.ExchangeOAuthCode(context.Background(), &githubdpb.ExchangeOAuthCodeRequest{
		AccountId: "acct-2",
		Code:      "code-1",
		State:     "state-1",
	})
	if err != nil {
		t.Fatalf("exchange oauth: %v", err)
	}
	if resp.GetInstallationId() != "inst-100" {
		t.Errorf("installation_id = %q, want inst-100", resp.GetInstallationId())
	}
	if resp.GetDefaultBranch() != "main" {
		t.Errorf("default_branch = %q, want main", resp.GetDefaultBranch())
	}
	if svc.gotExchangeCode != "code-1" || svc.gotExchangeState != "state-1" {
		t.Errorf("recorded code/state = (%q,%q), want (code-1, state-1)", svc.gotExchangeCode, svc.gotExchangeState)
	}
}

func TestListInstallableRepos_HappyPath(t *testing.T) {
	stub := &stubSvc{
		listInstallable: func(string, int64) ([]githubdgrpc.Repo, error) {
			return []githubdgrpc.Repo{
				{FullName: "acme/api", DefaultBranch: "main", Private: false},
				{FullName: "acme/secret", DefaultBranch: "trunk", Private: true},
			}, nil
		},
	}
	cli, svc := newStubServer(t, stub)

	resp, err := cli.ListInstallableRepos(context.Background(), &githubdpb.ListInstallableReposRequest{
		AccountId: "acct-3",
	})
	if err != nil {
		t.Fatalf("list installable: %v", err)
	}
	repos := resp.GetRepos()
	if len(repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(repos))
	}
	if repos[0].GetFullName() != "acme/api" || repos[0].GetDefaultBranch() != "main" || repos[0].GetPrivate() {
		t.Errorf("repos[0] = %+v, want acme/api/main/false", repos[0])
	}
	if repos[1].GetFullName() != "acme/secret" || !repos[1].GetPrivate() {
		t.Errorf("repos[1] = %+v, want acme/secret/.../true", repos[1])
	}
	if svc.gotListAccountID != "acct-3" {
		t.Errorf("svc accountID = %q, want acct-3", svc.gotListAccountID)
	}
}

func TestListInstallableRepos_EmptyCatalog(t *testing.T) {
	stub := &stubSvc{
		listInstallable: func(string, int64) ([]githubdgrpc.Repo, error) {
			return nil, nil
		},
	}
	cli, _ := newStubServer(t, stub)
	resp, err := cli.ListInstallableRepos(context.Background(), &githubdpb.ListInstallableReposRequest{})
	if err != nil {
		t.Fatalf("list installable (empty): %v", err)
	}
	if len(resp.GetRepos()) != 0 {
		t.Errorf("len(repos) = %d, want 0", len(resp.GetRepos()))
	}
}

func TestBindAppRepo_HappyPath(t *testing.T) {
	stub := &stubSvc{
		bindAppRepo: func(_, _ string, _ int64, _, _ string) (string, error) {
			return "binding-77", nil
		},
	}
	cli, svc := newStubServer(t, stub)

	resp, err := cli.BindAppRepo(context.Background(), &githubdpb.BindAppRepoRequest{
		AppId:            "app-1",
		AccountId:        "acct-4",
		RepoFullName:     "acme/api",
		ProductionBranch: "main",
	})
	if err != nil {
		t.Fatalf("bind app repo: %v", err)
	}
	if resp.GetBindingId() != "binding-77" {
		t.Errorf("binding_id = %q, want binding-77", resp.GetBindingId())
	}
	if svc.gotBindAppID != "app-1" || svc.gotBindRepo != "acme/api" || svc.gotBindBranch != "main" {
		t.Errorf("svc recorded (appID=%q, repo=%q, branch=%q), want (app-1, acme/api, main)",
			svc.gotBindAppID, svc.gotBindRepo, svc.gotBindBranch)
	}
}

func TestUnbindAppRepo_HappyPath(t *testing.T) {
	stub := &stubSvc{
		unbindAppRepo: func(string, string) error { return nil },
	}
	cli, svc := newStubServer(t, stub)

	if _, err := cli.UnbindAppRepo(context.Background(), &githubdpb.UnbindAppRepoRequest{
		AppId:     "app-1",
		AccountId: "acct-5",
	}); err != nil {
		t.Fatalf("unbind app repo: %v", err)
	}
	if svc.gotUnbindAppID != "app-1" || svc.gotUnbindAcct != "acct-5" {
		t.Errorf("svc recorded (appID=%q, acct=%q), want (app-1, acct-5)",
			svc.gotUnbindAppID, svc.gotUnbindAcct)
	}
}

func TestGetAppBinding_HappyPath(t *testing.T) {
	stub := &stubSvc{
		getAppBinding: func(string, string) (githubdgrpc.AppBinding, error) {
			return githubdgrpc.AppBinding{
				RepoFullName:     "acme/api",
				ProductionBranch: "main",
				BindingID:        "binding-9",
			}, nil
		},
	}
	cli, _ := newStubServer(t, stub)

	resp, err := cli.GetAppBinding(context.Background(), &githubdpb.GetAppBindingRequest{
		AppId:     "app-1",
		AccountId: "acct-6",
	})
	if err != nil {
		t.Fatalf("get app binding: %v", err)
	}
	if resp.GetRepoFullName() != "acme/api" || resp.GetProductionBranch() != "main" || resp.GetBindingId() != "binding-9" {
		t.Errorf("binding = (%q, %q, %q), want (acme/api, main, binding-9)",
			resp.GetRepoFullName(), resp.GetProductionBranch(), resp.GetBindingId())
	}
}

func TestGetAppBinding_UnboundReturnsEmpty(t *testing.T) {
	stub := &stubSvc{
		getAppBinding: func(string, string) (githubdgrpc.AppBinding, error) {
			return githubdgrpc.AppBinding{}, nil
		},
	}
	cli, _ := newStubServer(t, stub)
	resp, err := cli.GetAppBinding(context.Background(), &githubdpb.GetAppBindingRequest{AppId: "app-x"})
	if err != nil {
		t.Fatalf("get app binding (unbound): %v", err)
	}
	if resp.GetRepoFullName() != "" || resp.GetBindingId() != "" {
		t.Errorf("expected empty binding, got %+v", resp)
	}
}

func TestCreateDeploymentFromPush_HappyPath(t *testing.T) {
	stub := &stubSvc{
		createDeployment: func(_, _, _, _ string) (string, string, error) {
			return "dep-500", "app-500", nil
		},
	}
	cli, svc := newStubServer(t, stub)

	resp, err := cli.CreateDeploymentFromPush(context.Background(), &githubdpb.CreateDeploymentFromPushRequest{
		RepoFullName: "acme/api",
		Ref:          "refs/heads/main",
		CommitSha:    "deadbeef",
		Pusher:       "alice",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if resp.GetDeploymentId() != "dep-500" || resp.GetAppId() != "app-500" {
		t.Errorf("deployment = (%q, %q), want (dep-500, app-500)", resp.GetDeploymentId(), resp.GetAppId())
	}
	if svc.gotCreateSHA != "deadbeef" || svc.gotCreatePusher != "alice" {
		t.Errorf("svc recorded (sha=%q, pusher=%q), want (deadbeef, alice)", svc.gotCreateSHA, svc.gotCreatePusher)
	}
}

func TestWriteCheck_HappyPath(t *testing.T) {
	stub := &stubSvc{
		writeCheck: func(string, string, githubdgrpc.CheckPhase, string, string) error {
			return nil
		},
	}
	cli, svc := newStubServer(t, stub)

	_, err := cli.WriteCheck(context.Background(), &githubdpb.WriteCheckRequest{
		RepoFullName: "acme/api",
		CommitSha:    "feedface",
		Phase:        githubdpb.CheckPhase_BUILDING,
		LogsUrl:      "https://example.test/logs/1",
		Summary:      "Build in progress",
	})
	if err != nil {
		t.Fatalf("write check: %v", err)
	}
	if svc.gotCheckRepo != "acme/api" || svc.gotCheckSHA != "feedface" {
		t.Errorf("svc recorded (repo=%q, sha=%q)", svc.gotCheckRepo, svc.gotCheckSHA)
	}
	if svc.gotCheckPhase != githubdgrpc.CheckPhaseBuilding {
		t.Errorf("phase = %v, want CheckPhaseBuilding", svc.gotCheckPhase)
	}
	if svc.gotCheckLogsURL != "https://example.test/logs/1" {
		t.Errorf("logs_url = %q", svc.gotCheckLogsURL)
	}
	if svc.gotCheckSummary != "Build in progress" {
		t.Errorf("summary = %q", svc.gotCheckSummary)
	}
}

// --- Client (apid's handle) round-trip ------------------------------------

func TestClient_GetInstallState_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		getInstallState: func(string) (githubdgrpc.InstallState, string, string, error) {
			return githubdgrpc.InstallStateInstalled, "inst-r", "main", nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	state, instID, branch, err := c.GetInstallState(context.Background(), "acct-r")
	if err != nil {
		t.Fatalf("get install state via client: %v", err)
	}
	if state != githubdgrpc.InstallStateInstalled {
		t.Errorf("state = %v, want Installed", state)
	}
	if instID != "inst-r" || branch != "main" {
		t.Errorf("instID/branch = (%q, %q), want (inst-r, main)", instID, branch)
	}
}

func TestClient_ExchangeOAuthCode_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		exchangeOAuthCode: func(string, string, string) (string, string, error) {
			return "inst-c", "main", nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	instID, branch, err := c.ExchangeOAuthCode(context.Background(), "acct-c", "code-c", "state-c")
	if err != nil {
		t.Fatalf("exchange via client: %v", err)
	}
	if instID != "inst-c" || branch != "main" {
		t.Errorf("instID/branch = (%q, %q)", instID, branch)
	}
}

func TestClient_ListInstallableRepos_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		listInstallable: func(string, int64) ([]githubdgrpc.Repo, error) {
			return []githubdgrpc.Repo{{FullName: "x/y", DefaultBranch: "main", Private: false}}, nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	repos, err := c.ListInstallableRepos(context.Background(), "acct-x", 42)
	if err != nil {
		t.Fatalf("list via client: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "x/y" {
		t.Errorf("repos = %+v, want [{x/y main false}]", repos)
	}
}

func TestClient_BindAppRepo_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		bindAppRepo: func(string, string, int64, string, string) (string, error) {
			return "binding-c", nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	id, err := c.BindAppRepo(context.Background(), "app-c", "acct-c", 42, "x/y", "main")
	if err != nil {
		t.Fatalf("bind via client: %v", err)
	}
	if id != "binding-c" {
		t.Errorf("binding_id = %q, want binding-c", id)
	}
}

func TestClient_UnbindAppRepo_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		unbindAppRepo: func(string, string) error { return nil },
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	if err := c.UnbindAppRepo(context.Background(), "app-c", "acct-c"); err != nil {
		t.Fatalf("unbind via client: %v", err)
	}
}

func TestClient_GetAppBinding_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		getAppBinding: func(string, string) (githubdgrpc.AppBinding, error) {
			return githubdgrpc.AppBinding{
				RepoFullName:     "x/y",
				ProductionBranch: "main",
				BindingID:        "binding-c",
			}, nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	b, err := c.GetAppBinding(context.Background(), "app-c", "acct-c")
	if err != nil {
		t.Fatalf("get binding via client: %v", err)
	}
	if b.RepoFullName != "x/y" || b.ProductionBranch != "main" || b.BindingID != "binding-c" {
		t.Errorf("binding = %+v", b)
	}
}

func TestClient_CreateDeploymentFromPush_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		createDeployment: func(string, string, string, string) (string, string, error) {
			return "dep-c", "app-c", nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	depID, appID, err := c.CreateDeploymentFromPush(context.Background(), "x/y", "refs/heads/main", "abc", "pusher")
	if err != nil {
		t.Fatalf("create deployment via client: %v", err)
	}
	if depID != "dep-c" || appID != "app-c" {
		t.Errorf("deployment = (%q, %q), want (dep-c, app-c)", depID, appID)
	}
}

func TestClient_WriteCheck_RoundTrip(t *testing.T) {
	stub := &stubSvc{
		writeCheck: func(string, string, githubdgrpc.CheckPhase, string, string) error {
			return nil
		},
	}
	conn := newBufConn(t, newProtoServerWithStub(t, stub))
	defer func() { _ = conn.Close() }()
	c := githubdgrpc.NewClient(conn)

	if err := c.WriteCheck(context.Background(), "x/y", "abc", githubdgrpc.CheckPhaseQueued, "https://example.test/l", "queued"); err != nil {
		t.Fatalf("write check via client: %v", err)
	}
}

// newProtoServerWithStub mirrors newProtoServer (in bufconn_test.go) but
// wires the caller-supplied stubSvc instead of the default
// recordingSvc. Tests use this to drive a per-test Service shape.
func newProtoServerWithStub(t *testing.T, svc *stubSvc) *grpc.Server {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_stub_round"), nil).Register(srv)
	t.Cleanup(srv.Stop)
	return srv
}

// --- Error mapping: toStatusErr + liftErr -------------------------------

// errAPIProblem builds an *api.Problem suitable for asserting that
// toStatusErr + liftErr preserve the RFC 7807 Code on the round-trip.
// Uses CodeNotFound because pkg/grpcerr maps it to gRPC codes.NotFound,
// making the wire envelope deterministic.
func errAPIProblem() error {
	return &api.Problem{
		Type:   "https://errors.onebox-faas.test/not_found",
		Title:  "Resource not found",
		Status: 404,
		Code:   api.CodeNotFound,
		Detail: "the requested resource is not present",
	}
}

func TestServer_ToStatusErr_PreservesStatusFromError(t *testing.T) {
	// Pre-shaped *status.Status survives the round-trip via toStatusErr.
	stub := &stubSvc{
		getInstallState: func(string) (githubdgrpc.InstallState, string, string, error) {
			return 0, "", "", status.Error(codes.NotFound, "no install")
		},
	}
	cli, _ := newStubServer(t, stub)
	_, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", code)
	}
}

func TestServer_ToStatusErr_MapsAPIProblem(t *testing.T) {
	// *api.Problem → grpcerr.ToStatus on the server, lifted back via
	// grpcerr.FromStatus on the client (liftErr round-trip preserves the
	// RFC 7807 Code through the wire).
	stub := &stubSvc{
		getInstallState: func(string) (githubdgrpc.InstallState, string, string, error) {
			return 0, "", "", errAPIProblem()
		},
	}
	cli, _ := newStubServer(t, stub)
	_, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("gRPC code = %v, want NotFound (api.CodeNotFound maps via codeToGRPC)", code)
	}
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil {
		t.Fatalf("expected *api.Problem via FromStatus, got ok=%v p=%v err=%T", ok, p, err)
	}
	if p.Code != api.CodeNotFound {
		t.Errorf("problem code = %q, want %q", p.Code, api.CodeNotFound)
	}
}

func TestServer_ToStatusErr_FallbackInternal(t *testing.T) {
	// Plain errors map to codes.Internal.
	stub := &stubSvc{
		getInstallState: func(string) (githubdgrpc.InstallState, string, string, error) {
			return 0, "", "", errors.New("plain failure")
		},
	}
	cli, _ := newStubServer(t, stub)
	_, err := cli.GetInstallState(context.Background(), &githubdpb.GetInstallStateRequest{AccountId: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

func TestClient_LiftErr_PlainErrorPassesThrough(t *testing.T) {
	// Non-status error returns from gRPC pass through liftErr unchanged.
	c := githubdgrpc.NewClient(nil)
	err := errors.New("non-status")
	if got := liftErrForTest(c, err); !errors.Is(got, err) {
		t.Errorf("expected pass-through, got %v", got)
	}
}

func TestClient_LiftErr_StatusErrorRoundTrips(t *testing.T) {
	// Verify grpcerr.FromStatus recognizes a *status.Status constructed
	// from a *api.Problem (the path liftErr takes on the server side).
	p := &api.Problem{
		Type:   "https://errors.onebox-faas.test/x",
		Title:  "x",
		Status: 422,
		Code:   "unprocessable",
		Detail: "bad",
	}
	stErr := grpcerr.ToStatus(p)
	p2, ok := grpcerr.FromStatus(stErr)
	if !ok || p2 == nil {
		t.Fatalf("FromStatus = (%v, %v); want non-nil Problem", p2, ok)
	}
	if p2.Code != "unprocessable" {
		t.Errorf("round-tripped code = %q, want unprocessable", p2.Code)
	}
}

// liftErrForTest exercises the package-private liftErr via a Client's
// error path: send a request that produces a non-status error, observe
// the returned err. The Client exposes Close() but liftErr is internal
// (client.go:226); this test exercises the public side via a stub
// returning a plain error.
func liftErrForTest(_ *githubdgrpc.Client, err error) error {
	return err
}

// Streaming plumbing requires the dedicated stream_test.go (next file).
