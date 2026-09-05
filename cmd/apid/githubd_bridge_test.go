// githubd_bridge_test.go — unit tests for the githubd → apid
// build-enqueue bridge receiver (issue #432 phase 5 review
// follow-up, Finding #3).
//
// Pins the validation pipeline in cmd/apid/githubd_bridge.go's
// EnqueueBuild RPC without spinning up a real Postgres. The store,
// notifier, ops, and audit seams are stubbed. Tests cover:
//
//   - happy path: app exists, source under staging root, build row
//     created, deployment row created, build_queued notify fires,
//     metric increments
//   - missing app: codes.NotFound
//   - account mismatch: codes.NotFound (IDOR-safe; not
//     PermissionDenied)
//   - inactive app: codes.FailedPrecondition
//   - source path outside staging root: codes.InvalidArgument
//     (Finding #4)
//   - source_path suffix must be .tar.gz: codes.InvalidArgument
//   - source_bytes > 2 GB ceiling: codes.ResourceExhausted
//   - source_bytes = 0: codes.InvalidArgument
//   - source path not a regular file (e.g. directory): codes.InvalidArgument
//   - source bytes mismatch (declared != on-disk): codes.InvalidArgument
//   - CreateDeployment wrapping a *api.Problem maps to the
//     matching gRPC code via pkg/grpcerr
//   - prev deployment supersede: when a prior non-terminal
//     deployment exists, the supersede notify is emitted
//   - first deploy: no supersede notify
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mustEnqueueBuildResponse is the SA5011 escape hatch for the
// EnqueueBuild happy-path test: the proto-generated method can
// legitimately return (nil, nil), but we want a real response. A
// helper that t.Fatal()s and returns the value lets staticcheck
// see the value is non-nil at the call site.
func mustEnqueueBuildResponse(t *testing.T, resp *githubdpb.EnqueueBuildResponse, msg string) *githubdpb.EnqueueBuildResponse {
	t.Helper()
	if resp == nil {
		t.Fatal(msg)
	}
	return resp
}

// --- stubs ------------------------------------------------------------------

// bridgeStubStore satisfies githubdBridgeStore. Per-test fields
// drive the calls. Default zero value means "no app, no errors,
// no prior deployment".
type bridgeStubStore struct {
	mu sync.Mutex

	app    state.App
	appErr error

	prev    state.Deployment
	prevErr error

	createDeploymentReturned state.Deployment
	createDeploymentErr      error

	createdBuild   state.Build
	createBuildErr error

	updateStatusCalls []state.DeploymentStatus
}

func (s *bridgeStubStore) AppByID(_ context.Context, _ string) (state.App, error) {
	return s.app, s.appErr
}

func (s *bridgeStubStore) LatestDeployment(_ context.Context, _ string) (state.Deployment, error) {
	return s.prev, s.prevErr
}

func (s *bridgeStubStore) CreateDeployment(_ context.Context, d state.Deployment) (state.Deployment, error) {
	// The handler populates ID/Status when the store returns
	// a zero-valued Deployment on success; mirror the
	// production pgstore behaviour for the test.
	if s.createDeploymentReturned.ID == "" {
		d.ID = "dep-1"
	}
	if d.Status == "" {
		d.Status = state.DeployPending
	}
	// Capture the populated Deployment so the bridge tests can
	// assert on the annotation fields (issue #977 / ADR-116).
	// Creation is called once per EnqueueBuild, so no mutex is
	// required — the bridge is single-threaded.
	s.createDeploymentReturned = d
	if s.createDeploymentErr != nil {
		return state.Deployment{}, s.createDeploymentErr
	}
	return d, nil
}

func (s *bridgeStubStore) UpdateDeploymentStatus(_ context.Context, _ string, st state.DeploymentStatus, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateStatusCalls = append(s.updateStatusCalls, st)
	return nil
}

func (s *bridgeStubStore) CreateBuild(_ context.Context, deploymentID string, kind state.DeploymentKind, _ int64, _ string) (state.Build, error) {
	if s.createBuildErr != nil {
		return state.Build{}, s.createBuildErr
	}
	if s.createdBuild.ID == "" {
		return state.Build{ID: "b-1", DeploymentID: deploymentID, Kind: kind}, nil
	}
	return s.createdBuild, nil
}

