package conformance

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1330
// testJobRunAttach pins interactive image-run sessions: the session row is
// written with its run and only for a plain one-task run with no retries, the
// attach target is found only through its own account and job name and is
// ready once its instance runs on a node, and a finished interactive run is
// never replayed.
func testJobRunAttach(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.JobRunAttachStore)
	if !ok {
		t.Fatalf("%T does not implement state.JobRunAttachStore", fx.Store)
	}
	name := "attach-" + uuid.NewString()[:8]
	job, err := fx.Store.JobCreate(fx.Ctx, fx.Account.ID, name, "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/sh"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	materializer, ok := fx.Store.(interface {
		JobSetImageMaterialization(context.Context, string, string, string, string, string, string) (state.Job, error)
	})
	if !ok {
		t.Fatal("store does not implement job image materialization")
	}
	if _, err := materializer.JobSetImageMaterialization(fx.Ctx, job.ID, job.ImageRef, "ready",
		"sha256:"+strings.Repeat("b", 64), "jobs/conformance-attach.ext4", ""); err != nil {
		t.Fatalf("JobSetImageMaterialization: %v", err)
	}
	digest := sha256.Sum256([]byte("attach-token"))
	attach := &state.JobRunAttach{TTY: true, TokenSHA256: digest[:], AttachTimeoutSec: 60}
	one, zero := 1, 0
	createInteractive := func(opts state.JobRunOptions) (state.JobRun, error) {
		run, _, err := fx.Store.JobRunCreate(fx.Ctx, job.ID, fx.Account.ID, "manual", &one, &zero, nil, nil, 1, opts)
		return run, err
	}

	testJobRunAttachRejectsInvalidSessions(t, fx, job.ID, attach)

	run, err := createInteractive(state.JobRunOptions{Attach: attach})
	if err != nil {
		t.Fatalf("interactive JobRunCreate: %v", err)
	}
	session, err := store.JobRunAttachSession(fx.Ctx, run.ID)
	if err != nil || !session.TTY || session.AttachTimeoutSec != 60 || string(session.TokenSHA256) != string(digest[:]) {
		t.Fatalf("JobRunAttachSession = %+v, %v", session, err)
	}
	session.TokenSHA256[0] ^= 0xff
	if again, err := store.JobRunAttachSession(fx.Ctx, run.ID); err != nil || string(again.TokenSHA256) != string(digest[:]) {
		t.Fatalf("JobRunAttachSession aliased its digest: %x, %v", again.TokenSHA256, err)
	}
	ordinary, _, err := fx.Store.JobRunCreate(fx.Ctx, job.ID, fx.Account.ID, "manual", &one, &zero, nil, nil, 1)
	if err != nil {
		t.Fatalf("ordinary JobRunCreate: %v", err)
	}
	for label, runID := range map[string]string{"ordinary run": ordinary.ID, "unknown run": uuid.NewString(), "malformed run": "run-1"} {
		if _, err := store.JobRunAttachSession(fx.Ctx, runID); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("JobRunAttachSession(%s) = %v, want ErrNotFound", label, err)
		}
	}

	target, err := store.JobTaskAttachTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || target.Status != "queued" || !target.TTY || target.InstanceID != "" || target.NodeID != "" ||
		target.Ready() || target.Terminal() {
		t.Fatalf("queued attach target = %+v, %v", target, err)
	}
	for label, args := range map[string]struct {
		account, job, run string
		index             int
	}{
		"other account":       {uuid.NewString(), name, run.ID, 0},
		"other job name":      {fx.Account.ID, name + "-x", run.ID, 0},
		"unknown run":         {fx.Account.ID, name, uuid.NewString(), 0},
		"non-interactive run": {fx.Account.ID, name, ordinary.ID, 0},
		"unknown task":        {fx.Account.ID, name, run.ID, 1},
		"negative index":      {fx.Account.ID, name, run.ID, -1},
		"index past int32":    {fx.Account.ID, name, run.ID, math.MaxInt32 + 1},
		"malformed run":       {fx.Account.ID, name, "run-1", 0},
		"malformed account":   {"account-1", name, run.ID, 0},
	} {
		if _, err := store.JobTaskAttachTarget(fx.Ctx, args.account, args.job, args.run, args.index); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("JobTaskAttachTarget(%s) = %v, want ErrNotFound", label, err)
		}
	}

	instanceID := uuid.NewString()
	if _, err := fx.Store.CreateAndClaimJobInstance(fx.Ctx, instanceID, job.ID, run.ID, 0,
		"cold_booting", 128, fx.Node.ID, instanceID, uuid.NewString(), time.Now().Add(5*time.Minute), fx.Node.ID); err != nil {
		t.Fatalf("CreateAndClaimJobInstance: %v", err)
	}
	target, err = store.JobTaskAttachTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || target.Status != "claimed" || target.InstanceID != instanceID || target.NodeID != fx.Node.ID ||
		target.InstanceState != "cold_booting" || target.Ready() {
		t.Fatalf("booting attach target = %+v, %v", target, err)
	}
	if _, err := fx.Store.PublishInstanceRuntime(fx.Ctx, instanceID, "cold_booting", "netns-attach", "10.200.0.1", 20001); err != nil {
		t.Fatalf("PublishInstanceRuntime: %v", err)
	}
	target, err = store.JobTaskAttachTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || target.InstanceState != string(state.StateRunning) || !target.Ready() || target.Terminal() {
		t.Fatalf("running attach target = %+v, %v", target, err)
	}

	for _, finished := range []state.JobRun{run, ordinary} {
		if err := fx.Store.JobTaskMarkTerminal(fx.Ctx, finished.ID, 0, "failed", 129, "cancelled", "client left", time.Now()); err != nil {
			t.Fatalf("JobTaskMarkTerminal(%s): %v", finished.ID, err)
		}
		if recomputed, err := fx.Store.JobRunRecompute(fx.Ctx, finished.ID); err != nil || recomputed.FinishedAt == nil {
			t.Fatalf("JobRunRecompute(%s) = %+v, %v", finished.ID, recomputed, err)
		}
	}
	target, err = store.JobTaskAttachTarget(fx.Ctx, fx.Account.ID, name, run.ID, 0)
	if err != nil || target.Status != "failed" || !target.Terminal() || target.Ready() {
		t.Fatalf("finished attach target = %+v, %v", target, err)
	}
	if _, _, err := fx.Store.JobRunReplayFailed(fx.Ctx, run.ID, fx.Account.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay of an interactive run = %v, want ErrConflict", err)
	}
	if _, _, err := fx.Store.JobRunReplayFailed(fx.Ctx, ordinary.ID, fx.Account.ID); err != nil {
		t.Fatalf("replay of an ordinary run sharing the job: %v", err)
	}
	// A manual retry would requeue the task under the same token digest: a
	// reattach. It is refused; an ordinary run's task still retries.
	if err := fx.Store.JobTaskRetry(fx.Ctx, run.ID, 0, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retry of an interactive task = %v, want ErrConflict", err)
	}
	if task, err := fx.Store.JobTaskGet(fx.Ctx, run.ID, 0); err != nil || task.Status != "failed" || task.Attempt != 1 {
		t.Fatalf("interactive task after refused retry = %+v, %v", task, err)
	}
	if err := fx.Store.JobTaskRetry(fx.Ctx, ordinary.ID, 0, time.Now()); err != nil {
		t.Fatalf("retry of an ordinary task sharing the job: %v", err)
	}

	deletedName := name + "-gone"
	gone, err := fx.Store.JobCreate(fx.Ctx, fx.Account.ID, deletedName, "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/sh"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate(deleted): %v", err)
	}
	goneRun, _, err := fx.Store.JobRunCreate(fx.Ctx, gone.ID, fx.Account.ID, "manual", &one, &zero, nil, nil, 1,
		state.JobRunOptions{Attach: attach})
	if err != nil {
		t.Fatalf("JobRunCreate(deleted): %v", err)
	}
	// MemStore counts a queued task as live work; settle it so both stores
	// delete the job.
	if err := fx.Store.JobTaskMarkTerminal(fx.Ctx, goneRun.ID, 0, "cancelled", 130, "cancelled", "", time.Now()); err != nil {
		t.Fatalf("JobTaskMarkTerminal(deleted): %v", err)
	}
	if deleted, live, err := fx.Store.JobSoftDelete(fx.Ctx, gone.ID); err != nil || !deleted || live {
		t.Fatalf("JobSoftDelete = %v (live %v), %v", deleted, live, err)
	}
	if _, err := store.JobTaskAttachTarget(fx.Ctx, fx.Account.ID, deletedName, goneRun.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("attach target of a deleted job = %v, want ErrNotFound", err)
	}
}

