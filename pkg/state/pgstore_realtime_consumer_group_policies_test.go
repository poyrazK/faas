package state_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// realtimeMetadataFixture is realtimeGroupFixture with per-message metadata,
// plus the pool so a test can move clocks the store reads with
// clock_timestamp().
func realtimeMetadataFixture(t *testing.T, suffix string, metadata ...map[string]string) (*state.PgStore, *pgxpool.Pool, context.Context, string) {
	t.Helper()
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, suffix)
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	for i, md := range metadata {
		if _, err := s.AppendManagedRealtimeChannelMetadata(ctx, endpoint.ID, "orders", []byte("order-"+strconv.Itoa(i+1)), false, "", md); err != nil {
			t.Fatalf("append message %d: %v", i+1, err)
		}
	}
	return s, pool, ctx, endpoint.ID
}

func createRealtimeGroup(t *testing.T, s *state.PgStore, ctx context.Context, ep, name string, cfg state.RealtimeConsumerGroupConfig) state.RealtimeConsumerGroup {
	t.Helper()
	cfg.AfterSequence = "0"
	g, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", name, cfg)
	if err != nil {
		t.Fatalf("CreateRealtimeConsumerGroup %s: %v", name, err)
	}
	return g
}

// claimAll claims from group name until it is empty and returns the claims
// in order, completing each when complete is set.
func claimAll(t *testing.T, s *state.PgStore, ctx context.Context, ep, name string, complete bool) []state.RealtimeConsumerGroupClaim {
	t.Helper()
	var claims []state.RealtimeConsumerGroupClaim
	for range 32 {
		claim, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", name, "worker-a")
		if err != nil {
			t.Fatalf("ClaimRealtimeConsumerGroup %s: %v", name, err)
		}
		if claim == nil {
			return claims
		}
		claims = append(claims, *claim)
		if complete {
			req := state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "complete"}
			if _, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", name, req); err != nil {
				t.Fatalf("complete %s: %v", claim.Message.Sequence, err)
			}
		}
	}
	t.Fatal("group never drained")
	return nil
}

func claimedSequences(claims []state.RealtimeConsumerGroupClaim) []string {
	out := []string{}
	for _, c := range claims {
		out = append(out, c.Message.Sequence)
	}
	return out
}

// failClaim settles a claim as a failed attempt with code and no explicit
// delay, so the group's retry policy (or the immediate default) applies.
func failClaim(t *testing.T, s *state.PgStore, ctx context.Context, ep string, claim *state.RealtimeConsumerGroupClaim, code string) state.RealtimeConsumerGroupSettlementResult {
	t.Helper()
	req := state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "retry", FailureCode: code}
	res, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", req)
	if err != nil {
		t.Fatalf("retry %s: %v", claim.Message.Sequence, err)
	}
	return res
}

// killMessages dead-letters the first len(codes) messages of a MaxAttempts=1
// group, one failure code each.
func killMessages(t *testing.T, s *state.PgStore, ctx context.Context, ep string, codes ...string) {
	t.Helper()
	for _, code := range codes {
		claim := claimSequence(t, s, ctx, ep, "worker-a")
		if res := failClaim(t, s, ctx, ep, claim, code); res.Status != "dead" {
			t.Fatalf("retry %s = %+v, want dead", claim.Message.Sequence, res)
		}
	}
}

func deadSequences(t *testing.T, s *state.PgStore, ctx context.Context, ep string) map[string]string {
	t.Helper()
	letters, err := s.ListRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing")
	if err != nil {
		t.Fatalf("ListRealtimeConsumerGroupDeadLetters: %v", err)
	}
	out := map[string]string{}
	for _, l := range letters {
		out[l.Message.Sequence] = l.FailureCode
	}
	return out
}