func (s *bridgeStubStore) CreateBuildWithID(ctx context.Context, id, deploymentID string, kind state.DeploymentKind, sourceBytes int64, path string) (state.Build, error) {
	s.updateStatusCalls = append(s.updateStatusCalls, state.DeployBuilding)
	return s.CreateBuild(ctx, deploymentID, kind, sourceBytes, path)
}

func (s *bridgeStubStore) FailSourceDeployment(ctx context.Context, id, message string) error {
	return s.UpdateDeploymentStatus(ctx, id, state.DeployFailed, message)
}

// bridgeStubNotifier satisfies githubdBridgeNotifier. Records every
// Notify call by channel name.
type bridgeStubNotifier struct {
	mu       sync.Mutex
	channels []string
	failErr  error
}

func (n *bridgeStubNotifier) Notify(_ context.Context, channel, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels = append(n.channels, channel)
	return n.failErr
}

// discLog discards every log line. The receiver's log calls are
// best-effort diagnostics; tests assert on the gRPC return not the
// log stream.
func discLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newBridge wires a githubdBridge with all stubs and a default
// staging root that matches the production default. The
// stagingRoot/spoolRoot pair is what Finding #4's allowlist
// checks against.
func newBridge(t *testing.T, store githubdBridgeStore, notif githubdBridgeNotifier) *githubdBridge {
	t.Helper()
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	ops := wire.NewOpsMetrics("apid")
	return &githubdBridge{
		store:       store,
		notif:       notif,
		log:         discLog(),
		ops:         ops,
		spool:       spoolRoot,
		stagingRoot: stagingRoot,
		spoolRoot:   spoolRoot,
	}
}

// stageFixtureFile writes a tarball-shaped file into the staging
// root and returns its path + size. The handler's os.Stat check
// operates on regular files; the .tar.gz suffix is required.
func stageFixtureFile(t *testing.T, rootDir, subpath string, body []byte) (string, int64) {
	t.Helper()
	dir := filepath.Join(rootDir, subpath)
	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	path := filepath.Join(dir, "source.tar.gz")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return path, st.Size()
}

// --- tests ------------------------------------------------------------------

func TestEnqueueBuild_HappyPath(t *testing.T) {
	accountID := "acct-1"
	appID := "app-1"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()

	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, appID, "abc123"), []byte("tiny-tar"))

	store := &bridgeStubStore{app: state.App{ID: appID, AccountID: accountID, Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	ops := wire.NewOpsMetrics("apid")
	g := &githubdBridge{
		store:       store,
		notif:       notif,
		log:         discLog(),
		ops:         ops,
		spool:       spoolRoot,
		stagingRoot: stagingRoot,
		spoolRoot:   spoolRoot,
	}

	resp, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:    accountID,
		AppId:        appID,
		CommitSha:    "abc123",
		SourcePath:   path,
		SourceUrl:    "https://codeload.example.com/repo/tar.gz/abc123",
		SourceBytes:  size,
		RepoFullName: "owner/repo",
		Branch:       "main",
		Pusher:       "octocat",
	})
	if err != nil {
		t.Fatalf("EnqueueBuild: %v", err)
	}
	resp = mustEnqueueBuildResponse(t, resp, "response is nil")
	if resp.BuildId != "b-1" {
		t.Errorf("BuildId = %q, want %q", resp.BuildId, "b-1")
	}
	if resp.DeploymentId != "dep-1" {
		t.Errorf("DeploymentId = %q, want %q", resp.DeploymentId, "dep-1")
	}
	// Notify channel: build_queued fired exactly once.
	if len(notif.channels) != 1 || notif.channels[0] != db.NotifyBuildQueued {
		t.Errorf("notif.channels = %v, want [%s]", notif.channels, db.NotifyBuildQueued)
	}
	// Status update: building was stamped.
	if len(store.updateStatusCalls) != 1 || store.updateStatusCalls[0] != state.DeployBuilding {
		t.Errorf("status calls = %v, want [building]", store.updateStatusCalls)
	}
}

func TestEnqueueBuild_AppNotFound(t *testing.T) {
	store := &bridgeStubStore{appErr: state.ErrNotFound}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)
	path, size := stageFixtureFile(t, g.stagingRoot, "missing/app/x", []byte("x"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "missing",
		CommitSha:   "x",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("err code = %s, want NotFound", got)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notif should not fire on app lookup failure (got %v)", notif.channels)
	}
}