// testJobRunAttachRejectsInvalidSessions admits a session only on a plain
// one-task run with no retries, a SHA-256 digest and a 1..300 s window. A
// rejected request creates no run.
func testJobRunAttachRejectsInvalidSessions(t *testing.T, fx *Fixture, jobID string, valid *state.JobRunAttach) {
	t.Helper()
	before, err := fx.Store.JobRunListByJob(fx.Ctx, jobID, 100, 0)
	if err != nil {
		t.Fatalf("JobRunListByJob: %v", err)
	}
	withAttach := func(mutate func(*state.JobRunAttach)) *state.JobRunAttach {
		copied := *valid
		copied.TokenSHA256 = append([]byte(nil), valid.TokenSHA256...)
		mutate(&copied)
		return &copied
	}
	one, zero, two := 1, 0, 2
	cases := map[string]struct {
		tasks    int
		retryMax *int
		opts     state.JobRunOptions
	}{
		"two tasks":       {tasks: 2, retryMax: &zero, opts: state.JobRunOptions{Attach: valid}},
		"job retry max":   {tasks: 1, retryMax: nil, opts: state.JobRunOptions{Attach: valid}},
		"retries allowed": {tasks: 1, retryMax: &one, opts: state.JobRunOptions{Attach: valid}},
		"short digest": {tasks: 1, retryMax: &zero, opts: state.JobRunOptions{
			Attach: withAttach(func(a *state.JobRunAttach) { a.TokenSHA256 = a.TokenSHA256[:16] })}},
		"no window": {tasks: 1, retryMax: &zero, opts: state.JobRunOptions{
			Attach: withAttach(func(a *state.JobRunAttach) { a.AttachTimeoutSec = 0 })}},
		"window too long": {tasks: 1, retryMax: &zero, opts: state.JobRunOptions{
			Attach: withAttach(func(a *state.JobRunAttach) { a.AttachTimeoutSec = state.JobRunAttachMaxTimeoutSec + 1 })}},
		"inputs": {tasks: 1, retryMax: &zero, opts: state.JobRunOptions{
			Attach: valid, Inputs: []state.JobInput{{ID: "only", Ref: "obj-only"}}}},
	}
	for label, tc := range cases {
		parallelism := two
		if _, _, err := fx.Store.JobRunCreate(fx.Ctx, jobID, fx.Account.ID, "manual", &parallelism, tc.retryMax, nil, nil,
			tc.tasks, tc.opts); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("JobRunCreate(%s) = %v, want ErrInvalidArgument", label, err)
		}
	}
	after, err := fx.Store.JobRunListByJob(fx.Ctx, jobID, 100, 0)
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected interactive runs were created: before=%d after=%d err=%v", len(before), len(after), err)
	}
}
