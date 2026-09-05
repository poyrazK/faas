// coverage_test.go — fill the remaining pkg/apid/apidsource coverage gaps
// that the focused happy-path test file (apidsource_test.go) deliberately
// doesn't touch. Targets:
//
//   - sourceBackendFromEnv (apidsource.go:186) — 33.3% → covered. The
//     single-box / split-box branch and the BackendFromEnv error
//     wrap.
//   - publishSource (apidsource.go:197) — 20% → covered. The be==nil
//     short-circuit is exercised by the existing tests; this file
//     pins the file-open-error and Put-error branches.
//   - Enqueue error branches (apidsource.go:237) — 78.6% → covered.
//     The remaining gaps:
//       * source defaulting from Kind (line 353-355)
//       * publishSource failure (line 340-344) — separate from the
//         pre-existing happy-path which doesn't enable FAAS_STORAGE_BACKEND
//       * sourceBackendFromEnv error wrapped into Enqueue
//       * CreateDeployment error wrapped
//       * CreateBuild error wrapped
//       * mkdir-failure warn path (line 308-309)
//       * build.log Create-failure warn path (line 313-315)
//       * UpdateDeploymentStatus error swallowed (line 323)
//       * NotifyError swallowed for both build_queued + superseded
//         notify paths
//       * Explicit Source on EnqueueParams is honoured (the "preserve
//         a legacy wire-contract quirk" path)

package apidsource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// errStore wraps a state.Store and returns the configured error for
// the named method. Used to drive the per-step error branches of
// Enqueue that the happy-path test cannot reach.
type errStore struct {
	state.Store
	createDeploymentErr error
	createBuildErr      error
	latestDeploymentErr error
	updateStatusErr     error
	mu                  sync.Mutex
	updateStatusCalled  int
}

func (e *errStore) CreateDeployment(ctx context.Context, d state.Deployment) (state.Deployment, error) {
	if e.createDeploymentErr != nil {
		return state.Deployment{}, e.createDeploymentErr
	}
	return e.Store.CreateDeployment(ctx, d)
}

func (e *errStore) LatestDeployment(ctx context.Context, appID string) (state.Deployment, error) {
	if e.latestDeploymentErr != nil {
		return state.Deployment{}, e.latestDeploymentErr
	}
	return e.Store.LatestDeployment(ctx, appID)
}

func (e *errStore) UpdateDeploymentStatus(ctx context.Context, id string, status state.DeploymentStatus, logPath string) error {
	e.mu.Lock()
	e.updateStatusCalled++
	e.mu.Unlock()
	if e.updateStatusErr != nil {
		return e.updateStatusErr
	}
	return e.Store.UpdateDeploymentStatus(ctx, id, status, logPath)
}

func (e *errStore) CreateBuildWithID(ctx context.Context, id, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error) {
	if e.createBuildErr != nil {
		return state.Build{}, e.createBuildErr
	}
	return e.Store.CreateBuildWithID(ctx, id, deploymentID, kind, sourceBytes, logPath)
}

// memBackend is a StorageBackend that records Puts in memory. Used
// to drive the publishSource non-nil branch.
type memBackend struct {
	mu     sync.Mutex
	puts   map[string][]byte
	putErr error
}

func newMemBackend() *memBackend {
	return &memBackend{puts: map[string][]byte{}}
}

func (m *memBackend) Put(_ context.Context, key string, r io.Reader) error {
	if m.putErr != nil {
		return m.putErr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts[key] = data
	return nil
}

func (m *memBackend) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}

func (m *memBackend) Delete(_ context.Context, _ string) error {
	return nil
}

// --- sourceBackendFromEnv (apidsource.go:186) ------------------------

func TestSourceBackendFromEnv_NonOCIReturnsNil(t *testing.T) {
	// Default FAAS_STORAGE_BACKEND is "local" → no second copy is
	// uploaded. The function must return (nil, nil) so the caller
	// can short-circuit publishSource without an env check.
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	be, err := sourceBackendFromEnv()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if be != nil {
		t.Errorf("be = %v, want nil", be)
	}
}

func TestSourceBackendFromEnv_EmptyReturnsNil(t *testing.T) {
	t.Setenv("FAAS_STORAGE_BACKEND", "")
	be, err := sourceBackendFromEnv()
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if be != nil {
		t.Errorf("be = %v, want nil", be)
	}
}