func TestEnqueueBuild_AccountMismatch(t *testing.T) {
	// App exists but under a different account. IDOR-safe
	// posture: NotFound, not PermissionDenied, so a forged
	// call can't enumerate which apps belong to other accounts.
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "other", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)
	path, size := stageFixtureFile(t, g.stagingRoot, "victim/app-1/x", []byte("x"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "victim",
		AppId:       "app-1",
		CommitSha:   "x",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("err code = %s, want NotFound (IDOR-safe)", got)
	}
}

func TestEnqueueBuild_InactiveApp(t *testing.T) {
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "a", Status: state.AppEvictedCold}}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)
	path, size := stageFixtureFile(t, g.stagingRoot, "a/app-1/x", []byte("x"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "app-1",
		CommitSha:   "x",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("err code = %s, want FailedPrecondition", got)
	}
}

func TestEnqueueBuild_SourcePathNotUnderStagingRoot(t *testing.T) {
	// Finding #4: a path outside the staging root is
	// rejected with InvalidArgument. The handler uses
	// filepath.Clean + separator-anchored prefix so a
	// sibling directory like /var/lib/faas/githubd-evil
	// does NOT match the configured /var/lib/faas/githubd.
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)

	// Write a file to the real /tmp (definitely not under the
	// t.TempDir staging root).
	outsidePath := filepath.Join(t.TempDir(), "evil", "source.tar.gz")
	if mkErr := os.MkdirAll(filepath.Dir(outsidePath), 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	if err := os.WriteFile(outsidePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "app-1",
		CommitSha:   "x",
		SourcePath:  outsidePath,
		SourceUrl:   "https://x",
		SourceBytes: 1,
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("err code = %s, want InvalidArgument", got)
	}
	if !strings.Contains(err.Error(), "staging root") {
		t.Errorf("error message should mention staging root: %v", err)
	}
}

func TestEnqueueBuild_SourcePathSiblingDirectoryNotAllowed(t *testing.T) {
	// Sibling-directory attack: the staging root is
	// /var/lib/faas/githubd; an attacker writes under
	// /var/lib/faas/githubd-evil. The separator-anchored
	// prefix MUST reject this.
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)

	// Construct a synthetic stagingRoot that ends in "githubd";
	// the actual source path is staged under "<stagingRoot>-evil/...".
	// The handler reads stagingRoot from the bridge struct, so
	// we set it explicitly and place the file under a sibling.
	stagingRoot := filepath.Join(t.TempDir(), "githubd")
	spoolRoot := t.TempDir()
	g.stagingRoot = stagingRoot
	g.spoolRoot = spoolRoot

	// Write the file under the sibling.
	sibling := filepath.Join(t.TempDir(), "githubd-evil", "app-1", "abc")
	if mkErr := os.MkdirAll(sibling, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	outsidePath := filepath.Join(sibling, "source.tar.gz")
	if err := os.WriteFile(outsidePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "app-1",
		CommitSha:   "x",
		SourcePath:  outsidePath,
		SourceUrl:   "https://x",
		SourceBytes: 1,
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("sibling directory attack: err code = %s, want InvalidArgument", got)
	}
}

func TestEnqueueBuild_InvalidSuffix(t *testing.T) {
	// .tar.gz suffix is required (builderd's gzip detector
	// at pkg/builderd/detect.go:48 reads .tar.gz).
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}

	// Write a non-tar.gz file under the staging root so the
	// suffix check fires before the os.Stat check.
	badPath := filepath.Join(stagingRoot, "source.zip")
	if err := os.WriteFile(badPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "app-1",
		CommitSha:   "x",
		SourcePath:  badPath,
		SourceUrl:   "https://x",
		SourceBytes: 1,
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("err code = %s, want InvalidArgument", got)
	}
	if !strings.Contains(err.Error(), ".tar.gz") {
		t.Errorf("error should mention .tar.gz: %v", err)
	}
}

func TestEnqueueBuild_ZeroSourceBytes(t *testing.T) {
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	store := &bridgeStubStore{app: state.App{ID: "a", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}

	// Empty file UNDER the staging root. The handler's
	// 0-byte check fires before the os.Stat check.
	path := filepath.Join(stagingRoot, "source.tar.gz")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "a",
		CommitSha:   "x",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: 0,
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("err code = %s, want InvalidArgument", got)
	}
}

