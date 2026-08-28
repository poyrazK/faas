// issue #517 / PR-B (AC3 + AC4) whitebox tests for Engine.StreamAppLogs.
// The handler fan-out has two new PR-B behaviours that warrant direct
// coverage at the engine seam (the gRPC handler is covered by the
// bufconn tests in pkg/scheddgrpc):
//
//  1. The deploymentID filter scopes the per-instance goroutine
//     fan-out: instances whose DeploymentID is set and disagrees
//     with the caller's filter are skipped. Instances with an
//     empty DeploymentID match any non-empty filter (forward-compat
//     with legacy rows that pre-date the M7 column).
//
//  2. Gap frames emitted by vmmdgrpc.Logs are forwarded through
//     the schedd fan-out unchanged, with GapReason populated from
//     the vmmd producer's labelling.
//
// The seam is a programmable per-instance Logs fake that records
// every dial + emits a configurable stream of line/gap frames.
// The sink collects every LogFrame the engine emits; the test
// asserts on the instance-id set + frame mix.

package sched

import (
	"context"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deploymentFilterFakeVMM records every (nodeID, instanceID) the
// engine opens a Logs RPC against, plus the sinceSeq /
// sinceWrittenAt args. The per-instance LogStream is supplied by
// the test via perInstanceStream[nodeID+"/"+instanceID] so a test
// can deliver gap frames + line frames on demand.
//
// All non-Logs RoutedVMM methods no-op so newEngine() is happy.
type deploymentFilterFakeVMM struct {
	mu                sync.Mutex
	dials             []recordedDial
	perInstanceStream map[string]LogStream
}

type recordedDial struct {
	NodeID         string
	InstanceID     string
	SinceSeq       int64
	SinceWrittenAt time.Time
}

func (r *deploymentFilterFakeVMM) Logs(ctx context.Context, nodeID, instanceID string, sinceSeq int64, sinceWrittenAt time.Time) (LogStream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dials = append(r.dials, recordedDial{
		NodeID:         nodeID,
		InstanceID:     instanceID,
		SinceSeq:       sinceSeq,
		SinceWrittenAt: sinceWrittenAt,
	})
	if r.perInstanceStream == nil {
		return &fakeLogStream{}, nil
	}
	if s, ok := r.perInstanceStream[nodeID+"/"+instanceID]; ok {
		return s, nil
	}
	return &fakeLogStream{}, nil
}

func (r *deploymentFilterFakeVMM) dialed() []recordedDial {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedDial, len(r.dials))
	copy(out, r.dials)
	return out
}

func (r *deploymentFilterFakeVMM) CreateColdBoot(context.Context, string, string, AppSpec) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (r *deploymentFilterFakeVMM) CreateFromSnapshot(context.Context, string, string, AppSpec, SnapshotRef) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (r *deploymentFilterFakeVMM) PauseAndSnapshot(context.Context, string, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the logs-filter
// test's no-op seam — the logs stream path doesn't fire warm
// captures.
func (r *deploymentFilterFakeVMM) WarmSnapshot(context.Context, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}
func (r *deploymentFilterFakeVMM) Destroy(context.Context, string, string) error { return nil }

// FrameworkReady implements RoutedVMM for the logs-filter test
// fake (issue #470 / PR #470-FU-B). No-op — the logs-filter tests
// exercise the per-deployment log filtering path, not the
// warm-capture receipt path.
func (r *deploymentFilterFakeVMM) FrameworkReady(context.Context, string, string, int64) error {
	return nil
}
func (r *deploymentFilterFakeVMM) Ping(context.Context, string) (*PingOutcome, error) {
	return &PingOutcome{FcVersion: "1.10.0"}, nil
}
func (r *deploymentFilterFakeVMM) Stats(context.Context, string) (*StatsSnapshot, error) {
	return &StatsSnapshot{}, nil
}
func (r *deploymentFilterFakeVMM) UpdateEgressAllowlist(context.Context, string, string, []netip.Prefix) error {
	return nil
}
func (r *deploymentFilterFakeVMM) UpdateStaticEgressIP(context.Context, string, string, string) error {
	return nil
}

// Tier A5 (ADR-066) — logs filter tests don't drive migration.
func (r *deploymentFilterFakeVMM) PrepareLiveMigration(context.Context, string, string, string) (LiveMigrationPrepare, error) {
	return LiveMigrationPrepare{}, nil
}
func (r *deploymentFilterFakeVMM) AdoptMigratedInstance(context.Context, string, string, AppSpec, string, string, string) (LiveMigrationAdopt, error) {
	return LiveMigrationAdopt{}, nil
}
func (r *deploymentFilterFakeVMM) AcknowledgeMigration(context.Context, string, string, string) error {
	return nil
}
func (r *deploymentFilterFakeVMM) CancelLiveMigration(context.Context, string, string, string) error {
	return nil
}

// programmableLogStream lets a test push a deterministic sequence
// of LogLine values, then io.EOF. Used to deliver gap frames from
// the vmmd-side producer. Frames are pre-buffered at construction;
// the stream closes itself automatically once the buffered frames
// have been delivered, so callers don't need to manage a close()
// lifecycle (avoids the "wg.Wait hangs because the test forgot to
// close" trap that whitebox tests around this engine seam hit).
type programmableLogStream struct {
	lines chan LogLine
	once  sync.Once
}

func newProgrammableLogStream(lines ...LogLine) *programmableLogStream {
	ch := make(chan LogLine, len(lines))
	for _, l := range lines {
		ch <- l
	}
	return &programmableLogStream{lines: ch}
}

func (s *programmableLogStream) Recv() (LogLine, error) {
	l, ok := <-s.lines
	if !ok {
		return LogLine{}, io.EOF
	}
	return l, nil
}

// close terminates the stream. Recv returns io.EOF after the
// buffered frames are drained. Safe to call multiple times.
func (s *programmableLogStream) close() {
	s.once.Do(func() { close(s.lines) })
}

// frameSink collects every LogFrame the engine emits via the
// per-instance goroutine fan-out.
type frameSink struct {
	mu     sync.Mutex
	frames []LogFrame
}

func (s *frameSink) sink() LogFrameSink {
	return func(f LogFrame) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.frames = append(s.frames, f)
		return nil
	}
}

