package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_SafeReleaseWorkerLeaseHealth(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	health, err := s.SafeReleaseWorkerLeaseHealth(ctx)
	if err != nil || health.Exists || health.Ready() {
		t.Fatalf("missing lease health = %+v, err=%v", health, err)
	}
	if err := s.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	health, err = s.SafeReleaseWorkerLeaseHealth(ctx)
	if err != nil || !health.Ready() || health.SecondsUntilExpiry() <= 0 || health.SecondsUntilExpiry() > 60 {
		t.Fatalf("fresh lease health = %+v, err=%v", health, err)
	}
	if _, err := pool.Exec(ctx, `update safe_release_worker_lease set healthy_at = clock_timestamp() - interval '3 minutes', expires_at = clock_timestamp() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	health, err = s.SafeReleaseWorkerLeaseHealth(ctx)
	if err != nil || health.Ready() || health.SecondsUntilExpiry() >= 0 {
		t.Fatalf("expired lease health = %+v, err=%v", health, err)
	}
}

func TestPg_AdvanceCanaryRequiresFreshWorkerLease(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "lease-advance")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:lease-advance",
		Status: state.DeployPending, Scope: "default", CanaryTotalSteps: 4, TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status = 'live', rollout_state = 'pending', canary_step = 0, traffic_percent = 1 where id = $1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 99 where id = $1`, priorID); err != nil {
		t.Fatal(err)
	}
	params := state.CanaryAdvanceParams{
		ExpectedStep: 0, TrafficPercent: 10, RequireSafeReleaseLease: true,
		Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "meterd:canary_progression"},
	}
	if _, _, err := s.AdvanceCanary(ctx, candidate.ID, params); !errors.Is(err, state.ErrSafeReleaseLeaseUnavailable) || !errors.Is(err, state.ErrSafeReleaseLeaseMissing) {
		t.Fatalf("advance without worker lease = %v, want unavailable/missing", err)
	}
	if err := s.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	advanced, _, err := s.AdvanceCanary(ctx, candidate.ID, params)
	if err != nil || advanced.CanaryStep != 1 || advanced.TrafficPercent != 10 {
		t.Fatalf("advance with fresh lease = %+v, err=%v", advanced, err)
	}
	if _, err := pool.Exec(ctx, `update safe_release_worker_lease set healthy_at = clock_timestamp() - interval '3 minutes', expires_at = clock_timestamp() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	params.ExpectedStep = 1
	params.TrafficPercent = 50
	if _, _, err := s.AdvanceCanary(ctx, candidate.ID, params); !errors.Is(err, state.ErrSafeReleaseLeaseUnavailable) {
		t.Fatalf("advance with expired worker lease = %v, want unavailable", err)
	}
	unchanged, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || unchanged.CanaryStep != 1 || unchanged.TrafficPercent != 10 {
		t.Fatalf("expired-lease advance mutated candidate = %+v, err=%v", unchanged, err)
	}
	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil || prior.TrafficPercent != 90 {
		t.Fatalf("expired-lease advance mutated predecessor = %+v, err=%v", prior, err)
	}
	var auditRows int
	if err := pool.QueryRow(ctx, `select count(*) from deployment_audit where deployment_id = $1`, candidate.ID).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("audit rows after blocked advance = %d, err=%v; want only the successful step", auditRows, err)
	}
}

func TestPg_AbortCanaryOnExpiredWorkerLease(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "lease-emergency", "lease-emergency")
	canary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:lease-emergency",
		Status: state.DeployPending, Scope: "default", CanaryTotalSteps: 4, TrafficPercent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status = 'live', rollout_state = 'rolling_out', canary_step = 1, traffic_percent = 10 where id = $1`, canary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 90 where id = $1`, priorID); err != nil {
		t.Fatal(err)
	}
	grace := 2 * time.Minute
	if _, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, appID, canary.ID, grace); !errors.Is(err, state.ErrSafeReleaseLeaseMissing) {
		t.Fatalf("missing lease = %v", err)
	}
	if err := s.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, appID, canary.ID, grace); !errors.Is(err, state.ErrSafeReleaseLeaseNotExpired) {
		t.Fatalf("fresh lease = %v", err)
	}
	if _, err := pool.Exec(ctx, `update safe_release_worker_lease set healthy_at = clock_timestamp() - interval '3 minutes', expires_at = clock_timestamp() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, appID, canary.ID, grace); !errors.Is(err, state.ErrSafeReleaseLeaseNotExpired) {
		t.Fatalf("lease inside grace = %v", err)
	}
	if _, err := pool.Exec(ctx, `update safe_release_worker_lease set healthy_at = clock_timestamp() - interval '5 minutes', expires_at = clock_timestamp() - interval '3 minutes'`); err != nil {
		t.Fatal(err)
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `listen deployment_changed`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		deployment state.Deployment
		auditID    int64
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	recoverCtx, recoverCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recoverCancel()
	for range 2 {
		go func() {
			<-start
			d, id, callErr := s.AbortCanaryOnExpiredWorkerLease(recoverCtx, appID, canary.ID, grace)
			results <- result{deployment: d, auditID: id, err: callErr}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, state.ErrNotFound) {
		t.Fatalf("concurrent aborts: first=%v second=%v", first.err, second.err)
	}
	aborted, auditID := first.deployment, first.auditID
	notifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	notification, err := listener.Conn().WaitForNotification(notifyCtx)
	if err != nil || notification.Channel != "deployment_changed" || !strings.Contains(notification.Payload, appID) {
		t.Fatalf("traffic invalidation = %+v, err=%v", notification, err)
	}
	if aborted.RolloutState != "aborted" || aborted.TrafficPercent != 0 || auditID == 0 {
		t.Fatalf("abort = %+v, audit_id=%d", aborted, auditID)
	}
	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil || prior.TrafficPercent != 100 {
		t.Fatalf("predecessor = %+v, err=%v", prior, err)
	}
	var actor string
	if err := pool.QueryRow(ctx, `select actor from deployment_audit where id = $1`, auditID).Scan(&actor); err != nil || actor != "apid:safe_release_lease_expired" {
		t.Fatalf("audit actor = %q, err=%v", actor, err)
	}
	if _, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, appID, canary.ID, grace); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate abort = %v", err)
	}
}