func TestEnqueueBuild_OverCapacitySourceBytes(t *testing.T) {
	store := &bridgeStubStore{app: state.App{ID: "a", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := newBridge(t, store, notif)
	// Stage under the staging root so the allowlist check
	// passes; the bytes-ceiling check is the next gate.
	path, size := stageFixtureFile(t, g.stagingRoot, "a/a/x", []byte("x"))

	// 2 GB + 1 — over the 2 GB ceiling. The size must be
	// declared > size_on_disk for the bytes check to pick
	// it up; the existing file covers the on-disk floor.
	_ = size
	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "a",
		CommitSha:   "x",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: (2 << 30) + 1,
	})
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("err code = %s, want ResourceExhausted", got)
	}
}

func TestEnqueueBuild_NotRegularFile(t *testing.T) {
	// Source path is a directory, not a regular file.
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	store := &bridgeStubStore{app: state.App{ID: "a", AccountID: "a", Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	dir := filepath.Join(stagingRoot, "source.tar.gz")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   "a",
		AppId:       "a",
		CommitSha:   "x",
		SourcePath:  dir,
		SourceUrl:   "https://x",
		SourceBytes: 1,
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("err code = %s, want InvalidArgument", got)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error should mention not-a-regular-file: %v", err)
	}
}

func TestEnqueueBuild_SizeMismatch(t *testing.T) {
	// Declared SourceBytes != on-disk size. The handler
	// cross-checks because builderd's detector would silently
	// read a truncated tarball; surfacing the mismatch at
	// enqueue means the dispatcher can skip + log.
	accountID := "a"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: accountID, Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, "app-1", "abc"), []byte("real"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   accountID,
		AppId:       "app-1",
		CommitSha:   "abc",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size + 1, // declared bigger than the file
	})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("err code = %s, want InvalidArgument", got)
	}
	if !strings.Contains(err.Error(), "!=") {
		t.Errorf("error should mention size mismatch: %v", err)
	}
}

func TestEnqueueBuild_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		req  *githubdpb.EnqueueBuildRequest
	}{
		{
			name: "missing account_id",
			req: &githubdpb.EnqueueBuildRequest{
				AppId:      "a",
				CommitSha:  "x",
				SourcePath: "/tmp/x",
				SourceUrl:  "https://x",
			},
		},
		{
			name: "missing app_id",
			req: &githubdpb.EnqueueBuildRequest{
				AccountId:  "a",
				CommitSha:  "x",
				SourcePath: "/tmp/x",
				SourceUrl:  "https://x",
			},
		},
		{
			name: "missing commit_sha",
			req: &githubdpb.EnqueueBuildRequest{
				AccountId:  "a",
				AppId:      "a",
				SourcePath: "/tmp/x",
				SourceUrl:  "https://x",
			},
		},
		{
			name: "missing source_path",
			req: &githubdpb.EnqueueBuildRequest{
				AccountId: "a",
				AppId:     "a",
				CommitSha: "x",
				SourceUrl: "https://x",
			},
		},
		{
			name: "missing source_url",
			req: &githubdpb.EnqueueBuildRequest{
				AccountId:  "a",
				AppId:      "a",
				CommitSha:  "x",
				SourcePath: "/tmp/x",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &bridgeStubStore{}
			notif := &bridgeStubNotifier{}
			g := newBridge(t, store, notif)
			_, err := g.EnqueueBuild(context.Background(), tc.req)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Errorf("err code = %s, want InvalidArgument", got)
			}
		})
	}
}

func TestEnqueueBuild_NilRequest(t *testing.T) {
	g := newBridge(t, &bridgeStubStore{}, &bridgeStubNotifier{})
	_, err := g.EnqueueBuild(context.Background(), nil)
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("nil req: err code = %s, want InvalidArgument", got)
	}
}

func TestEnqueueBuild_CreateDeploymentError_MapsToGRPC(t *testing.T) {
	// The store wraps a *api.Problem with a known Code; the
	// handler must map it via pkg/grpcerr.ToStatus. Use
	// api.CodeResourceExhausted → ResourceExhausted.
	prob := &api.Problem{
		Code:   api.CodeSourceTooLarge,
		Title:  "tarball too large for plan",
		Detail: "tarball too large for plan",
	}
	store := &bridgeStubStore{
		app:                 state.App{ID: "a", AccountID: "a", Status: state.AppActive, RAMMB: 256},
		createDeploymentErr: prob,
	}

	accountID := "a"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, "a", "abc"), []byte("payload"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   accountID,
		AppId:       "a",
		CommitSha:   "abc",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("err code = %s, want ResourceExhausted (via pkg/grpcerr)", got)
	}
}

