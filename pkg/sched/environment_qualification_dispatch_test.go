// adr: 568 — durable dispatch consumes current owner authority, not notifications.
package sched

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Interpose only after the real durable scan; claim/admission/runtime fences
// still use the real store. This models an old page or simultaneous scanners.
type delayedQualificationDispatchStore struct {
	*state.MemStore
	afterList func()
}

func (s *delayedQualificationDispatchStore) ListEnvironmentWorkloadQualificationsForDispatch(ctx context.Context, nodeID, cursor string, limit int) ([]string, error) {
	ids, err := s.MemStore.ListEnvironmentWorkloadQualificationsForDispatch(ctx, nodeID, cursor, limit)
	if err == nil && s.afterList != nil {
		s.afterList()
	}
	return ids, err
}

// One physical generation and stable receipt per original frame. Sharing the
// legacy one-attempt fake's receipt across candidates must be rejected by state.
func newDispatchQualificationVMM(vmm *fakeVMM) *qualificationRuntimeVMM {
	v := newQualificationRuntimeVMM(vmm)
	v.changeEvidence = func(evidence *EnvironmentQualificationRetirementEvidence) {
		evidence.Retirement.ReceiptID = uuid.NewSHA1(uuid.Nil, []byte(evidence.Execution.InstanceID+"-retirement")).String()
		evidence.Retirement.NativeGeneration = uuid.MustParse(evidence.Execution.InstanceID).String()
	}
	return v
}

func TestEnvironmentQualificationDispatchPagesRetireBeforeAdvancingWithoutActivation(t *testing.T) {
	store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeWorker, api.ExecutionModeService)
	v, notif := newDispatchQualificationVMM(&fakeVMM{}), &fakeNotifier{}
	e := newEngine(t, store, v, notif, "test-fc")
	ids, err := store.ListEnvironmentWorkloadQualificationsForDispatch(t.Context(), e.defaultLocalNodeID, "", 3)
	if err != nil || len(ids) != 3 {
		t.Fatal("fixture lacks complete queued cohort", err)
	}
	injected := errors.New("isolated check failed")
	visits := []string{}
	claimed := map[string]state.EnvironmentWorkloadQualificationRequest{}
	visit := func(ctx context.Context, request state.EnvironmentWorkloadQualificationRequest, ins state.Instance) error {
		visits = append(visits, request.ID)
		claimed[request.ID] = request
		if ins.ID != request.ReservedInstanceID || ins.State != string(state.StateRunning) || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
			return errors.New("runtime visitor has wrong reservation")
		}
		deadline, ok := ctx.Deadline()
		if !ok || request.LeaseUntil == nil || deadline.After(*request.LeaseUntil) {
			return errors.New("runtime visitor exceeds claim lease")
		}
		if request.ID == ids[0] {
			return injected
		}
		return nil
	}
	page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", "", 2, visit)
	if !errors.Is(err, injected) || page.Examined != 2 || page.Claimed != 2 || page.Executed != 1 || page.Skipped != 0 || page.NextCursor != ids[1] {
		t.Fatal("failed member starved neighbor or lost page progress", page, err)
	}
	for _, id := range ids[:2] {
		assertQualificationRetired(t, store, e, claimed[id], v, 2)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", page.NextCursor, 2, visit)
	if err != nil || page.Examined != 1 || page.Executed != 1 || page.NextCursor != "" || !reflect.DeepEqual(visits, ids) {
		t.Fatal("page cursor lost or repeated committed work", page, visits, err)
	}
	assertQualificationRetired(t, store, e, claimed[ids[2]], v, 3)
	restarted := newEngine(t, store, v, notif, "test-fc")
	page, err = restarted.DispatchEnvironmentWorkloadQualifications(t.Context(), restarted.defaultLocalNodeID, "restarted", "", 2, visit)
	if err != nil || page.Examined != 0 || v.coldBoots != 3 || v.snapshots != 0 || notif.count(db.NotifyDeploymentReady) != 0 {
		t.Fatal("restart replayed active lease or claimed activation evidence", page, err)
	}
	for _, request := range requests {
		if err := store.MarkDeploymentLive(t.Context(), request.DeploymentID); err == nil {
			t.Fatal("dispatch lifted candidate hold")
		}
		dep, err := store.DeploymentByID(t.Context(), request.DeploymentID)
		if err != nil || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeploySnapshotting {
			t.Fatal("dispatch changed held deployment identity or phase", err)
		}
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if err != nil || current.AppliedRevisionID != "" {
		t.Fatal("successful visitor became complete environment convergence", err)
	}
}

func TestEnvironmentQualificationDispatchRefusesStalePagesBeforeClaim(t *testing.T) {
	for _, change := range []string{"owner_transfer", "source_revocation", "other_claim"} {
		t.Run(change, func(t *testing.T) {
			store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
			request := requests[0]
			v := newDispatchQualificationVMM(&fakeVMM{})
			delayed := &delayedQualificationDispatchStore{MemStore: store}
			e := newEngine(t, delayed, v, &fakeNotifier{}, "test-fc")
			nodeB, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "new-owner", Active: true, AdmissionCeilingMB: 4096})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetAppNodeID(t.Context(), request.AppID, e.defaultLocalNodeID); err != nil {
				t.Fatal(err)
			}
			delayed.afterList = func() {
				switch change {
				case "owner_transfer":
					err = store.ReassignAppOwner(t.Context(), request.AppID, e.defaultLocalNodeID, nodeB.ID)
				case "source_revocation":
					_, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
				case "other_claim":
					_, err = store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), request.ID, e.defaultLocalNodeID, "winner", time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "stale-scanner", "", 1,
				func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
					t.Fatal("stale page booted a VM")
					return nil
				})
			if err != nil || page.Examined != 1 || page.Skipped != 1 || page.Claimed != 0 || page.Executed != 0 || page.NextCursor != request.ID || v.coldBoots != 0 || v.destroys != 0 {
				t.Fatal("advisory page became claim/native authority", page, err)
			}
			if change == "owner_transfer" {
				claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), request.ID, nodeB.ID, "new-owner", time.Minute)
				if err != nil || claimed.Attempt != 1 {
					t.Fatal("stale owner consumed an attempt", claimed.Attempt, err)
				}
			}
		})
	}
}

