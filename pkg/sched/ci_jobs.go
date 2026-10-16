package sched

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

type ciJobsRuntime struct {
	store  ci.JobsSchedulerStore
	config ci.Config
	origin string
}

// WithCIJobsRuntime configures only the scheduler boundary for already-owned
// Jobs runs. It never admits CI executions or starts an execution dispatcher.
func (e *Engine) WithCIJobsRuntime(store ci.JobsSchedulerStore, cfg ci.Config, origin string) *Engine {
	e.ciJobs = &ciJobsRuntime{store: store, config: cfg, origin: origin}
	return e
}

func (e *Engine) initCIJobsRuntime() {
	if provider, ok := e.store.(interface{ CIStore() ci.Store }); ok {
		if store, ok := provider.CIStore().(ci.JobsSchedulerStore); ok {
			// A caller that omits explicit wiring still refuses CI-owned work.
			e.WithCIJobsRuntime(store, ci.Config{}, "")
		}
	}
}

func (e *Engine) ciJobPreflight(ctx context.Context, run state.JobRun, index int) (bool, time.Time, error) {
	if e.ciJobs == nil || e.ciJobs.store == nil {
		if len(run.Command) > 0 && run.Command[0] == ci.JobsRunnerPath {
			return true, time.Time{}, ci.ErrRuntimeAuthority
		}
		return false, time.Time{}, nil
	}
	owned, deadline, err := e.ciJobs.store.ValidateJobsLaunch(ctx, e.ciJobs.config, run.AccountID, run.ID, index)
	if err != nil {
		return owned, deadline, ci.ErrRuntimeAuthority
	}
	if owned && (!e.ciJobs.config.TransportEnabled || !ci.ValidRuntimeOrigin(e.ciJobs.origin) || e.jobVmmClient == nil || e.vmm == nil) {
		return true, deadline, ci.ErrRuntimeAuthority
	}
	if owned {
		if _, ok := e.jobVmmClient.(interface {
			ReleaseJobStart(context.Context, JobStartSpec) error
		}); !ok {
			return true, deadline, ci.ErrRuntimeAuthority
		}
	}
	return owned, deadline, nil
}

func (e *Engine) ciJobProof(ctx context.Context, runID, instanceID string, token LeaseToken) (ci.RuntimeProof, error) {
	proof, err := e.ciJobs.store.IssueJobsRuntime(ctx, e.ciJobs.config, ci.RuntimeHostProof{RunID: runID, InstanceID: instanceID, LeaseToken: string(token)})
	if err != nil {
		return ci.RuntimeProof{}, ci.ErrRuntimeAuthority
	}
	return proof, nil
}

func (e *Engine) ciJobStartAllowed(ctx context.Context, proof ci.RuntimeProof) error {
	view, err := e.ciJobs.store.AuthorizeJobsRuntime(ctx, e.ciJobs.config, proof, ci.RuntimeControl)
	if err != nil || view.CancelRequested || view.Sealed {
		return ci.ErrRuntimeAuthority
	}
	return nil
}

func scrubCIJobEnv(env map[string]string) {
	for key := range env {
		if strings.HasPrefix(key, "GREGALE_CI_") {
			delete(env, key)
		}
	}
}

// CI boot rollback uses the normal native destroy boundary. Capacity and the
// instance's live state survive a failed destroy so reconciliation can retry.
func (e *Engine) rollbackCIJobAdmission(ctx context.Context, runID string, index int, instanceID string, token LeaseToken, message string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DestroyTimeout)
	defer cancel()
	_, err := e.store.JobTaskFailBoot(cleanupCtx, runID, index, instanceID, string(token), 0, time.Time{}, message)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		e.log.Warn("sched: CI boot failure receipt pending", "run", runID)
		return
	}
	if e.jobLeaser != nil {
		_ = e.jobLeaser.Release(cleanupCtx, token, e.ownerNodeID)
	}
	if instance, err := e.store.InstanceByID(cleanupCtx, instanceID); err == nil {
		e.cleanupJobInstance(cleanupCtx, instanceID, instance.NodeID, "ci_boot_failed")
	}
	_, _ = e.store.JobRunRecompute(cleanupCtx, runID)
}