func TestPgStoreRealtimeConsumerGroupRetryPolicy(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-retry", 2)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 5})
	if _, err := s.SetRealtimeConsumerGroupRetryPolicy(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupRetryPolicy{Enabled: true}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("zero base delay err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	policy := state.RealtimeConsumerGroupRetryPolicy{Enabled: true, BaseDelaySeconds: 30, MaxDelaySeconds: 120, PermanentFailureCodes: []string{"bad_input"}}
	if _, err := s.SetRealtimeConsumerGroupRetryPolicy(ctx, ep, "orders", "billing", policy); err != nil {
		t.Fatalf("SetRealtimeConsumerGroupRetryPolicy: %v", err)
	}

	first := claimSequence(t, s, ctx, ep, "worker-a")
	before := time.Now()
	res := failClaim(t, s, ctx, ep, first, "timeout")
	if res.Status != "ready" || res.AvailableAt == nil {
		t.Fatalf("retry = %+v, want ready with a backoff", res)
	}
	if delay := res.AvailableAt.Sub(before); delay < 25*time.Second || delay > 35*time.Second {
		t.Fatalf("backoff = %s, want the 30 s base delay", delay)
	}
	status, err := s.ReadRealtimeConsumerGroupRetryPolicy(ctx, ep, "orders", "billing")
	if err != nil || status.NextRetryAt == nil || !status.Enabled || !slices.Equal(status.PermanentFailureCodes, []string{"bad_input"}) {
		t.Fatalf("retry status = %+v, %v", status, err)
	}

	// The backed-off message waits; the next one is claimable now, and a
	// permanent failure code dead-letters it on the first attempt.
	second := claimSequence(t, s, ctx, ep, "worker-a")
	if second.Message.Sequence != "2" {
		t.Fatalf("claim during backoff = %s, want 2", second.Message.Sequence)
	}
	if res := failClaim(t, s, ctx, ep, second, "bad_input"); res.Status != "dead" {
		t.Fatalf("permanent failure = %+v, want dead", res)
	}
	if claim, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-a"); err != nil || claim != nil {
		t.Fatalf("claim during backoff = %+v, %v, want nothing", claim, err)
	}
	if dead := deadSequences(t, s, ctx, ep); len(dead) != 1 || dead["2"] != "bad_input" {
		t.Fatalf("dead letters = %v, want 2:bad_input", dead)
	}
}

func TestPgStoreRealtimeConsumerGroupCircuitBreaker(t *testing.T) {
	s, pool, ctx, ep := realtimeMetadataFixture(t, "-rt-group-circuit", nil, nil, nil)
	g := createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 10})
	if _, err := s.SetRealtimeConsumerGroupCircuit(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupCircuitPolicy{Enabled: true}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("zero threshold err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	if _, err := s.SetRealtimeConsumerGroupCircuit(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupCircuitPolicy{Enabled: true, FailureThreshold: 2, CooldownSeconds: 60}); err != nil {
		t.Fatalf("SetRealtimeConsumerGroupCircuit: %v", err)
	}
	for range 2 {
		failClaim(t, s, ctx, ep, claimSequence(t, s, ctx, ep, "worker-a"), "handler_failed")
	}
	var open *state.RealtimeConsumerGroupCircuitOpenError
	if _, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-a"); !errors.As(err, &open) || open.RetryAfterMilliseconds <= 0 {
		t.Fatalf("claim with open circuit err = %v, want CircuitOpenError with a retry hint", err)
	}
	circuit, err := s.ReadRealtimeConsumerGroupCircuit(ctx, ep, "orders", "billing")
	if err != nil || circuit.State != "open" || circuit.ConsecutiveFailures != 2 || !circuit.Blocked || circuit.NextProbeAt == nil {
		t.Fatalf("circuit = %+v, %v, want open after 2 failures", circuit, err)
	}

	// Elapse the cooldown: one probe claim goes through, the rest wait on it.
	if _, err := pool.Exec(ctx, `update managed_realtime_consumer_groups set circuit_open_until=clock_timestamp()-interval '1 second' where id=$1`, g.GroupID); err != nil {
		t.Fatal(err)
	}
	probe := claimSequence(t, s, ctx, ep, "worker-a")
	circuit, err = s.ReadRealtimeConsumerGroupCircuit(ctx, ep, "orders", "billing")
	if err != nil || circuit.State != "half_open" || circuit.ProbeSequence == nil || *circuit.ProbeSequence != probe.Message.Sequence {
		t.Fatalf("circuit after probe claim = %+v, %v, want half_open on %s", circuit, err, probe.Message.Sequence)
	}
	if _, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-b"); !errors.As(err, &open) {
		t.Fatalf("second claim during probe err = %v, want CircuitOpenError", err)
	}
	if _, err := settle(s, ctx, ep, probe, "complete"); err != nil {
		t.Fatalf("complete probe: %v", err)
	}
	circuit, err = s.ReadRealtimeConsumerGroupCircuit(ctx, ep, "orders", "billing")
	if err != nil || circuit.State != "closed" || circuit.ConsecutiveFailures != 0 || circuit.Blocked {
		t.Fatalf("circuit after probe success = %+v, %v, want closed", circuit, err)
	}
	if next := claimSequence(t, s, ctx, ep, "worker-b"); next.Message.Sequence != "2" {
		t.Fatalf("claim after close = %s, want 2", next.Message.Sequence)
	}
}