func TestEnvironmentQualificationDispatchRacingScannersBootOnce(t *testing.T) {
	store, _, _ := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	delayed := &delayedQualificationDispatchStore{MemStore: store, afterList: func() { ready.Done(); <-start }}
	vms := []*qualificationRuntimeVMM{newDispatchQualificationVMM(&fakeVMM{}), newDispatchQualificationVMM(&fakeVMM{})}
	engines := []*Engine{newEngine(t, delayed, vms[0], &fakeNotifier{}, "test-fc"), newEngine(t, delayed, vms[1], &fakeNotifier{}, "test-fc")}
	var visits atomic.Int32
	type outcome struct {
		page EnvironmentQualificationDispatchPage
		err  error
	}
	results := make(chan outcome, 2)
	for _, e := range engines {
		go func() {
			page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, uuid.NewString(), "", 1,
				func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
					visits.Add(1)
					return nil
				})
			results <- outcome{page, err}
		}()
	}
	ready.Wait()
	close(start)
	claimed, executed, skipped := 0, 0, 0
	for range engines {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		claimed += r.page.Claimed
		executed += r.page.Executed
		skipped += r.page.Skipped
	}
	if claimed != 1 || executed != 1 || skipped != 1 || visits.Load() != 1 || vms[0].coldBoots+vms[1].coldBoots != 1 || engines[0].ledger.ResidentRAM()+engines[1].ledger.ResidentRAM() != 0 {
		t.Fatal("racing durable consumers duplicated native work", claimed, executed, skipped)
	}
}

func TestEnvironmentQualificationDispatchCancellationLeavesNeighborsQueued(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeWorker)
	v := newDispatchQualificationVMM(&fakeVMM{})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	ctx, cancel := context.WithCancel(t.Context())
	page, err := e.DispatchEnvironmentWorkloadQualifications(ctx, e.defaultLocalNodeID, "cancelled", "", 2,
		func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
			cancel()
			return nil
		})
	if !errors.Is(err, context.Canceled) || page.Examined != 1 || page.Claimed != 1 || page.Executed != 0 || page.NextCursor == "" || e.ledger.ResidentRAM() != 0 || v.destroys != 1 {
		t.Fatal("cancelled consumer acquired its neighbor or skipped retirement", page, err)
	}
	ids := []string{requests[0].ID, requests[1].ID}
	slices.Sort(ids)
	if page.NextCursor != ids[0] {
		t.Fatal("cancellation advanced past unclaimed work", page.NextCursor)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "resume", page.NextCursor, 2,
		func(_ context.Context, request state.EnvironmentWorkloadQualificationRequest, _ state.Instance) error {
			if request.ID != ids[1] || request.Attempt != 1 {
				return errors.New("cancelled consumer changed unvisited attempt")
			}
			return nil
		})
	if err != nil || page.Examined != 1 || page.Executed != 1 || page.NextCursor != "" {
		t.Fatal("resume lost queued neighbor", page, err)
	}
}

