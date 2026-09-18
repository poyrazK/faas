package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testInvocationClaimPreservesStoredCap protects the operator/plan-provisioned
// cap from a transient caller value. A drain lookup can pass zero (or another
// stale value), but an existing account_async_quota row is authoritative.
func testInvocationClaimPreservesStoredCap(t *testing.T, fx *Fixture) {
	const storedCap = 1
	if max, current, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, storedCap); err != nil {
		t.Fatalf("EnsureAccountAsyncQuota: %v", err)
	} else if max != storedCap || current != 0 {
		t.Fatalf("initial quota = %d/%d, want %d/0", max, current, storedCap)
	}

	first, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(first): %v", err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, first.ID, "instance-1", 30, 99); err != nil {
		t.Fatalf("ClaimInvocationWithCap(first): %v", err)
	}

	if max, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountAsyncQuota(after first claim): %v", err)
	} else if max != storedCap || current != 1 {
		t.Fatalf("quota after first claim = %d/%d, want %d/1", max, current, storedCap)
	}

	second, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(second): %v", err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, second.ID, "instance-2", 30, 99); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("second claim err = %v, want ErrQuotaExceeded", err)
	}
}

func testInvocationRetryReleasesReservedSlot(t *testing.T, fx *Fixture) {
	const cap = 2
	if _, _, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, cap); err != nil {
		t.Fatalf("EnsureAccountAsyncQuota: %v", err)
	}
	inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "instance-1", 30, cap); err != nil {
		t.Fatalf("ClaimInvocationWithCap(first): %v", err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, inv.ID, "transient", time.Millisecond, cap); err != nil {
		t.Fatalf("FailInvocation(retry): %v", err)
	}
	if max, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountAsyncQuota(after retry): %v", err)
	} else if max != cap || current != 0 {
		t.Fatalf("quota after retry = %d/%d, want %d/0", max, current, cap)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "instance-2", 30, cap); err != nil {
		t.Fatalf("ClaimInvocationWithCap(second): %v", err)
	}
	if _, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil || current != 1 {
		t.Fatalf("quota after re-claim = %d, %v; want 1", current, err)
	}
	if err := fx.Store.CompleteInvocation(fx.Ctx, inv.ID, nil); err != nil {
		t.Fatalf("CompleteInvocation: %v", err)
	}
	if _, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil || current != 0 {
		t.Fatalf("quota after completion = %d, %v; want 0", current, err)
	}
}

func testLegacyClaimDoesNotReleaseReservedSlot(t *testing.T, fx *Fixture) {
	const cap = 2
	if _, _, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, cap); err != nil {
		t.Fatalf("EnsureAccountAsyncQuota: %v", err)
	}
	reserved, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(reserved): %v", err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, reserved.ID, "reserved", 30, cap); err != nil {
		t.Fatalf("ClaimInvocationWithCap: %v", err)
	}

	legacy, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationCron, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(legacy): %v", err)
	}
	if _, err := fx.Store.ClaimInvocation(fx.Ctx, legacy.ID, "legacy", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if err := fx.Store.CompleteInvocation(fx.Ctx, legacy.ID, nil); err != nil {
		t.Fatalf("CompleteInvocation(legacy): %v", err)
	}
	if _, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil || current != 1 {
		t.Fatalf("quota after legacy completion = %d, %v; want reserved sibling's slot (1)", current, err)
	}
	if err := fx.Store.CompleteInvocation(fx.Ctx, reserved.ID, nil); err != nil {
		t.Fatalf("CompleteInvocation(reserved): %v", err)
	}
}

// testLeaseRequeueReleasesEachSlot verifies that lease recovery accounts for
// every abandoned dispatch, not merely one row per account. A leaked slot can
// permanently reduce queue throughput after a worker or host disappears.
func testLeaseRequeueReleasesEachSlot(t *testing.T, fx *Fixture) {
	const cap = 3
	reclaimer, ok := fx.Store.(interface {
		RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	})
	if !ok {
		t.Fatal("store does not implement the scheduler requeue contract")
	}
	if _, _, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, cap); err != nil {
		t.Fatalf("EnsureAccountAsyncQuota: %v", err)
	}

	ids := make([]string, 0, cap)
	for i := 0; i < cap; i++ {
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
			AppID: fx.App.ID, AccountID: fx.Account.ID,
			Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation(%d): %v", i, err)
		}
		ids = append(ids, inv.ID)
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "instance-"+uuid.NewString(), 0, cap); err != nil {
			t.Fatalf("ClaimInvocationWithCap(%d): %v", i, err)
		}
	}

	requeued, err := reclaimer.RequeueExpiredInvocations(fx.Ctx, time.Now().UTC().Add(time.Second), cap)
	if err != nil {
		t.Fatalf("RequeueExpiredInvocations: %v", err)
	}
	if requeued != cap {
		t.Fatalf("requeued = %d, want %d", requeued, cap)
	}
	if max, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountAsyncQuota(after requeue): %v", err)
	} else if max != cap || current != 0 {
		t.Fatalf("quota after requeue = %d/%d, want %d/0", max, current, cap)
	}

	for i, id := range ids {
		inv, err := fx.Store.InvocationByID(fx.Ctx, id)
		if err != nil {
			t.Fatalf("InvocationByID(%d): %v", i, err)
		}
		if inv.State != state.InvocationPending || inv.LeaseExpiresAt != nil || inv.InstanceID != "" || inv.LastError != "dispatch lease expired; requeued" {
			t.Fatalf("requeued invocation %d = state %q lease %v instance %q error %q", i, inv.State, inv.LeaseExpiresAt, inv.InstanceID, inv.LastError)
		}
	}
}