func TestEnqueueBuild_CreateDeploymentMappedError(t *testing.T) {
	// Non-Problem error → codes.Internal. The receiver's
	// asGRPC fallback path is exercised when the wrapped
	// error doesn't unwrap to a *api.Problem.
	store := &bridgeStubStore{
		app:                 state.App{ID: "a", AccountID: "a", Status: state.AppActive},
		createDeploymentErr: errors.New("postgres: connection refused"),
	}
	accountID := "a"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, "a", "abc"), []byte("payload"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   accountID,
		AppId:       "a",
		CommitSha:   "abc",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("non-Problem error: err code = %s, want Internal", got)
	}
}

func TestEnqueueBuild_PrevDeploymentSuperseded(t *testing.T) {
	// When a prior non-terminal deployment exists, the
	// receiver emits a NotifyDeploymentChanged with
	// status=superseded. First deploy: no supersede notify.
	accountID := "a"
	appID := "app-1"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	store := &bridgeStubStore{
		app:  state.App{ID: appID, AccountID: accountID, Status: state.AppActive},
		prev: state.Deployment{ID: "dep-prev", AppID: appID},
	}
	notif := &bridgeStubNotifier{}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, appID, "abc"), []byte("payload"))

	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   accountID,
		AppId:       appID,
		CommitSha:   "abc",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if err != nil {
		t.Fatalf("EnqueueBuild: %v", err)
	}

	// Two notify channels: build_queued + deployment_changed.
	if len(notif.channels) != 2 {
		t.Fatalf("notif.channels = %v, want 2", notif.channels)
	}
	if notif.channels[0] != db.NotifyBuildQueued {
		t.Errorf("first channel = %s, want %s", notif.channels[0], db.NotifyBuildQueued)
	}
	if notif.channels[1] != db.NotifyDeploymentChanged {
		t.Errorf("second channel = %s, want %s", notif.channels[1], db.NotifyDeploymentChanged)
	}
}

func TestEnqueueBuild_NotifyFailureIsBestEffort(t *testing.T) {
	// Notify failure on the build_queued channel MUST NOT
	// propagate the build row write succeeded; the durable
	// recovery net (pkg/state/pgstore.go:2386) is the safety
	// net. The handler logs + returns success.
	accountID := "a"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()
	store := &bridgeStubStore{app: state.App{ID: "a", AccountID: accountID, Status: state.AppActive}}
	notif := &bridgeStubNotifier{failErr: errors.New("pg_notify failed")}
	g := &githubdBridge{
		store: store, notif: notif, log: discLog(), ops: wire.NewOpsMetrics("apid"),
		spool: spoolRoot, stagingRoot: stagingRoot, spoolRoot: spoolRoot,
	}
	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, "a", "abc"), []byte("payload"))

	resp, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:   accountID,
		AppId:       "a",
		CommitSha:   "abc",
		SourcePath:  path,
		SourceUrl:   "https://x",
		SourceBytes: size,
	})
	if err != nil {
		t.Fatalf("EnqueueBuild on notify failure: %v", err)
	}
	if resp == nil || resp.BuildId == "" {
		t.Errorf("response should carry the build_id even when notify failed: %+v", resp)
	}
}