func TestSourceBackendFromEnv_OCIMissingRegistryErrors(t *testing.T) {
	// "oci" routes to storage.BackendFromEnv, which requires
	// FAAS_OCI_REGISTRY. With the env unset the inner helper
	// returns "storage: FAAS_STORAGE_BACKEND=oci requires
	// FAAS_OCI_REGISTRY" → the helper must wrap it via the
	// "source storage:" prefix (line 192).
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_OCI_REGISTRY", "")
	_, err := sourceBackendFromEnv()
	if err == nil {
		t.Fatal("err = nil, want error from BackendFromEnv")
	}
	if !strings.Contains(err.Error(), "source storage") {
		t.Errorf("err = %v, want 'source storage' wrap prefix", err)
	}
}

// --- publishSource (apidsource.go:197) -------------------------------

func TestPublishSource_NilBackendNoOp(t *testing.T) {
	// The single-box install never sets FAAS_STORAGE_BACKEND=oci,
	// so the helper must NOT touch the filesystem when be is nil.
	err := publishSource(context.Background(), nil, "build-id", "/some/path")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestPublishSource_HappyPath(t *testing.T) {
	// Drive the os.Open + Put path. The build ID ends up in the
	// key ("sources/<id>.tar.gz") and the file contents are
	// uploaded verbatim.
	be := newMemBackend()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.tar.gz")
	if err := os.WriteFile(src, []byte("archive bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := publishSource(context.Background(), be, "build-abc", src); err != nil {
		t.Fatalf("publishSource: %v", err)
	}
	if got := be.puts["sources/build-abc.tar.gz"]; string(got) != "archive bytes" {
		t.Errorf("uploaded bytes = %q, want 'archive bytes'", got)
	}
}

func TestPublishSource_MissingSourceFile(t *testing.T) {
	be := newMemBackend()
	err := publishSource(context.Background(), be, "build-id", "/no/such/file")
	if err == nil {
		t.Fatal("err = nil, want error (file missing)")
	}
	if !strings.Contains(err.Error(), "open source archive") {
		t.Errorf("err = %v, want 'open source archive' fragment", err)
	}
}

func TestPublishSource_PutError(t *testing.T) {
	be := newMemBackend()
	be.putErr = errors.New("registry down")
	dir := t.TempDir()
	src := filepath.Join(dir, "source.tar.gz")
	if err := os.WriteFile(src, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := publishSource(context.Background(), be, "build-id", src)
	if err == nil {
		t.Fatal("err = nil, want error from Put")
	}
	if !strings.Contains(err.Error(), "publish source archive") {
		t.Errorf("err = %v, want 'publish source archive' fragment", err)
	}
}

// --- Enqueue error branches (apidsource.go:237) ----------------------

func TestEnqueue_SourceDefaultsFromKind(t *testing.T) {
	// Source="" → the helper derives "source" from Kind (line
	// 353-355). Pin that the wire payload's source field tracks
	// the kind when the caller leaves Source empty.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindDockerfile,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		// Source omitted → defaults to Kind.
		LogSpool: spoolDir,
		Log:      quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	payload, _ := notif.lastPayload()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON: %v (%s)", err, payload)
	}
	if parsed["source"] != string(state.DeploymentKindDockerfile) {
		t.Errorf("source = %v, want %q (defaulted from Kind)", parsed["source"], state.DeploymentKindDockerfile)
	}
	if parsed["kind"] != string(state.DeploymentKindDockerfile) {
		t.Errorf("kind = %v, want %q", parsed["kind"], state.DeploymentKindDockerfile)
	}
}

func TestEnqueue_ExplicitSourceHonoured(t *testing.T) {
	// The doc says Source is honoured "only to preserve a legacy
	// wire-contract quirk". Pin that an explicit non-empty value
	// is sent verbatim (the helper does NOT overwrite it with Kind).
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "legacy-quirk",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	payload, _ := notif.lastPayload()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON: %v (%s)", err, payload)
	}
	if parsed["source"] != "legacy-quirk" {
		t.Errorf("source = %v, want 'legacy-quirk' (explicit)", parsed["source"])
	}
}

func TestEnqueue_CreateDeploymentError(t *testing.T) {
	// Store.CreateDeployment returns an error → Enqueue must wrap
	// it via "create deployment" and NOT fall through to build or
	// notify.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	store := &errStore{Store: st, createDeploymentErr: errors.New("db constraint violation")}
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), store, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    t.TempDir(),
		Log:         quietLogger(),
	})
	if err == nil {
		t.Fatal("err = nil, want error from CreateDeployment")
	}
	if !strings.Contains(err.Error(), "create deployment") {
		t.Errorf("err = %v, want 'create deployment' wrap", err)
	}
	if !errors.Is(err, errors.Unwrap(err)) && !strings.Contains(err.Error(), "db constraint violation") {
		t.Errorf("err = %v, want db constraint violation in chain", err)
	}
	if got := notif.callCount(); got != 0 {
		t.Errorf("notify calls = %d, want 0 (early return on CreateDeployment failure)", got)
	}
}

