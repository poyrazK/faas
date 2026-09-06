// apidsource_test.go — unit tests for the shared deploy+build flow.
//
// These tests pin the helper's behaviour against the MemStore. The
// full Postgres path is exercised by the e2e suite
// (cmd/e2e/apply_project_builds_e2e_test.go). MemStore is sufficient
// here because the helper's contract is about (a) the call
// ordering against Store, (b) the notification payloads, and (c)
// the success / error mapping — all of which are state-engine
// neutral.
//
// The Notifier is a hand-rolled stub that records Notify calls so
// the tests can assert on the payload shape and ordering. A real
// pg_notify is unnecessary at this layer; pkg/db's notify_test.go
// covers the channel/payload round-trip.
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
)

// recordingNotifier records every Notify call so tests can assert
// on payload + channel. The slice is mutex-guarded because the
// production helper is single-goroutine, but tests for partial-
// failure paths (Phase 5 PR-A) will fan-out.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
}

type notifyCall struct {
	channel string
	payload string
}

func (r *recordingNotifier) Notify(_ context.Context, channel, payload string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, notifyCall{channel: channel, payload: payload})
	return nil
}

func (r *recordingNotifier) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingNotifier) lastPayload() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return "", errors.New("no notify calls recorded")
	}
	return r.calls[len(r.calls)-1].payload, nil
}

// errNotifier always returns an error so the helper's best-effort
// path is exercised without leaking a real pg_notify failure.
type errNotifier struct{ err error }

func (e errNotifier) Notify(_ context.Context, _, _ string) error { return e.err }

// quietLogger returns a slog.Logger that swallows everything; the
// helper's logs are not asserted in unit tests.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mustSeedApp creates an account + active app so MemStore.CreateDeployment
// has something to attach to. Returns (app, cleanup).
func mustSeedApp(t *testing.T, st *state.MemStore) state.App {
	t.Helper()
	acct, err := st.CreateAccount(context.Background(), "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := st.CreateApp(context.Background(), state.App{
		AccountID:     acct.ID,
		Slug:          "test-app",
		RootDir:       ".",
		WorkloadClass: state.WorkloadClassHTTP,
		Type:          state.AppTypeApp,
		Status:        state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app
}

// stageSource writes a dummy tarball-sized file under dir and returns
// the (path, bytes) pair so the helper can record it as the source.
func stageSource(t *testing.T, dir string) (string, int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "source.tar.gz")
	if err := os.WriteFile(p, []byte("fake-tarball-bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return p, fi.Size()
}

func TestEnqueue_HappyPath_FirstDeploy(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()

	// Stage a fake source tarball.
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if res.DeploymentID == "" || res.BuildID == "" {
		t.Fatalf("expected non-empty ids, got %+v", res)
	}

	// First deploy: only the build_queued notify fires, no
	// supersede (no prev).
	if got := notif.callCount(); got != 1 {
		t.Fatalf("notify calls: got %d want 1 (no prev → no supersede)", got)
	}
	payload, _ := notif.lastPayload()
	if !strings.Contains(payload, `"build":"`+res.BuildID+`"`) {
		t.Fatalf("payload missing build id: %s", payload)
	}
	if !strings.Contains(payload, `"source":"tarball"`) {
		t.Fatalf("payload missing source=tarball: %s", payload)
	}
	// Build.log file landed under <LogSpool>/<deployment_id>/.
	logPath := filepath.Join(spoolDir, res.DeploymentID, "build.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("build.log not staged: %v", err)
	}
}

func TestEnqueue_GithubDeliveryRetryReturnsExistingWork(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	srcPath, srcBytes := stageSource(t, t.TempDir())
	params := EnqueueParams{
		AppID:       app.ID,
		DeliveryID:  "f7fd51ee-e5e7-4a2e-a30c-c111fa02dc6f",
		Kind:        state.DeploymentKindGitHub,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		LogSpool:    t.TempDir(),
		Log:         quietLogger(),
	}

	first, err := Enqueue(context.Background(), st, notif, params)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	second, err := Enqueue(context.Background(), st, notif, params)
	if err != nil {
		t.Fatalf("retry Enqueue: %v", err)
	}
	if second != first {
		t.Fatalf("retry created different work: first=%+v second=%+v", first, second)
	}
	build, err := st.BuildByID(context.Background(), first.BuildID)
	if err != nil || build.DeploymentID != first.DeploymentID {
		t.Fatalf("durable build mismatch: build=%+v err=%v", build, err)
	}
}

func TestEnqueue_HappyPath_SecondDeploy_FiresSupersede(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	// First deploy — produces a prev for the second.
	first, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	// Second deploy against the same app.
	notif.mu.Lock()
	notif.calls = nil
	notif.mu.Unlock()
	second, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}

	// Two notifies this time: build_queued + deployment_changed.
	if got := notif.callCount(); got != 2 {
		t.Fatalf("notify calls: got %d want 2 (build_queued + supersede)", got)
	}
	notif.mu.Lock()
	sup := notif.calls[1]
	notif.mu.Unlock()
	if sup.channel != db.NotifyDeploymentChanged {
		t.Fatalf("supersede channel: got %q want %q", sup.channel, db.NotifyDeploymentChanged)
	}
	if !strings.Contains(sup.payload, `"status":"superseded"`) {
		t.Fatalf("supersede payload: %s", sup.payload)
	}
	if !strings.Contains(sup.payload, `"deployment_id":"`+first.DeploymentID+`"`) {
		t.Fatalf("supersede payload missing prev deployment id: %s", sup.payload)
	}
	if second.DeploymentID == first.DeploymentID {
		t.Fatalf("expected new deployment id, got duplicate %q", second.DeploymentID)
	}
}

func TestEnqueue_NotifyFailureIsBestEffort(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), st, errNotifier{err: errors.New("pg_notify unavailable")}, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue must not bubble up notify failure: %v", err)
	}
	if res.DeploymentID == "" {
		t.Fatalf("deployment row must still be created on notify failure")
	}
}