func (s *frameSink) snapshot() []LogFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogFrame, len(s.frames))
	copy(out, s.frames)
	return out
}

// TestEngineStreamAppLogs_DeploymentFilterScopesFanout pins AC3:
// the engine skips instances whose DeploymentID disagrees with
// the caller's filter. Three live instances are staged:
//
//   - ins-A: empty DeploymentID (legacy row, matches any filter)
//   - ins-B: DeploymentID = "dep-1" (matches the filter)
//   - ins-C: DeploymentID = "dep-2" (skipped)
//
// The test asserts that the VMM fake only saw two Logs dials
// (A + B), with the right sinceSeq + sinceWrittenAt args. The
// per-instance streams close immediately so the fan-out
// goroutines exit before the test returns (otherwise the writer
// goroutine would hang on wg.Wait() forever).
func TestEngineStreamAppLogs_DeploymentFilterScopesFanout(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 5)

	// Seed three live instances on the local node, each pinned to
	// a different DeploymentID. The default CreateDeployment from
	// seedApp creates dep-1; we create two more so ins-B / ins-C
	// can disambiguate.
	insA, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(A): %v", err)
	}
	dep1, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:dep1", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(dep1): %v", err)
	}
	insB, err := store.CreateInstance(ctx, app.ID, dep1.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(B): %v", err)
	}
	dep2, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:dep2", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(dep2): %v", err)
	}
	insC, err := store.CreateInstance(ctx, app.ID, dep2.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(C): %v", err)
	}

	streamA := newProgrammableLogStream()
	streamB := newProgrammableLogStream()
	streamC := newProgrammableLogStream()
	vmm := &deploymentFilterFakeVMM{
		perInstanceStream: map[string]LogStream{
			state.DefaultLocalNodeName + "/" + insA.ID: streamA,
			state.DefaultLocalNodeName + "/" + insB.ID: streamB,
			state.DefaultLocalNodeName + "/" + insC.ID: streamC,
		},
	}
	eng := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	sink := &frameSink{}
	wantSince := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	done := make(chan error, 1)
	go func() {
		done <- eng.StreamAppLogs(ctx, app.ID, 42, wantSince, dep1.ID, sink.sink())
	}()
	// Wait for the engine to fan out two Logs dials (A + B),
	// then close all three streams so the per-instance goroutines
	// exit and the writer goroutine's wg.Wait() unblocks.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(vmm.dialed()) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	streamA.close()
	streamB.close()
	streamC.close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamAppLogs: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamAppLogs did not return within 2s of stream close")
	}
	dials := vmm.dialed()
	if len(dials) != 2 {
		t.Fatalf("got %d dials (want 2 = ins-A + ins-B), dials=%+v", len(dials), dials)
	}
	got := map[string]recordedDial{}
	for _, d := range dials {
		got[d.InstanceID] = d
	}
	if _, ok := got[insA.ID]; !ok {
		t.Errorf("ins-A (legacy empty DeploymentID) must match any filter, dials=%+v", dials)
	}
	if _, ok := got[insB.ID]; !ok {
		t.Errorf("ins-B (DeploymentID = dep-1) must match the filter, dials=%+v", dials)
	}
	if _, ok := got[insC.ID]; ok {
		t.Errorf("ins-C (DeploymentID = dep-2) must be filtered out, dials=%+v", dials)
	}
	for id, d := range got {
		if d.SinceSeq != 42 {
			t.Errorf("dial(%s).SinceSeq = %d, want 42", id, d.SinceSeq)
		}
		if !d.SinceWrittenAt.Equal(wantSince) {
			t.Errorf("dial(%s).SinceWrittenAt = %v, want %v", id, d.SinceWrittenAt, wantSince)
		}
	}
}