func TestPgStoreRealtimeConsumerGroupClaimRate(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-rate", 3)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{})
	if _, err := s.SetRealtimeConsumerGroupClaimRate(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupClaimRatePolicy{RatePerSecond: 1}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("zero burst err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	if _, err := s.SetRealtimeConsumerGroupClaimRate(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupClaimRatePolicy{RatePerSecond: 1, Burst: 1}); err != nil {
		t.Fatalf("SetRealtimeConsumerGroupClaimRate: %v", err)
	}
	claimSequence(t, s, ctx, ep, "worker-a")
	var limited *state.RealtimeConsumerGroupRateLimitError
	if _, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-a"); !errors.As(err, &limited) || limited.RetryAfterMilliseconds <= 0 || limited.RetryAfterMilliseconds > 1000 {
		t.Fatalf("over-rate claim err = %v, want RateLimitError within one second", err)
	}
	batch, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, "orders", "billing", "worker-a", 3)
	if err != nil || !batch.RateThrottled || len(batch.Claims) != 0 {
		t.Fatalf("over-rate batch = %+v, %v, want throttled and empty", batch, err)
	}
	rate, err := s.ReadRealtimeConsumerGroupClaimRate(ctx, ep, "orders", "billing")
	if err != nil || rate.ThrottledClaimRequests != "2" || rate.RatePerSecond != 1 {
		t.Fatalf("claim rate = %+v, %v, want 2 throttled requests", rate, err)
	}
	// Rate 0 lifts the limit immediately.
	if _, err := s.SetRealtimeConsumerGroupClaimRate(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupClaimRatePolicy{Burst: 1}); err != nil {
		t.Fatalf("clear claim rate: %v", err)
	}
	if got := claimedSequences(claimAll(t, s, ctx, ep, "billing", false)); !slices.Equal(got, []string{"2", "3"}) {
		t.Fatalf("claims after clearing the rate = %v, want [2 3]", got)
	}
}

func TestPgStoreRealtimeConsumerGroupWorkerHeartbeats(t *testing.T) {
	s, pool, ctx, ep := realtimeMetadataFixture(t, "-rt-group-heartbeat")
	g := createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{})
	ready, notReady := true, false
	if _, err := s.HeartbeatRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeWorkerHeartbeatRequest{Worker: "w1"}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("heartbeat without readiness err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	if _, err := s.HeartbeatRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeWorkerHeartbeatRequest{Worker: "w1", Ready: &ready}); err != nil {
		t.Fatalf("heartbeat w1: %v", err)
	}
	if hb, err := s.HeartbeatRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeWorkerHeartbeatRequest{Worker: "w2", Ready: &notReady, StatusCode: "warming"}); err != nil || !hb.Live || hb.StatusCode != "warming" {
		t.Fatalf("heartbeat w2 = %+v, %v", hb, err)
	}
	health, err := s.ReadRealtimeConsumerGroupWorkerHealth(ctx, ep, "orders", "billing")
	if err != nil || len(health.Workers) != 2 || health.LiveWorkers != 2 || health.ReadyWorkers != 1 {
		t.Fatalf("health = %+v, %v, want 2 live, 1 ready", health, err)
	}

	// w2 goes silent past the timeout; the background scan marks it lost.
	if _, err := pool.Exec(ctx, `update managed_realtime_worker_heartbeats set last_seen_at=clock_timestamp()-interval '10 minutes' where group_id=$1 and worker='w2'`, g.GroupID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EvaluateRealtimeConsumerGroupWorkerHealth(ctx); err != nil || n != 1 {
		t.Fatalf("EvaluateRealtimeConsumerGroupWorkerHealth = %d, %v, want 1 group", n, err)
	}
	health, err = s.ReadRealtimeConsumerGroupWorkerHealth(ctx, ep, "orders", "billing")
	if err != nil || health.LiveWorkers != 1 || health.ReadyWorkers != 1 {
		t.Fatalf("health after timeout = %+v, %v, want 1 live", health, err)
	}
	for _, w := range health.Workers {
		if w.Worker == "w2" && (!w.Lost || w.Live) {
			t.Fatalf("w2 = %+v, want lost", w)
		}
	}
	// A fresh heartbeat revives the worker.
	if hb, err := s.HeartbeatRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeWorkerHeartbeatRequest{Worker: "w2", Ready: &ready}); err != nil || hb.Lost {
		t.Fatalf("revive w2 = %+v, %v", hb, err)
	}
	health, err = s.ReadRealtimeConsumerGroupWorkerHealth(ctx, ep, "orders", "billing")
	if err != nil || health.LiveWorkers != 2 || health.ReadyWorkers != 2 {
		t.Fatalf("health after revive = %+v, %v, want 2 ready", health, err)
	}
}

