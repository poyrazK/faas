package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

type DataRepairRehearsalExecutor interface {
	Get(context.Context, core.Scope, string) (core.Plan, error)
	Snapshot(context.Context, core.Scope, string, string) (core.RehearsalInput, error)
	RecordRehearsal(context.Context, core.Scope, string, string, core.Evidence) (core.Plan, error)
}

type DataRepairRehearsalBinding struct {
	Scope    core.Scope
	Identity string
	Executor DataRepairRehearsalExecutor
}

type DataRepairRehearsalVMM interface {
	DataRepairRehearsalIdentity(context.Context, string, core.RunnerSpec) (string, error)
	RunDataRepairRehearsal(context.Context, core.RehearsalFrame, core.RehearsalInput) (core.Evidence, error)
	RetireDataRepairRehearsal(context.Context, core.RehearsalFrame) error
}

type DataRepairRehearsalAdmission interface {
	ReserveDataRepairRehearsal(context.Context, state.DataRepairRehearsal, string, core.RunnerSpec, string) (core.RehearsalFrame, error)
	ReleaseDataRepairRehearsal(string)
}

// One bounded worker per node. Database admission independently enforces the
// node/account caps across processes. An admitted task is never reclaimed.
type DataRepairRehearsalCoordinator struct {
	Store                                      state.DataRepairRehearsalStore
	Admission                                  DataRepairRehearsalAdmission
	VMM                                        DataRepairRehearsalVMM
	Bindings                                   []DataRepairRehearsalBinding
	Runner                                     core.RunnerSpec
	NodeID                                     string
	Enabled                                    bool
	Log                                        *slog.Logger
	queuedCursor, recoveryCursor, expiryCursor string
	pendingClaims                              map[string]core.RehearsalFrame
}

func (c *DataRepairRehearsalCoordinator) binding(t state.DataRepairRehearsal) (DataRepairRehearsalExecutor, error) {
	for _, b := range c.Bindings {
		if b.Scope == t.Scope && b.Identity == t.Binding && b.Executor != nil {
			return b.Executor, nil
		}
	}
	return nil, core.ErrUnavailable
}

func (c *DataRepairRehearsalCoordinator) Run(ctx context.Context) error {
	if c == nil || c.Store == nil || c.Admission == nil || c.VMM == nil || c.NodeID == "" {
		return core.ErrUnavailable
	}
	if c.Enabled && c.Runner.Validate() != nil {
		return core.ErrInvalid
	}
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := c.RecoverOnce(ctx); err != nil && ctx.Err() == nil {
			log.Warn("data repair rehearsal recovery deferred", "error_class", "unavailable")
		}
		if c.Enabled {
			if _, err := c.ProcessNext(ctx); err != nil && ctx.Err() == nil {
				log.Warn("data repair rehearsal deferred", "error_class", "unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *DataRepairRehearsalCoordinator) ProcessNext(ctx context.Context) (bool, error) {
	if !c.Enabled {
		return false, nil
	}
	rows, err := c.Store.ListDataRepairRehearsals(ctx, true, "", c.queuedCursor, api.DataRepairRehearsalPageSize)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		c.queuedCursor = ""
		return false, nil
	}
	for _, t := range rows {
		c.queuedCursor = t.ID
		if !time.Now().Before(t.PlanExpiresAt) {
			_, err = c.Store.RetireDataRepairRehearsal(ctx, t.ID, nil)
			return true, err
		}
		executor, e := c.binding(t)
		if e != nil {
			continue
		}
		incarnation, e := c.VMM.DataRepairRehearsalIdentity(ctx, c.NodeID, c.Runner)
		if e != nil {
			continue
		}
		f, e := c.Admission.ReserveDataRepairRehearsal(ctx, t, c.NodeID, c.Runner, incarnation)
		if e != nil {
			continue
		}
		claimed, e := c.Store.ClaimDataRepairRehearsal(ctx, t.ID, f)
		if e != nil {
			// A lost claim response may have committed. Keep its reservation
			// unless a fresh primary read proves it was never admitted.
			fresh, readErr := c.Store.DataRepairRehearsal(ctx, t.ID)
			if readErr == nil && (fresh.Phase == state.RepairRehearsalQueued || fresh.Terminal() || fresh.Frame != nil && !fresh.Frame.Same(f)) {
				c.Admission.ReleaseDataRepairRehearsal(t.ID)
			} else if readErr != nil {
				if c.pendingClaims == nil {
					c.pendingClaims = map[string]core.RehearsalFrame{}
				}
				c.pendingClaims[t.ID] = f
			}
			return false, e
		}
		return true, c.execute(ctx, claimed, executor)
	}
	if len(rows) < api.DataRepairRehearsalPageSize {
		c.queuedCursor = ""
	}
	return false, nil
}

func (c *DataRepairRehearsalCoordinator) execute(parent context.Context, t state.DataRepairRehearsal, x DataRepairRehearsalExecutor) error {
	ctx, cancel := context.WithDeadline(parent, t.Frame.Deadline)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go c.watchCancellation(ctx, t.ID, cancel, done)
	input, err := x.Snapshot(ctx, t.Scope, t.PlanID, t.PlanDigest)
	if err == nil && !t.Frame.Matches(input.Plan) {
		err = core.ErrConflict
	}
	if err == nil {
		var dispatched state.DataRepairRehearsal
		dispatched, err = c.Store.DispatchDataRepairRehearsal(ctx, t.ID, input.SnapshotSHA256)
		if err == nil {
			t = dispatched
		}
	}
	var evidence core.Evidence
	if err == nil {
		evidence, err = c.VMM.RunDataRepairRehearsal(ctx, *t.Frame, input)
	}
	clean, stop := context.WithTimeout(context.WithoutCancel(parent), time.Duration(api.DataRepairRehearsalCleanupSeconds)*time.Second)
	defer stop()
	if err == nil && t.Frame.ValidOutcome(evidence) && input.Plan.ValidCheckResults(evidence) {
		stored, e := c.Store.RetireDataRepairRehearsal(clean, t.ID, &evidence)
		if e != nil {
			return e
		}
		c.Admission.ReleaseDataRepairRehearsal(t.ID)
		if stored.Phase == state.RepairRehearsalPublishing {
			return c.publish(clean, stored)
		}
		return nil
	}
	// Always reload the persisted frame: a lost dispatch response cannot
	// substitute an older, snapshot-free frame for native retirement.
	fresh, e := c.Store.DataRepairRehearsal(clean, t.ID)
	if e != nil {
		return e
	}
	return c.retire(clean, fresh)
}

func (c *DataRepairRehearsalCoordinator) watchCancellation(ctx context.Context, id string, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			t, err := c.Store.DataRepairRehearsal(ctx, id)
			if err != nil || t.CancelRequested || t.Phase != state.RepairRehearsalRunning {
				cancel()
				return
			}
		}
	}
}