func TestEnvironmentQualificationDispatchUncertainRetirementKeepsCapacity(t *testing.T) {
	store, source, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest, api.ExecutionModeWorker)
	injected := errors.New("native retirement unavailable")
	v := newDispatchQualificationVMM(&fakeVMM{destroyErr: injected})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	frames := map[string]state.EnvironmentQualificationExecution{}
	page, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "scheduler", "", 2,
		func(ctx context.Context, request state.EnvironmentWorkloadQualificationRequest, ins state.Instance) error {
			status, err := store.EnvironmentQualificationExecution(ctx, ins.ID)
			frames[request.ID] = status.Execution
			return err
		})
	if !errors.Is(err, injected) || page.Examined != 2 || page.Claimed != 2 || page.Executed != 0 || e.ledger.ResidentRAM() != 2*(512+api.PerVMOverheadMB) {
		t.Fatal("consumer acknowledged uncertain retirement or starved neighbor", page, err)
	}
	page, err = e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "again", "", 2,
		func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
			t.Fatal("uncertain attempt replayed")
			return nil
		})
	if err != nil || page.Examined != 0 || v.coldBoots != 2 {
		t.Fatal("unfinished original execution reentered dispatch", page, err)
	}
	if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
		t.Fatal(err)
	}
	v.destroyErr = nil
	recovery, err := e.RecoverEnvironmentQualificationExecutions(t.Context(), e.defaultLocalNodeID, "", 3)
	if err != nil || recovery.Retired != 2 || e.ledger.ResidentRAM() != 0 {
		t.Fatal("original retirement could not release retained capacity", recovery, err)
	}
	for _, request := range requests {
		status, err := store.EnvironmentQualificationExecution(t.Context(), frames[request.ID].InstanceID)
		if err != nil || status.Execution != frames[request.ID] || status.RetiredAt == nil {
			t.Fatal("recovery replaced original consumer frame", err)
		}
	}
}

func TestEnvironmentQualificationDispatchPreflightDoesNotClaimWork(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	legacy := &fakeVMM{}
	e := newEngine(t, store, legacy, &fakeNotifier{}, "test-fc")
	visit := func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error {
		t.Fatal("preflight failure ran visitor")
		return nil
	}
	if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "old-node", "", 1, visit); !errors.Is(err, state.ErrConflict) || legacy.coldBoots != 0 {
		t.Fatal("generic VMM consumed durable qualification work", err)
	}
	e = newEngine(t, store, newDispatchQualificationVMM(&fakeVMM{}), &fakeNotifier{}, "test-fc")
	for _, args := range []struct {
		node, worker, cursor string
		limit                int
	}{
		{"", "worker", "", 1}, {uuid.Nil.String(), "worker", "", 1}, {uuid.NewString(), "worker", "", 1},
		{e.defaultLocalNodeID, "", "", 1}, {e.defaultLocalNodeID, " worker ", "", 1},
		{e.defaultLocalNodeID, strings.Repeat("w", api.EnvironmentGitOpsQualificationWorkerIDMaxBytes+1), "", 1},
		{e.defaultLocalNodeID, "worker", "bad-cursor", 1}, {e.defaultLocalNodeID, "worker", uuid.Nil.String(), 1},
		{e.defaultLocalNodeID, "worker", "", 0}, {e.defaultLocalNodeID, "worker", "", api.EnvironmentGitOpsQualificationDispatchBatchMax + 1},
	} {
		if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), args.node, args.worker, args.cursor, args.limit, visit); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid or foreign consumer accepted", err)
		}
	}
	if _, err := e.DispatchEnvironmentWorkloadQualifications(t.Context(), e.defaultLocalNodeID, "worker", "", 1, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("consumer without checker reserved work", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.DispatchEnvironmentWorkloadQualifications(ctx, e.defaultLocalNodeID, "cancelled", "", 1, visit); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled consumer claimed work", err)
	}
	claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID, e.defaultLocalNodeID, "first-valid", time.Minute)
	if err != nil || claimed.Attempt != 1 {
		t.Fatal("preflight rejection changed durable queue authority", claimed.Attempt, err)
	}
}