func TestPgStoreRealtimeConsumerGroupLagAlert(t *testing.T) {
	s, pool, ctx, ep := realtimeMetadataFixture(t, "-rt-group-alert", nil, nil, nil)
	g := createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 1})
	if _, err := s.SetRealtimeConsumerGroupAlert(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupAlertConfig{Enabled: true}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("alert without thresholds err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	if _, err := s.SetRealtimeConsumerGroupAlert(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupAlertConfig{Enabled: true, UnassignedThreshold: "5", DeadLetterThreshold: 1}); err != nil {
		t.Fatalf("SetRealtimeConsumerGroupAlert: %v", err)
	}
	evaluate := func() state.RealtimeConsumerGroupAlert {
		t.Helper()
		if _, err := pool.Exec(ctx, `update managed_realtime_consumer_group_alerts set next_check_at=clock_timestamp() where group_id=$1`, g.GroupID); err != nil {
			t.Fatal(err)
		}
		if n, err := s.EvaluateRealtimeConsumerGroupAlerts(ctx); err != nil || n != 1 {
			t.Fatalf("EvaluateRealtimeConsumerGroupAlerts = %d, %v, want 1", n, err)
		}
		a, err := s.ReadRealtimeConsumerGroupAlert(ctx, ep, "orders", "billing")
		if err != nil {
			t.Fatalf("ReadRealtimeConsumerGroupAlert: %v", err)
		}
		return a
	}

	a := evaluate()
	if a.Breached || a.Unassigned == nil || *a.Unassigned != "3" || a.DeadLetters == nil || *a.DeadLetters != 0 || a.ObservedAt == nil {
		t.Fatalf("alert below thresholds = %+v", a)
	}
	killMessages(t, s, ctx, ep, "handler_failed")
	a = evaluate()
	if !a.Breached || *a.DeadLetters != 1 || a.LastEventAt == nil {
		t.Fatalf("alert after a dead letter = %+v, want breached", a)
	}
	if err := s.ReplayRealtimeConsumerGroupDeadLetter(ctx, ep, "orders", "billing", "1"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	a = evaluate()
	if a.Breached || *a.DeadLetters != 0 {
		t.Fatalf("alert after replay = %+v, want recovered", a)
	}
}

func TestPgStoreRealtimeConsumerGroupDeduplication(t *testing.T) {
	s, _, ctx, ep := realtimeMetadataFixture(t, "-rt-group-dedup",
		map[string]string{"event_id": "a"}, map[string]string{"event_id": "a"}, map[string]string{"event_id": "b"}, nil)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{DeduplicationMetadataKey: "event_id", DeduplicationWindowSeconds: 60})
	if got := claimedSequences(claimAll(t, s, ctx, ep, "billing", true)); !slices.Equal(got, []string{"1", "3", "4"}) {
		t.Fatalf("claims = %v, want the duplicate event skipped", got)
	}
	groups, err := s.ListRealtimeConsumerGroups(ctx, ep, "orders")
	if err != nil || len(groups) != 1 || groups[0].DuplicateMessages != "1" || groups[0].DeduplicationKeys != 2 {
		t.Fatalf("group = %+v, %v, want 1 duplicate and 2 remembered keys", groups, err)
	}
}