func (c *DataRepairRehearsalCoordinator) retire(ctx context.Context, t state.DataRepairRehearsal) error {
	if t.Terminal() {
		c.Admission.ReleaseDataRepairRehearsal(t.ID)
		return nil
	}
	if t.Frame != nil && t.Frame.SnapshotSHA256 != "" {
		var err error
		t, err = c.Store.MarkDataRepairRehearsalCleanup(ctx, t.ID)
		if err != nil {
			return err
		}
		if err = c.VMM.RetireDataRepairRehearsal(ctx, *t.Frame); err != nil {
			return err
		}
	}
	if _, err := c.Store.RetireDataRepairRehearsal(ctx, t.ID, nil); err != nil {
		return err
	}
	c.Admission.ReleaseDataRepairRehearsal(t.ID)
	return nil
}

func (c *DataRepairRehearsalCoordinator) publish(ctx context.Context, t state.DataRepairRehearsal) error {
	if t.Frame == nil || t.Evidence == nil || !t.Frame.ValidOutcome(*t.Evidence) {
		return core.ErrConflict
	}
	if t.CancelRequested || !time.Now().Before(t.PlanExpiresAt) {
		_, err := c.Store.CompleteDataRepairRehearsal(ctx, t.ID)
		return err
	}
	x, err := c.binding(t)
	if err != nil {
		return err
	}
	p, err := x.Get(ctx, t.Scope, t.PlanID)
	if err != nil {
		return err
	}
	if !t.Frame.Matches(p) {
		return core.ErrConflict
	}
	if !publishedCheckOutcome(t, p) {
		p, err = x.RecordRehearsal(ctx, t.Scope, t.PlanID, t.PlanDigest, *t.Evidence)
		if err != nil {
			return err
		}
	}
	if !t.Frame.Matches(p) || !publishedCheckOutcome(t, p) {
		return core.ErrConflict
	}
	_, err = c.Store.CompleteDataRepairRehearsal(ctx, t.ID)
	return err
}

func publishedCheckOutcome(t state.DataRepairRehearsal, p core.Plan) bool {
	if t.Evidence.Passed {
		return p.Rehearsal != nil && t.Frame.ValidEvidence(*p.Rehearsal) && p.ValidCheckResults(*p.Rehearsal)
	}
	return p.CheckFailure != nil && p.CheckFailure.Stage == "rehearsal" && !p.CheckFailure.Evidence.Passed &&
		t.Frame.ValidOutcome(p.CheckFailure.Evidence) && p.ValidCheckResults(p.CheckFailure.Evidence)
}

// Recovery is always enabled and uses only the recorded node/frame. It never
// fetches another snapshot or boots another VM, even after task expiry.
func (c *DataRepairRehearsalCoordinator) RecoverOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairRehearsalCleanupSeconds)*time.Second)
	defer cancel()
	for id, frame := range c.pendingClaims {
		t, err := c.Store.DataRepairRehearsal(ctx, id)
		if err != nil {
			return err
		}
		if t.Phase == state.RepairRehearsalQueued || t.Terminal() || t.Frame != nil && !t.Frame.Same(frame) {
			c.Admission.ReleaseDataRepairRehearsal(id)
		}
		delete(c.pendingClaims, id)
	}
	queued, err := c.Store.ListDataRepairRehearsals(ctx, true, "", c.expiryCursor, api.DataRepairRehearsalPageSize)
	if err != nil {
		return err
	}
	for _, t := range queued {
		c.expiryCursor = t.ID
		if !time.Now().Before(t.PlanExpiresAt) {
			if _, err = c.Store.RetireDataRepairRehearsal(ctx, t.ID, nil); err != nil {
				return err
			}
		}
	}
	if len(queued) < api.DataRepairRehearsalPageSize {
		c.expiryCursor = ""
	}
	rows, err := c.Store.ListDataRepairRehearsals(ctx, false, c.NodeID, c.recoveryCursor, api.DataRepairRehearsalPageSize)
	if err != nil {
		return err
	}
	var result error
	for _, t := range rows {
		c.recoveryCursor = t.ID
		if t.Phase == state.RepairRehearsalPublishing {
			result = errors.Join(result, c.publish(ctx, t))
			continue
		}
		if t.Frame == nil {
			result = errors.Join(result, core.ErrConflict)
			continue
		}
		if t.Phase == state.RepairRehearsalCleanup || t.CancelRequested || !time.Now().Before(t.Frame.Deadline) {
			result = errors.Join(result, c.retire(ctx, t))
		}
	}
	if len(rows) < api.DataRepairRehearsalPageSize {
		c.recoveryCursor = ""
	}
	return result
}