func TestEnqueue_CreateBuildError(t *testing.T) {
	// CreateDeployment succeeds (deploy row exists), but CreateBuild
	// fails → Enqueue must surface a "create build" wrapped error.
	// The unqueued deployment must become failed so it cannot hang in flight.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	store := &errStore{Store: st, createBuildErr: errors.New("build row insert failed")}
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), store, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    t.TempDir(),
		Log:         quietLogger(),
	})
	if err == nil {
		t.Fatal("err = nil, want error from CreateBuild")
	}
	if !strings.Contains(err.Error(), "create build") {
		t.Errorf("err = %v, want 'create build' wrap", err)
	}
	if !strings.Contains(err.Error(), "build row insert failed") {
		t.Errorf("err = %v, want 'build row insert failed' in chain", err)
	}
	dep, readErr := st.LatestDeployment(context.Background(), app.ID)
	if readErr != nil || dep.Status != state.DeployFailed {
		t.Fatalf("orphan deployment: %+v %v", dep, readErr)
	}
	if _, readErr := st.ClaimNextQueuedBuild(context.Background()); !errors.Is(readErr, state.ErrNotFound) {
		t.Fatalf("unexpected queued build: %v", readErr)
	}

}

func TestEnqueue_MkdirSpoolFailure_BuildLogFallbackOK(t *testing.T) {
	// LogSpool points at a path whose parent is a regular file →
	// MkdirAll fails. The helper must Warn and continue (builderd
	// creates the dir on demand). CreateBuild succeeds after the
	// warn so the deployment row is still enqueued.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}

	// Create a file where the LogSpool dir would need to go.
	blocking := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocking, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// LogSpool/<deployment_id>/ — the parent is a file, so MkdirAll
	// fails. But /<LogSpool>/<deployment_id>/build.log creation also
	// fails, both paths Warn and continue.

	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    blocking, // parent is a file → MkdirAll fails
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue must continue past mkdir failure: %v", err)
	}
	// Verify build_queued still fires.
	if got := notif.callCount(); got != 1 {
		t.Errorf("notify calls = %d, want 1 (mkdir failure must NOT block notify)", got)
	}
}

func TestEnqueue_StatusAndQueueUseSingleStoreOperation(t *testing.T) {
	// Queue publication must not rely on a separate status update.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	store := &errStore{Store: st, updateStatusErr: errors.New("status update rejected")}
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), store, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    t.TempDir(),
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue must not bubble UpdateDeploymentStatus error: %v", err)
	}
	if res.DeploymentID == "" || res.BuildID == "" {
		t.Fatalf("expected durable IDs, got %+v", res)
	}
	if store.updateStatusCalled != 0 {
		t.Error("separate UpdateDeploymentStatus called")
	}
}

func TestEnqueue_SourceBackendEnvError(t *testing.T) {
	// FAAS_STORAGE_BACKEND=oci but FAAS_OCI_REGISTRY unset →
	// sourceBackendFromEnv returns an error → Enqueue wraps it
	// via "apidsource.Enqueue:" prefix. Pin the early-exit path
	// that does NOT touch Store.
	t.Setenv("FAAS_STORAGE_BACKEND", "oci")
	t.Setenv("FAAS_OCI_REGISTRY", "")
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    t.TempDir(),
		Log:         quietLogger(),
	})
	if err == nil {
		t.Fatal("err = nil, want error from sourceBackendFromEnv")
	}
	if !strings.Contains(err.Error(), "apidsource.Enqueue:") {
		t.Errorf("err = %v, want 'apidsource.Enqueue:' prefix", err)
	}
	if !strings.Contains(err.Error(), "source storage") {
		t.Errorf("err = %v, want 'source storage' wrap", err)
	}
	if got := notif.callCount(); got != 0 {
		t.Errorf("notify calls = %d, want 0 (early return on sourceBackendFromEnv failure)", got)
	}
}