func TestPgStoreRealtimeConsumerGroupOrdering(t *testing.T) {
	s, _, ctx, ep := realtimeMetadataFixture(t, "-rt-group-ordering",
		map[string]string{"customer": "a"}, map[string]string{"customer": "a"}, map[string]string{"customer": "b"})
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{OrderingMetadataKey: "customer"})
	first := claimSequence(t, s, ctx, ep, "worker-a")
	other := claimSequence(t, s, ctx, ep, "worker-b")
	if first.Message.Sequence != "1" || other.Message.Sequence != "3" {
		t.Fatalf("claims = %s, %s, want 1 then 3 (2 waits behind 1)", first.Message.Sequence, other.Message.Sequence)
	}
	blocked, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, "orders", "billing", "worker-c", 4)
	if err != nil || !blocked.OrderingBlocked || len(blocked.Claims) != 0 {
		t.Fatalf("claim behind a leased key = %+v, %v, want ordering-blocked", blocked, err)
	}
	if _, err := settle(s, ctx, ep, first, "complete"); err != nil {
		t.Fatalf("complete 1: %v", err)
	}
	if next := claimSequence(t, s, ctx, ep, "worker-c"); next.Message.Sequence != "2" {
		t.Fatalf("claim after completing 1 = %s, want 2", next.Message.Sequence)
	}
}

func TestPgStoreRealtimeConsumerGroupPriority(t *testing.T) {
	s, _, ctx, ep := realtimeMetadataFixture(t, "-rt-group-priority",
		nil, map[string]string{"priority": "9"}, map[string]string{"priority": "5"}, map[string]string{"priority": "high"})
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{PriorityMetadataKey: "priority"})
	// Higher priority first; missing or unparseable priority is 0, oldest first.
	if got := claimedSequences(claimAll(t, s, ctx, ep, "billing", false)); !slices.Equal(got, []string{"2", "3", "1", "4"}) {
		t.Fatalf("claims = %v, want [2 3 1 4]", got)
	}
	status, err := s.ReadRealtimeConsumerGroupPriority(ctx, ep, "orders", "billing")
	if err != nil || status.MetadataKey != "priority" {
		t.Fatalf("priority = %+v, %v", status, err)
	}
	leased := map[int]int{}
	for _, b := range status.Backlog {
		leased[b.Priority] = b.Leased
	}
	if leased[9] != 1 || leased[5] != 1 || leased[0] != 2 {
		t.Fatalf("priority backlog = %+v, want leased 9:1 5:1 0:2", status.Backlog)
	}
}

func TestPgStoreRealtimeConsumerGroupFilters(t *testing.T) {
	s, _, ctx, ep := realtimeMetadataFixture(t, "-rt-group-filter",
		map[string]string{"event_type": "order.created"},
		map[string]string{"event_type": "order.deleted"},
		nil,
		map[string]string{"event_type": "order.created", "region": "eu"})
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{Filter: &state.RealtimeConsumerGroupFilter{EventTypes: []string{"order.created"}}})
	createRealtimeGroup(t, s, ctx, ep, "eu-audit", state.RealtimeConsumerGroupConfig{Filter: &state.RealtimeConsumerGroupFilter{Metadata: map[string]string{"region": "eu"}}})
	if got := claimedSequences(claimAll(t, s, ctx, ep, "billing", true)); !slices.Equal(got, []string{"1", "4"}) {
		t.Fatalf("event-type filter claims = %v, want [1 4]", got)
	}
	if got := claimedSequences(claimAll(t, s, ctx, ep, "eu-audit", true)); !slices.Equal(got, []string{"4"}) {
		t.Fatalf("metadata filter claims = %v, want [4]", got)
	}
	groups, err := s.ListRealtimeConsumerGroups(ctx, ep, "orders")
	if err != nil {
		t.Fatal(err)
	}
	filtered := map[string]string{}
	for _, g := range groups {
		filtered[g.Name] = g.FilteredMessages
	}
	if filtered["billing"] != "2" || filtered["eu-audit"] != "3" {
		t.Fatalf("filtered counts = %v, want billing 2, eu-audit 3", filtered)
	}
}

