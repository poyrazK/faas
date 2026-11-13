package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CPUAbuseMode is schedd's sustained CPU abuse switch (ADR-20261112120603).
type CPUAbuseMode string

const (
	// CPUAbuseOff ignores the signal.
	CPUAbuseOff CPUAbuseMode = "off"
	// CPUAbuseShadow audits and counts every signal but recycles nothing.
	CPUAbuseShadow CPUAbuseMode = "shadow"
	// CPUAbuseEnforce recycles Free instances and escalates repeats to the
	// account abuse hold; paid instances are still only flagged.
	CPUAbuseEnforce CPUAbuseMode = "enforce"
)

// DefaultCPUAbuseMode is shadow until a week of production signals has been
// reviewed (ADR-20261112120603 decision 5).
const DefaultCPUAbuseMode = CPUAbuseShadow

// ParseCPUAbuseMode parses off, shadow or enforce.
func ParseCPUAbuseMode(s string) (CPUAbuseMode, error) {
	switch m := CPUAbuseMode(s); m {
	case CPUAbuseOff, CPUAbuseShadow, CPUAbuseEnforce:
		return m, nil
	}
	return "", fmt.Errorf("cpu abuse mode must be off, shadow or enforce (got %q)", s)
}

// CPU abuse actions: the action label of schedd_cpu_abuse_signals_total and
// the audit event.
const (
	CPUAbuseActionRecycled = "recycled"
	CPUAbuseActionShadow   = "shadow"
	CPUAbuseActionFlagged  = "flagged"
)

// cpuAbuseKind is the instance transition kind of a CPU abuse recycle.
const cpuAbuseKind = "cpu_sustained"

// cpuAbuseAction decides what a sustained CPU signal does on a plan: only
// Free is recycled, and only in enforce mode. Paid plans run legitimate
// CPU-bound work (batch, encoding, inference) and have a card on file, so they
// are flagged for review.
func cpuAbuseAction(plan api.Plan, mode CPUAbuseMode) string {
	if plan != api.PlanFree {
		return CPUAbuseActionFlagged
	}
	if mode == CPUAbuseEnforce {
		return CPUAbuseActionRecycled
	}
	return CPUAbuseActionShadow
}

// HandleSustainedCPU acts on an instance whose CPU use stayed at
// api.CPUAbuseSaturationPercent of its quota for api.CPUAbuseSustainedAfter
// (ADR-20261112120603). Every signal is audited as instance.cpu_abuse and
// counted; a Free instance in enforce mode is recycled like an egress abuse
// recycle, and the recycle counts toward the ADR-361 account abuse hold. It
// returns the action taken ("" in off mode or when the app is gone).
func (e *Engine) HandleSustainedCPU(ctx context.Context, instanceID, appID string, mode CPUAbuseMode, usedMillicores, quotaMillicores int, sustained time.Duration) (string, error) {
	if mode == CPUAbuseOff {
		return "", nil
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("sched: cpu abuse: load app %s: %w", appID, err)
	}
	owner, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		return "", fmt.Errorf("sched: cpu abuse: resource owner: %w", err)
	}
	plan := owner.Plan
	if plan == "" {
		plan = api.PlanFree
	}
	action := cpuAbuseAction(plan, mode)
	detail := map[string]any{
		"instance": instanceID, "app": appID, "account": owner.ResourceOwnerID(), "plan": string(plan),
		"action": action, "mode": string(mode), "used_millicores": usedMillicores, "quota_millicores": quotaMillicores,
		"sustained_seconds": int64(sustained / time.Second),
	}
	if action == CPUAbuseActionRecycled {
		recycledApp, recycleErr := e.recycleForAbuse(ctx, instanceID, cpuAbuseKind,
			fmt.Sprintf("%s used_millicores=%d quota_millicores=%d sustained_s=%d", cpuAbuseKind, usedMillicores, quotaMillicores, int64(sustained/time.Second)), nil)
		if recycleErr != nil {
			return "", recycleErr
		}
		if recycledApp == "" {
			// No longer running (parked, frozen or destroyed since the
			// sample): nothing to recycle, and the next episode re-arms.
			return "", nil
		}
	}
	e.log.Warn("cpu abuse: sustained CPU", "instance", instanceID, "app", appID, "account", owner.ResourceOwnerID(),
		"plan", plan, "action", action, "used_millicores", usedMillicores, "quota_millicores", quotaMillicores, "sustained", sustained)
	if e.ops != nil {
		e.ops.CPUAbuseSignal(string(plan), action).Inc()
	}
	if data, marshalErr := json.Marshal(detail); marshalErr == nil {
		subject := appID
		if auditErr := e.store.AppendEvent(ctx, "schedd", "instance.cpu_abuse", &subject, data); auditErr != nil {
			e.log.Warn("cpu abuse: audit write failed", "instance", instanceID, "err", auditErr)
		}
	}
	if action != CPUAbuseActionRecycled {
		return action, nil
	}
	// The app lock is released: a hold parks every app of the account.
	if !e.noteEgressAbuseRecycle(owner.ResourceOwnerID(), e.clock()) {
		return action, nil
	}
	holdDetail := map[string]any{"app": appID, "signal": cpuAbuseKind, "used_millicores": usedMillicores, "quota_millicores": quotaMillicores,
		"sustained_seconds": int64(sustained / time.Second), "recycles": api.EgressFanoutHoldRecycles, "window_seconds": api.EgressFanoutHoldWindowSeconds}
	return action, e.HoldAccountForAbuse(ctx, owner.ResourceOwnerID(), state.AccountAbuseHoldCPUSustained, holdDetail)
}
