package state_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// realtimeGroupFixture is an enabled endpoint with n retained messages on
// channel "orders".
func realtimeGroupFixture(t *testing.T, suffix string, n int) (*state.PgStore, context.Context, string) {
	t.Helper()
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, suffix)
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	for i := 1; i <= n; i++ {
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "orders", []byte("order-"+strconv.Itoa(i)), false, ""); err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
	}
	return s, ctx, endpoint.ID
}

func claimSequence(t *testing.T, s *state.PgStore, ctx context.Context, ep, worker string) *state.RealtimeConsumerGroupClaim {
	t.Helper()
	claim, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", worker)
	if err != nil {
		t.Fatalf("ClaimRealtimeConsumerGroup: %v", err)
	}
	if claim == nil {
		t.Fatal("ClaimRealtimeConsumerGroup returned no claim")
	}
	return claim
}

func settle(s *state.PgStore, ctx context.Context, ep string, claim *state.RealtimeConsumerGroupClaim, action string) (state.RealtimeConsumerGroupSettlementResult, error) {
	req := state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: action}
	if action == "retry" {
		zero := 0
		req.RetryAfterSeconds = &zero
		req.FailureCode = "handler_failed"
	}
	return s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", req)
}

func TestPgStoreRealtimeConsumerGroupLifecycle(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-lifecycle", 2)
	if _, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupConfig{AfterSequence: "0", MaxAttempts: 2}); err != nil {
		t.Fatalf("CreateRealtimeConsumerGroup: %v", err)
	}
	if _, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupConfig{AfterSequence: "0"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate group err = %v, want ErrConflict", err)
	}

	first := claimSequence(t, s, ctx, ep, "worker-a")
	if first.Message.Sequence != "1" || first.Attempt != 1 || first.ClaimToken == "" || first.FencingToken == "" {
		t.Fatalf("first claim = %+v, want sequence 1 attempt 1 with tokens", first)
	}
	if res, err := settle(s, ctx, ep, first, "complete"); err != nil || res.Status != "completed" {
		t.Fatalf("complete = %+v, %v", res, err)
	}
	if _, err := settle(s, ctx, ep, first, "complete"); !errors.Is(err, state.ErrRealtimeConsumerGroupLease) {
		t.Fatalf("second complete err = %v, want ErrRealtimeConsumerGroupLease", err)
	}

	// Retry until the attempt budget is exhausted, then the message is dead.
	second := claimSequence(t, s, ctx, ep, "worker-a")
	if second.Message.Sequence != "2" {
		t.Fatalf("second claim sequence = %s, want 2", second.Message.Sequence)
	}
	if res, err := settle(s, ctx, ep, second, "retry"); err != nil || res.Status != "ready" {
		t.Fatalf("first retry = %+v, %v", res, err)
	}
	again := claimSequence(t, s, ctx, ep, "worker-b")
	if again.Message.Sequence != "2" || again.Attempt != 2 {
		t.Fatalf("reclaim = %+v, want sequence 2 attempt 2", again)
	}
	if again.FencingToken == second.FencingToken {
		t.Fatalf("fencing token did not advance: %s", again.FencingToken)
	}
	if res, err := settle(s, ctx, ep, again, "retry"); err != nil || res.Status != "dead" {
		t.Fatalf("final retry = %+v, %v, want dead", res, err)
	}
	if claim, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-a"); err != nil || claim != nil {
		t.Fatalf("claim after dead = %+v, %v, want empty", claim, err)
	}

	letters, err := s.ListRealtimeConsumerGroupDeadLetters(ctx, ep, "orders", "billing")
	if err != nil {
		t.Fatalf("ListRealtimeConsumerGroupDeadLetters: %v", err)
	}
	if len(letters) != 1 || letters[0].Message.Sequence != "2" || letters[0].FailureCode != "handler_failed" || letters[0].Attempts != 2 {
		t.Fatalf("dead letters = %+v", letters)
	}
	if err := s.ReplayRealtimeConsumerGroupDeadLetter(ctx, ep, "orders", "billing", "2"); err != nil {
		t.Fatalf("ReplayRealtimeConsumerGroupDeadLetter: %v", err)
	}
	replayed := claimSequence(t, s, ctx, ep, "worker-a")
	if replayed.Message.Sequence != "2" || replayed.Attempt != 1 {
		t.Fatalf("replayed claim = %+v, want sequence 2 attempt 1", replayed)
	}
	if _, err := settle(s, ctx, ep, replayed, "complete"); err != nil {
		t.Fatalf("complete replayed: %v", err)
	}

	groups, err := s.ListRealtimeConsumerGroups(ctx, ep, "orders")
	if err != nil {
		t.Fatalf("ListRealtimeConsumerGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].Completed != "2" || groups[0].Ready != 0 || groups[0].Leased != 0 || groups[0].Dead != 0 || groups[0].Unassigned != "0" {
		t.Fatalf("group status = %+v", groups)
	}
	if err := s.DeleteRealtimeConsumerGroup(ctx, ep, "orders", "billing"); err != nil {
		t.Fatalf("DeleteRealtimeConsumerGroup: %v", err)
	}
	if _, err := s.ClaimRealtimeConsumerGroup(ctx, ep, "orders", "billing", "worker-a"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim after delete err = %v, want ErrNotFound", err)
	}
}