func TestPgStoreRealtimeConsumerGroupDeliveryDeadlines(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	s, _, ctx, ep := realtimeMetadataFixture(t, "-rt-group-deadline",
		map[string]string{"deliver_by": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)},
		map[string]string{"deliver_by": "tomorrow"},
		map[string]string{"deliver_by": future.Format(time.RFC3339Nano)},
		nil)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{DeadlineMetadataKey: "deliver_by"})
	claims := claimAll(t, s, ctx, ep, "billing", false)
	if got := claimedSequences(claims); !slices.Equal(got, []string{"3", "4"}) {
		t.Fatalf("claims = %v, want only the deliverable messages [3 4]", got)
	}
	if claims[0].DeliveryDeadline == nil || !claims[0].DeliveryDeadline.Equal(future) || claims[1].DeliveryDeadline != nil {
		t.Fatalf("deadlines = %v, %v, want %s and none", claims[0].DeliveryDeadline, claims[1].DeliveryDeadline, future)
	}
	if dead := deadSequences(t, s, ctx, ep); dead["1"] != "delivery_expired" || dead["2"] != "invalid_delivery_deadline" || len(dead) != 2 {
		t.Fatalf("dead letters = %v, want 1 expired and 2 invalid", dead)
	}
}

func TestPgStoreRealtimeConsumerGroupDeadLetterPreviewAndBulkReplay(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-bulk", 4)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 1})
	killMessages(t, s, ctx, ep, "timeout", "bad_input", "timeout")
	held := claimSequence(t, s, ctx, ep, "worker-a") // sequence 4 stays leased

	preview, err := s.PreviewRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterPreviewRequest{FailureCodes: []string{"timeout"}})
	if err != nil || preview.HasMore || len(preview.Items) != 2 || preview.Items[0].Sequence != "1" || preview.Items[1].Sequence != "3" {
		t.Fatalf("timeout preview = %+v, %v, want sequences 1 and 3", preview, err)
	}
	page, err := s.PreviewRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterPreviewRequest{Limit: 1})
	if err != nil || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("limited preview = %+v, %v, want one item and more", page, err)
	}
	if _, err := s.BulkReplayRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterBulkReplayRequest{
		GroupID: "00000000-0000-4000-8000-000000000000", Items: []state.RealtimeDeadLetterReplaySelection{preview.Items[0].RealtimeDeadLetterReplaySelection},
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay against another group err = %v, want ErrConflict", err)
	}

	stale := state.RealtimeDeadLetterReplaySelection{Sequence: "2", DeadSince: time.Now().Add(-time.Hour)}
	leased := state.RealtimeDeadLetterReplaySelection{Sequence: held.Message.Sequence, DeadSince: time.Now()}
	result, err := s.BulkReplayRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterBulkReplayRequest{
		GroupID: preview.GroupID,
		Items:   []state.RealtimeDeadLetterReplaySelection{preview.Items[0].RealtimeDeadLetterReplaySelection, preview.Items[1].RealtimeDeadLetterReplaySelection, stale, leased},
	})
	if err != nil {
		t.Fatalf("BulkReplayRealtimeConsumerGroupDeadLetters: %v", err)
	}
	outcomes := map[string]string{}
	for _, o := range result.Outcomes {
		outcomes[o.Sequence] = o.Status
	}
	if result.Replayed != 2 || outcomes["1"] != "replayed" || outcomes["3"] != "replayed" || outcomes["2"] != "stale" || outcomes["4"] != "not_dead" {
		t.Fatalf("bulk replay = %+v, want 1 and 3 replayed, 2 stale, 4 not dead", result)
	}
	if dead := deadSequences(t, s, ctx, ep); len(dead) != 1 || dead["2"] != "bad_input" {
		t.Fatalf("dead letters after replay = %v, want only 2", dead)
	}
	for _, c := range claimAll(t, s, ctx, ep, "billing", false) {
		if c.Attempt != 1 {
			t.Fatalf("replayed claim %s attempt = %d, want a fresh budget", c.Message.Sequence, c.Attempt)
		}
	}
}