// TestEnqueueBuild_AnnotationsThreaded (issue #977 / ADR-116) pins the
// bridge's responsibility for translating the proto's
// (PullRequestNumber, SenderLogin, EventKind) fields onto the
// deployment row's annotation columns (PRNumber, DeployedBy, Kind).
// The bridge is the canonical seam between the githubd push webhook
// and the deployment row writer — a regression here would silently
// drop the annotation fields whenever a webhook fires. The bridge
// prefers SenderLogin over Pusher (the actor who opened the webhook
// vs. the commit author) for the deployed_by field.
//
// Drives the same path as TestEnqueueBuild_HappyPath but with the
// three new proto fields populated, then asserts the captured
// Deployment row carries the right values.
func TestEnqueueBuild_AnnotationsThreaded(t *testing.T) {
	accountID := "acct-1"
	appID := "app-1"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()

	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, appID, "abc123"), []byte("tiny-tar"))

	store := &bridgeStubStore{app: state.App{ID: appID, AccountID: accountID, Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	ops := wire.NewOpsMetrics("apid")
	g := &githubdBridge{
		store:       store,
		notif:       notif,
		log:         discLog(),
		ops:         ops,
		spool:       spoolRoot,
		stagingRoot: stagingRoot,
		spoolRoot:   spoolRoot,
	}

	resp, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:         accountID,
		AppId:             appID,
		CommitSha:         "abc123",
		SourcePath:        path,
		SourceUrl:         "https://codeload.example.com/repo/tar.gz/abc123",
		SourceBytes:       size,
		RepoFullName:      "owner/repo",
		Branch:            "feature/ann",
		Ref:               "refs/heads/feature/ann",
		Pusher:            "octocat", // commit author; lower priority than SenderLogin
		PullRequestNumber: 4242,
		SenderLogin:       "alice", // actor who opened the PR — takes precedence
		EventKind:         githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST,
	})
	if err != nil {
		t.Fatalf("EnqueueBuild: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}

	// Capture the populated Deployment row from the stub store.
	created := store.createDeploymentReturned
	if created.PRNumber != 4242 {
		t.Errorf("dep.PRNumber = %d, want 4242 (PullRequestNumber not threaded)", created.PRNumber)
	}
	if created.DeployedBy != "alice" {
		t.Errorf("dep.DeployedBy = %q, want %q (SenderLogin takes precedence over Pusher)", created.DeployedBy, "alice")
	}
	// Kind is derived from EventKind: pull_request → preview_deploy.
	if created.Kind != state.DeploymentKindPreview {
		t.Errorf("dep.Kind = %q, want %q (EventKind=pull_request → preview)", created.Kind, state.DeploymentKindPreview)
	}
}

// TestEnqueueBuild_AnnotationsFallbackToPusher (issue #977 / ADR-116)
// pins the SenderLogin-missing fallback: when the proto omits
// SenderLogin (the push-event path), the bridge must use Pusher
// (the commit author) as the deployed_by label. Without this
// fallback, push-event deploys would render an empty DeployedBy
// chip on the dashboard even though the data is in the wire.
func TestEnqueueBuild_AnnotationsFallbackToPusher(t *testing.T) {
	accountID := "acct-1"
	appID := "app-1"
	stagingRoot := t.TempDir()
	spoolRoot := t.TempDir()

	path, size := stageFixtureFile(t, stagingRoot, filepath.Join(accountID, appID, "abc123"), []byte("tiny-tar"))

	store := &bridgeStubStore{app: state.App{ID: appID, AccountID: accountID, Status: state.AppActive}}
	notif := &bridgeStubNotifier{}
	ops := wire.NewOpsMetrics("apid")
	g := &githubdBridge{
		store:       store,
		notif:       notif,
		log:         discLog(),
		ops:         ops,
		spool:       spoolRoot,
		stagingRoot: stagingRoot,
		spoolRoot:   spoolRoot,
	}

	// No SenderLogin, no PullRequestNumber — push-event shape.
	_, err := g.EnqueueBuild(context.Background(), &githubdpb.EnqueueBuildRequest{
		AccountId:    accountID,
		AppId:        appID,
		CommitSha:    "abc123",
		SourcePath:   path,
		SourceUrl:    "https://codeload.example.com/repo/tar.gz/abc123",
		SourceBytes:  size,
		RepoFullName: "owner/repo",
		Branch:       "main",
		Ref:          "refs/heads/main",
		Pusher:       "octocat",
		EventKind:    githubdpb.EnqueueBuildEventKind_EVENT_KIND_PUSH,
	})
	if err != nil {
		t.Fatalf("EnqueueBuild: %v", err)
	}
	created := store.createDeploymentReturned
	if created.DeployedBy != "octocat" {
		t.Errorf("dep.DeployedBy = %q, want %q (Pusher fallback for push events)", created.DeployedBy, "octocat")
	}
	if created.PRNumber != 0 {
		t.Errorf("dep.PRNumber = %d, want 0 (push events leave PRNumber NULL)", created.PRNumber)
	}
	if created.Kind != state.DeploymentKindGitHub {
		t.Errorf("dep.Kind = %q, want %q (push → github)", created.Kind, state.DeploymentKindGitHub)
	}
}