func TestPgStoreRealtimeConsumerGroupReleaseRenewAndIdempotentSettlement(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-settle", 1)
	if _, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupConfig{AfterSequence: "0"}); err != nil {
		t.Fatalf("CreateRealtimeConsumerGroup: %v", err)
	}
	claim := claimSequence(t, s, ctx, ep, "worker-a")
	renewed, err := settle(s, ctx, ep, claim, "renew")
	if err != nil || renewed.Status != "leased" || renewed.LeaseUntil == nil || renewed.LeaseUntil.Before(claim.LeaseUntil) {
		t.Fatalf("renew = %+v, %v", renewed, err)
	}
	if res, err := settle(s, ctx, ep, claim, "release"); err != nil || res.Status != "ready" {
		t.Fatalf("release = %+v, %v", res, err)
	}
	// Release returns the attempt: the next claim is attempt 1 again.
	claim = claimSequence(t, s, ctx, ep, "worker-b")
	if claim.Attempt != 1 {
		t.Fatalf("attempt after release = %d, want 1", claim.Attempt)
	}

	key := uuid.NewString()
	req := state.RealtimeConsumerGroupSettlement{IdempotencyKey: key, Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "complete"}
	first, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", req)
	if err != nil || first.Status != "completed" {
		t.Fatalf("keyed complete = %+v, %v", first, err)
	}
	replay, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", req)
	if err != nil || replay.Status != "completed" {
		t.Fatalf("keyed replay = %+v, %v, want the saved result", replay, err)
	}
	changed := req
	changed.Action = "retry"
	if _, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", changed); err == nil {
		t.Fatal("reusing an idempotency key for a different settlement succeeded")
	}
	bad := state.RealtimeConsumerGroupSettlement{Sequence: "1", ClaimToken: "not-a-token", Action: "complete"}
	if _, err := s.SettleRealtimeConsumerGroup(ctx, ep, "orders", "billing", bad); !errors.Is(err, state.ErrRealtimeConsumerGroupInvalid) {
		t.Fatalf("malformed token err = %v, want ErrRealtimeConsumerGroupInvalid", err)
	}
}

func TestPgStoreRealtimeConsumerGroupBatchAndPause(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-group-batch", 5)
	if _, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupConfig{AfterSequence: "0"}); err != nil {
		t.Fatalf("CreateRealtimeConsumerGroup: %v", err)
	}
	batch, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, "orders", "billing", "worker-a", 3)
	if err != nil {
		t.Fatalf("ClaimRealtimeConsumerGroupBatch: %v", err)
	}
	if len(batch.Claims) != 3 {
		t.Fatalf("batch claims = %d, want 3", len(batch.Claims))
	}
	seen := map[string]bool{}
	for i, claim := range batch.Claims {
		if want := strconv.Itoa(i + 1); claim.Message.Sequence != want {
			t.Fatalf("claim %d sequence = %s, want %s", i, claim.Message.Sequence, want)
		}
		if seen[claim.ClaimToken] {
			t.Fatal("batch reused a claim token")
		}
		seen[claim.ClaimToken] = true
	}

	paused, err := s.SetRealtimeConsumerGroupPaused(ctx, ep, "orders", "billing", true)
	if err != nil || !paused.Paused {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	blocked, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, "orders", "billing", "worker-a", 2)
	if err != nil || !blocked.Paused || len(blocked.Claims) != 0 {
		t.Fatalf("claim while paused = %+v, %v", blocked, err)
	}
	// Leases issued before the pause stay valid.
	if _, err := settle(s, ctx, ep, &batch.Claims[0], "complete"); err != nil {
		t.Fatalf("complete while paused: %v", err)
	}
	if _, err := s.SetRealtimeConsumerGroupPaused(ctx, ep, "orders", "billing", false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	rest, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, "orders", "billing", "worker-b", 16)
	if err != nil || len(rest.Claims) != 2 || rest.Claims[0].Message.Sequence != "4" {
		t.Fatalf("claims after resume = %+v, %v, want sequences 4 and 5", rest, err)
	}
}