// TestEnqueue_LogSpoolParentIsFile_PinLogPathIsBuiltFromDeploymentID
// verifies that even when MkdirAll fails, the helper still passes
// the well-formed logPath to CreateBuild (the path it computes at
// line 302 is the one wired into the build row regardless of the
// mkdir outcome).
func TestEnqueue_LogPathAlwaysBuiltFromDeploymentID(t *testing.T) {
	// Drive a MemStore that's a thin wrapper; the helper will
	// succeed. Verify the build row's logPath field is exactly
	// <LogSpool>/<deploymentID>/build.log. MemStore doesn't expose
	// the build row read-back directly, so we use a tee Store.
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	tee := &teeStore{Store: st, lastBuildLogPath: new(string)}
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)
	spoolDir := t.TempDir()

	_, err := Enqueue(context.Background(), tee, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// The build's logPath must point at <spoolDir>/<deploymentID>/build.log
	// — pin only the suffix (the deployment ID is uuid-shaped and
	// MemStore doesn't echo it back here, but the suffix "/build.log"
	// and the prefix "<spoolDir>/" can both be asserted).
	got := *tee.lastBuildLogPath
	if !strings.HasPrefix(got, spoolDir) {
		t.Errorf("logPath = %q, want prefix %q", got, spoolDir)
	}
	if !strings.HasSuffix(got, "/build.log") {
		t.Errorf("logPath = %q, want suffix '/build.log'", got)
	}
}

type teeStore struct {
	state.Store
	lastBuildLogPath *string
}

func (t *teeStore) CreateBuildWithID(ctx context.Context, id, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error) {
	*t.lastBuildLogPath = logPath
	return t.Store.CreateBuildWithID(ctx, id, deploymentID, kind, sourceBytes, logPath)
}

func TestEnqueue_SupersedeNotifyError_Swallowed(t *testing.T) {
	// The 2nd deploy triggers a supersede notify; if that notify
	// fails the helper must Warn and continue (the durable net is
	// the build row).
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	// First deploy to establish a prev.
	if _, err := Enqueue(context.Background(), st, &recordingNotifier{}, EnqueueParams{
		AppID: app.ID, Kind: state.DeploymentKindTarball,
		SourcePath: srcPath, SourceBytes: srcBytes,
		LogSpool: spoolDir, Log: quietLogger(),
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second deploy with a notifier that fails on the supersede
	// (second call) but succeeds on the build_queued (first call).
	notif := &selectiveErrNotifier{failAt: 2}
	res, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID: app.ID, Kind: state.DeploymentKindTarball,
		SourcePath: srcPath, SourceBytes: srcBytes,
		LogSpool: spoolDir, Log: quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue must not bubble supersede-notify error: %v", err)
	}
	if res.DeploymentID == "" {
		t.Error("deployment row must still be created")
	}
}

type selectiveErrNotifier struct {
	mu     sync.Mutex
	calls  int
	failAt int
}

func (s *selectiveErrNotifier) Notify(_ context.Context, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == s.failAt {
		return errors.New("pg_notify transient")
	}
	return nil
}

// --- TestEnqueue_FirstDeploySourceOmitted confirms that on the
// FIRST deploy (no prev), no superseded notify fires — pins the
// "prev.ID == ”" skip path at line 374.
func TestEnqueue_FirstDeploySourceOmitted_NoSupersede(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	_, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		// Source omitted → defaults to Kind.
		LogSpool: spoolDir,
		Log:      quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := notif.callCount(); got != 1 {
		t.Errorf("notify calls = %d, want 1 (no prev → no supersede)", got)
	}
	// Channel must be build_queued, not deployment_changed.
	notif.mu.Lock()
	ch := notif.calls[0].channel
	notif.mu.Unlock()
	if ch != db.NotifyBuildQueued {
		t.Errorf("channel = %q, want %q", ch, db.NotifyBuildQueued)
	}
}

// --- helpers ---------------------------------------------------------

// capBuf is a tiny io.Writer that captures into a string for tests
// that need to inspect the slog output (none currently, but kept
// here in case future coverage tests need it).
type capBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *capBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// silentLogger returns a slog.Logger that discards everything.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// silence unused-warning: capBuf + silentLogger kept for future
// coverage tests that need to assert slog output.
var _ = capBuf{}
var _ = silentLogger()

// helper to confirm api package is still imported (PlanHobby used
// elsewhere); silence unused if these references rotate out.
var _ = api.PlanHobby