func TestPg_AbortServingPendingCanaryOnExpiredWorkerLease(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "lease-pending", "lease-pending")
	canary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:lease-pending",
		Status: state.DeployPending, Scope: "default", CanaryTotalSteps: 4, TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status = 'live', rollout_state = 'pending', canary_step = 0, traffic_percent = 1 where id = $1`, canary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 99 where id = $1`, priorID); err != nil {
		t.Fatal(err)
	}
	if err := s.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update safe_release_worker_lease set healthy_at = clock_timestamp() - interval '5 minutes', expires_at = clock_timestamp() - interval '3 minutes'`); err != nil {
		t.Fatal(err)
	}
	aborted, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, appID, canary.ID, 2*time.Minute)
	if err != nil || aborted.RolloutState != "aborted" {
		t.Fatalf("pending canary abort = %+v, err=%v", aborted, err)
	}
}

func TestPg_RecoverRolloutAbortRedistributesTraffic(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "recover-abort")

	canary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:            appID,
		Kind:             state.DeploymentKindImage,
		ImageDigest:      "sha256:recover-abort",
		Status:           state.DeployPending,
		Scope:            "canary",
		CanaryTotalSteps: 4,
		TrafficPercent:   25,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(canary): %v", err)
	}

	// CreateDeployment intentionally leaves canary metadata to the deploy
	// handler. Seed the minimal active-rollout state needed to exercise the
	// transactional abort path against the real schema.
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'live', canary_step = 1, canary_total_steps = 4,
		       rollout_state = 'pending'
		 where id = $1`, canary.ID); err != nil {
		t.Fatalf("seed active canary: %v", err)
	}

	aborted, _, err := s.RecoverRollout(ctx, appID, "abort", "manual stop")
	if err != nil {
		t.Fatalf("RecoverRollout(abort): %v", err)
	}
	if aborted.ID != canary.ID || aborted.RolloutState != "aborted" || aborted.TrafficPercent != 0 || aborted.RolloutAbortedReason != "manual stop" {
		t.Fatalf("aborted deployment = %+v", aborted)
	}

	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil {
		t.Fatalf("DeploymentByID(prior): %v", err)
	}
	if prior.TrafficPercent != 100 {
		t.Fatalf("prior traffic = %d, want 100", prior.TrafficPercent)
	}

	live, err := s.LiveDeployments(ctx, appID)
	if err != nil {
		t.Fatalf("LiveDeployments: %v", err)
	}
	if len(live) != 2 || live[0].TrafficPercent+live[1].TrafficPercent != 100 {
		t.Fatalf("live traffic = %+v, want two rows summing to 100", live)
	}
}
