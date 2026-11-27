package toolregistry

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// HostedTaskRehearsalCandidate is the private input to contained provisioning.
// Replayed claims carry the original run identity with empty archives.
type HostedTaskRehearsalCandidate struct {
	Run           state.HostedTaskRehearsalRun
	WebArchive    []byte `json:"-"`
	WorkerArchive []byte `json:"-"`
	bindings      tasktoolbinding.Policy
}

// ClaimHostedTaskRehearsalCandidate hands verified archives to the one caller
// that acquires the native claim. The owner key comes from the authenticated
// caller. Source verification precedes claim consumption so a bad archive
// cannot spend a valid approval, while the final transaction rechecks drift
// and expiry under lock.
func (e *PrivateExecutor) ClaimHostedTaskRehearsalCandidate(ctx context.Context, account, id, ownerKeyID, planDigest string) (HostedTaskRehearsalCandidate, error) {
	return e.claimHostedTaskRehearsalCandidate(ctx, account, id, ownerKeyID, planDigest, false, nil)
}

func (e *PrivateExecutor) claimHostedTaskRehearsalCandidate(ctx context.Context, account, id, ownerKeyID, planDigest string, runner bool, fixture *taskrehearsal.StartupFixture) (HostedTaskRehearsalCandidate, error) {
	var zero HostedTaskRehearsalCandidate
	journal, ok := e.store.(state.HostedTaskRehearsalStore)
	if !ok || !toolpublication.ValidID(planDigest) {
		return zero, state.ErrInvalidArgument
	}
	in := state.HostedTaskRehearsalChange{OwnerKeyID: ownerKeyID, PlanSHA256: planDigest}
	view, err := journal.ChangeHostedTaskRehearsal(ctx, account, id, state.ReadHostedTaskRehearsal, in)
	if err != nil {
		return zero, err
	}
	if view.Plan.PlanSHA256 != planDigest {
		return zero, state.ErrConflict
	}
	var candidate HostedTaskRehearsalCandidate
	if view.Plan.Status != tasktoolrehearsal.Claimed {
		if !view.ReadyToClaim || !e.opts.RehearsalPlanEnabled || !e.opts.SourceRetentionEnabled || !e.opts.BindingVerificationEnabled {
			return zero, state.ErrConflict
		}
		candidate, err = e.packRehearsalCandidate(ctx, view.Plan, runner, fixture)
		if err != nil {
			return zero, err
		}
	}
	out, err := journal.ChangeHostedTaskRehearsal(ctx, account, id, state.ClaimHostedTaskRehearsal, in)
	if err != nil {
		return zero, err
	}
	if out.Run == nil {
		return zero, state.ErrConflict
	}
	if !out.ClaimAcquired {
		return HostedTaskRehearsalCandidate{Run: *out.Run}, nil
	}
	candidate.Run = *out.Run
	return candidate, nil
}

func (e *PrivateExecutor) packRehearsalCandidate(ctx context.Context, plan api.HostedTaskRehearsalPlan, runner bool, fixture *taskrehearsal.StartupFixture) (HostedTaskRehearsalCandidate, error) {
	var zero HostedTaskRehearsalCandidate
	source, err := e.loadHostedTaskSource(ctx, plan.AccountID, plan.IntentID)
	if err != nil {
		return zero, err
	}
	if source.intent.IntentSHA256 != plan.IntentSHA256 || source.intent.ReviewSHA256 != plan.ReviewSHA256 {
		return zero, state.ErrConflict
	}
	if fixture != nil && (source.review.Sources["web"] != fixture.WebSHA256 || source.review.Sources["worker"] != fixture.WorkerSHA256) {
		return zero, state.ErrConflict
	}
	var bindings tasktoolbinding.Policy
	if runner {
		bindings, err = rehearsalRunnerBindings(source.entries)
		if err != nil {
			return zero, err
		}
	}
	web, err := tasktoolsource.PackReviewedArchive(source.entries["web"], source.review.Sources["web"])
	if err != nil {
		return zero, state.ErrConflict
	}
	worker, err := tasktoolsource.PackReviewedArchive(source.entries["worker"], source.review.Sources["worker"])
	if err != nil {
		return zero, state.ErrConflict
	}
	return HostedTaskRehearsalCandidate{WebArchive: web, WorkerArchive: worker, bindings: bindings}, nil
}

func rehearsalRunnerBindings(sources map[string][]api.HostedTaskSourceEntry) (tasktoolbinding.Policy, error) {
	for _, entry := range sources["worker"] {
		if entry.Path != "tasks-migrate.js" || entry.Directory || len(entry.Content) == 0 {
			continue
		}
		policy, err := reviewedHostedBindingPolicy(sources)
		if err != nil {
			return policy, err
		}
		return policy, taskrehearsal.ValidateRunnerPolicy(policy)
	}
	return tasktoolbinding.Policy{}, state.ErrConflict
}