func TestEnqueue_BuildIDIsDurable(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// MemStore build rows are not directly readable, but the
	// build.log file the helper stages IS the durable handle
	// builderd uses — and its on-disk path mirrors what the
	// build row's log_path column holds. Verifying that file
	// exists + the returned BuildID is non-empty is enough to
	// pin "the build row was created and its log_path was
	// stamped".
	if res.BuildID == "" {
		t.Fatalf("BuildID empty — MemStore.CreateBuild must have failed")
	}
	logPath := filepath.Join(spoolDir, res.DeploymentID, "build.log")
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("build.log not staged at %s: %v", logPath, err)
	}
	if fi.Size() != 0 {
		t.Fatalf("build.log seeded non-empty (size=%d) — builderd appends", fi.Size())
	}
}

func TestEnqueue_RequiredFields(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	cases := []struct {
		name    string
		mutate  func(p *EnqueueParams)
		wantErr string
	}{
		{
			name:    "missing log",
			mutate:  func(p *EnqueueParams) { p.Log = nil },
			wantErr: "log is required",
		},
		{
			name:    "missing log spool",
			mutate:  func(p *EnqueueParams) { p.LogSpool = "" },
			wantErr: "LogSpool is required",
		},
		{
			name:    "missing app id",
			mutate:  func(p *EnqueueParams) { p.AppID = "" },
			wantErr: "AppID is required",
		},
		{
			name:    "missing kind",
			mutate:  func(p *EnqueueParams) { p.Kind = "" },
			wantErr: "Kind is required",
		},
		{
			name:    "missing source path",
			mutate:  func(p *EnqueueParams) { p.SourcePath = "" },
			wantErr: "SourcePath is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := EnqueueParams{
				AppID:       app.ID,
				Kind:        state.DeploymentKindTarball,
				SourcePath:  srcPath,
				SourceBytes: srcBytes,
				Source:      "tarball",
				LogSpool:    spoolDir,
				Log:         quietLogger(),
			}
			tc.mutate(&p)
			_, err := Enqueue(context.Background(), st, &recordingNotifier{}, p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error: got %q want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestEnqueue_PayloadShape(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindGitHub,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		SourceURL:   "https://github.com/o/r/archive/sha.tar.gz",
		CommitSHA:   "deadbeef",
		Source:      "github",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	notif.mu.Lock()
	payload := notif.calls[0].payload
	notif.mu.Unlock()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not JSON: %v (%s)", err, payload)
	}
	for _, key := range []string{"build", "deployment", "app", "kind", "source"} {
		if _, ok := parsed[key]; !ok {
			t.Fatalf("payload missing %q: %v", key, parsed)
		}
	}
	if parsed["source"] != "github" {
		t.Fatalf("source: got %v want github", parsed["source"])
	}
	if parsed["build"] != res.BuildID {
		t.Fatalf("build in payload: got %v want %s", parsed["build"], res.BuildID)
	}
}

func TestEnqueue_BuildLogSpoolExists(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	spoolDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath, srcBytes := stageSource(t, srcDir)

	res, err := Enqueue(context.Background(), st, notif, EnqueueParams{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  srcPath,
		SourceBytes: srcBytes,
		Source:      "tarball",
		LogSpool:    spoolDir,
		Log:         quietLogger(),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Log spool path matches the apid-side convention.
	expected := filepath.Join(spoolDir, res.DeploymentID, "build.log")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("build.log not at %s: %v", expected, err)
	}
}