// TestEngineStreamAppLogs_GapForwardedFromVMM pins AC4: a gap
// frame the vmmd-side programmableLogStream delivers is forwarded
// to the sink verbatim, with IsGap=true, GapToWrittenAt populated,
// and GapReason labelled "seq_below_retained" (the vmmd producer
// chose it). Line frames that follow the gap are also forwarded.
//
// Also asserts Finding 2's contract: the schedd-side gap frame's
// line-frame fields (Seq / Stream / Line / WrittenAt) are zeroed
// before the sink call, so the wire payload can't accidentally
// leak stale values into a gap event.
func TestEngineStreamAppLogs_GapForwardedFromVMM(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 5)
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	headAt := time.Date(2026, 8, 1, 12, 30, 0, 0, time.UTC)
	stream := newProgrammableLogStream(
		LogLine{IsGap: true, GapToWrittenAt: headAt, GapReason: "seq_below_retained"},
		LogLine{Seq: 7, Stream: "stdout", Line: "first", WrittenAt: headAt.Add(time.Second)},
		LogLine{Seq: 8, Stream: "stdout", Line: "second", WrittenAt: headAt.Add(2 * time.Second)},
	)
	vmm := &deploymentFilterFakeVMM{
		perInstanceStream: map[string]LogStream{
			state.DefaultLocalNodeName + "/" + ins.ID: stream,
		},
	}
	eng := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	sink := &frameSink{}
	done := make(chan error, 1)
	go func() {
		done <- eng.StreamAppLogs(ctx, app.ID, 0, time.Time{}, "", sink.sink())
	}()
	// Wait for all three frames to land in the sink (each is
	// delivered synchronously by the writer goroutine) then
	// close the stream so the per-instance reader exits and
	// wg.Wait unblocks the closer goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.snapshot()) == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stream.close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamAppLogs: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamAppLogs did not return within 2s of stream close")
	}
	frames := sink.snapshot()
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3 (1 gap + 2 lines), frames=%+v", len(frames), frames)
	}
	// Frame 0: the gap.
	if !frames[0].IsGap {
		t.Errorf("frames[0].IsGap = false, want true")
	}
	if frames[0].InstanceID != ins.ID {
		t.Errorf("frames[0].InstanceID = %q, want %q", frames[0].InstanceID, ins.ID)
	}
	if !frames[0].GapToWrittenAt.Equal(headAt) {
		t.Errorf("frames[0].GapToWrittenAt = %v, want %v", frames[0].GapToWrittenAt, headAt)
	}
	if frames[0].GapReason != "seq_below_retained" {
		t.Errorf("frames[0].GapReason = %q, want %q", frames[0].GapReason, "seq_below_retained")
	}
	// Finding 2: line-frame fields must be zero on a gap frame.
	if frames[0].Seq != 0 || frames[0].Stream != "" || frames[0].Line != "" || !frames[0].WrittenAt.IsZero() {
		t.Errorf("gap frame leaked line-frame values: %+v", frames[0])
	}
	// Frame 1: line frame.
	if frames[1].IsGap {
		t.Errorf("frames[1].IsGap = true, want false")
	}
	if frames[1].Seq != 7 || frames[1].Stream != "stdout" || frames[1].Line != "first" {
		t.Errorf("frames[1] = %+v, want seq=7 stream=stdout line=first", frames[1])
	}
	// Frame 2: line frame.
	if frames[2].Seq != 8 || frames[2].Line != "second" {
		t.Errorf("frames[2] = %+v, want seq=8 line=second", frames[2])
	}
}