// testDeadlineForceOnlyReleasesTransitions catches a particularly damaging
// accounting bug: forcing a batch must decrement inflight only for rows that
// actually moved from pending/dispatching. Terminal rows can be present in a
// stale reaper batch after completing concurrently.
func testDeadlineForceOnlyReleasesTransitions(t *testing.T, fx *Fixture) {
	if _, _, err := fx.Store.EnsureAccountAsyncQuota(fx.Ctx, fx.Account.ID, 3); err != nil {
		t.Fatalf("EnsureAccountAsyncQuota: %v", err)
	}

	ids := make([]string, 0, 4)
	for i := 0; i < 3; i++ {
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
			AppID: fx.App.ID, AccountID: fx.Account.ID,
			Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation(%d): %v", i, err)
		}
		ids = append(ids, inv.ID)
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "instance-"+uuid.NewString(), 30, 3); err != nil {
			t.Fatalf("ClaimInvocationWithCap(%d): %v", i, err)
		}
	}
	pending, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{
		AppID: fx.App.ID, AccountID: fx.Account.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation(pending): %v", err)
	}
	ids = append(ids, pending.ID)

	// Make the first row terminal before the deadline batch is forced. The
	// second row is still active; the third row proves the counter is not
	// merely clamped into a passing value.
	if err := fx.Store.CompleteInvocation(fx.Ctx, ids[0], nil); err != nil {
		t.Fatalf("CompleteInvocation: %v", err)
	}
	forced, err := fx.Store.ForceDeadlineBreachedInvocations(fx.Ctx, []string{ids[0], ids[1], ids[3]})
	if err != nil {
		t.Fatalf("ForceDeadlineBreachedInvocations: %v", err)
	}
	if forced != 2 {
		t.Fatalf("forced = %d, want 2", forced)
	}

	completed, err := fx.Store.InvocationByID(fx.Ctx, ids[0])
	if err != nil {
		t.Fatalf("InvocationByID(completed): %v", err)
	}
	if completed.State != state.InvocationCompleted {
		t.Fatalf("completed row state = %q, want completed", completed.State)
	}
	forcedInvocation, err := fx.Store.InvocationByID(fx.Ctx, ids[1])
	if err != nil {
		t.Fatalf("InvocationByID(forced): %v", err)
	}
	if forcedInvocation.State != state.InvocationDeadLetter || forcedInvocation.Outcome == nil || *forcedInvocation.Outcome != state.OutcomeTimeout {
		t.Fatalf("forced row = state %q outcome %v, want dead_letter/timeout", forcedInvocation.State, forcedInvocation.Outcome)
	}

	if max, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountAsyncQuota(after force): %v", err)
	} else if max != 3 || current != 1 {
		t.Fatalf("quota after force = %d/%d, want 3/1", max, current)
	}
}

// testMirrorRuleRejectsOversizedRedactionList keeps the store boundary aligned
// with the SQL/API contract. This deliberately uses invalid deployment IDs:
// the input-size guard must run before any lookup or transaction work.
func testMirrorRuleRejectsOversizedRedactionList(t *testing.T, fx *Fixture) {
	headers := make([]string, 33)
	_, err := fx.Store.CreateMirrorRuleIfUnderQuota(fx.Ctx, state.CreateMirrorRuleParams{
		AccountID:          fx.Account.ID,
		AppID:              fx.App.ID,
		SourceDeploymentID: uuid.NewString(),
		MirrorDeploymentID: uuid.NewString(),
		Percent:            100,
		RedactHeaders:      headers,
	}, api.MustLimitsFor(api.PlanPro))
	if err == nil || !strings.Contains(err.Error(), "mirror_rules redact_headers") {
		t.Fatalf("oversized redact_headers err = %v, want cap validation", err)
	}
}

func testMirrorRuleUpdateRejectsOversizedRedactionList(t *testing.T, fx *Fixture) {
	mirrorDeployment, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:mirror-" + uuid.NewString(), Status: state.DeployPending,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(mirror): %v", err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, mirrorDeployment.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(mirror): %v", err)
	}

	rule, err := fx.Store.CreateMirrorRuleIfUnderQuota(fx.Ctx, state.CreateMirrorRuleParams{
		AccountID:          fx.Account.ID,
		AppID:              fx.App.ID,
		SourceDeploymentID: fx.Deployment.ID,
		MirrorDeploymentID: mirrorDeployment.ID,
		Percent:            100,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateMirrorRuleIfUnderQuota: %v", err)
	}

	headers := make([]string, 33)
	if _, err := fx.Store.UpdateMirrorRule(fx.Ctx, rule.ID, state.MirrorRulePatch{RedactHeaders: &headers}); err == nil || !strings.Contains(err.Error(), "mirror_rules redact_headers") {
		t.Fatalf("oversized update err = %v, want cap validation", err)
	}
	unchanged, err := fx.Store.GetMirrorRuleByID(fx.Ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetMirrorRuleByID(after rejected update): %v", err)
	}
	if len(unchanged.RedactHeaders) != 0 {
		t.Fatalf("redact_headers after rejected update = %d, want 0", len(unchanged.RedactHeaders))
	}
}