func TestPgStoreRealtimeConsumerGroupDeadLetterExport(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-export", 3)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 1})
	killMessages(t, s, ctx, ep, "timeout", "bad_input", "timeout")

	first, err := s.ExportRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterExportRequest{Limit: 2})
	if err != nil {
		t.Fatalf("export first page: %v", err)
	}
	m := first.Manifest
	if m.Count != 2 || !m.HasMore || m.ThroughSequence != "3" || m.GroupID == "" || len(first.Records) != 2 || first.Records[0].Sequence != "1" || first.Records[1].FailureCode != "bad_input" {
		t.Fatalf("first page = %+v", first)
	}
	// Resuming pins the first page's snapshot and upper bound.
	rest, err := s.ExportRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterExportRequest{
		GroupID: m.GroupID, AfterSequence: first.Records[1].Sequence, ThroughSequence: m.ThroughSequence, SnapshotAt: &m.SnapshotAt, Limit: 2,
	})
	if err != nil || rest.Manifest.HasMore || len(rest.Records) != 1 || rest.Records[0].Sequence != "3" {
		t.Fatalf("resumed page = %+v, %v, want sequence 3 only", rest, err)
	}
	if _, err := s.ExportRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterExportRequest{GroupID: m.GroupID}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("resume without a snapshot err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	filtered, err := s.ExportRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing", state.RealtimeDeadLetterExportRequest{FailureCodes: []string{"bad_input"}})
	if err != nil || len(filtered.Records) != 1 || filtered.Records[0].Sequence != "2" {
		t.Fatalf("filtered export = %+v, %v, want sequence 2", filtered, err)
	}
}

func TestPgStoreRealtimeReplayJobDryRunThenStart(t *testing.T) {
	s, pool, ctx, ep := realtimeMetadataFixture(t, "-rt-group-replay-job", nil, nil, nil)
	createRealtimeGroup(t, s, ctx, ep, "billing", state.RealtimeConsumerGroupConfig{MaxAttempts: 1})
	killMessages(t, s, ctx, ep, "timeout", "timeout")
	if _, err := s.CreateRealtimeReplayJob(ctx, ep, "orders", "billing", state.RealtimeReplayJobRequest{}); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("zero-rate job err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
	job := func(id string) state.RealtimeReplayJob {
		t.Helper()
		jobs, err := s.ListRealtimeReplayJobs(ctx, ep, "orders", "billing")
		if err != nil {
			t.Fatalf("ListRealtimeReplayJobs: %v", err)
		}
		for _, j := range jobs {
			if j.ID == id {
				return j
			}
		}
		t.Fatalf("job %s not listed in %+v", id, jobs)
		return state.RealtimeReplayJob{}
	}
	// step makes every job due regardless of its rate, then runs one pass.
	step := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `update managed_realtime_replay_jobs set next_at=clock_timestamp()`); err != nil {
			t.Fatal(err)
		}
		if err := s.RunRealtimeReplayJobs(ctx); err != nil {
			t.Fatalf("RunRealtimeReplayJobs: %v", err)
		}
	}

	dry, err := s.CreateRealtimeReplayJob(ctx, ep, "orders", "billing", state.RealtimeReplayJobRequest{DryRun: true, Rate: 32})
	if err != nil || dry.State != "checking" || dry.Selected != 2 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if _, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", dry.ID, "start"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("start before the review finished err = %v, want ErrConflict", err)
	}
	step()
	step()
	if dry = job(dry.ID); dry.State != "completed" || dry.Eligible != 2 || dry.Replayed != 0 {
		t.Fatalf("dry run after passes = %+v, want completed with 2 eligible", dry)
	}
	if dead := deadSequences(t, s, ctx, ep); len(dead) != 2 {
		t.Fatalf("a dry run replayed messages: dead = %v", dead)
	}

	started, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", dry.ID, "start")
	if err != nil || started.State != "running" || started.ReviewedJobID == nil || *started.ReviewedJobID != dry.ID {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if again, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", dry.ID, "start"); err != nil || again.ID != started.ID {
		t.Fatalf("second start = %+v, %v, want the same job %s", again, err, started.ID)
	}
	if paused, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", started.ID, "pause"); err != nil || paused.State != "paused" {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	step()
	if j := job(started.ID); j.Processed != 0 {
		t.Fatalf("paused job progressed: %+v", j)
	}
	if _, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", started.ID, "resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	step()
	step()
	if j := job(started.ID); j.State != "completed" || j.Replayed != 2 {
		t.Fatalf("replay job = %+v, want completed with 2 replayed", j)
	}
	if dead := deadSequences(t, s, ctx, ep); len(dead) != 0 {
		t.Fatalf("dead letters after replay job = %v, want none", dead)
	}
	if _, err := s.ControlRealtimeReplayJob(ctx, ep, "orders", "billing", started.ID, "cancel"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancel completed job err = %v, want ErrConflict", err)
	}
}
