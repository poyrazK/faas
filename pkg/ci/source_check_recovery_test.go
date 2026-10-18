package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func sourceCheckFixture(t *testing.T) (*jobsHarness, ExecutionProfile, Config, AutomationEventSummary) {
	t.Helper()
	h, profile, cfg := automationFixture(t)
	cfg.GitHubChecksEnabled, cfg.ExecutionChecksEnabled = true, true
	pathPolicy(t, h, profile, cfg, false, false)
	return h, profile, cfg, captureSkippedCheck(t, h, cfg, 1)
}

func captureSkippedCheck(t *testing.T, h *jobsHarness, cfg Config, n int) AutomationEventSummary {
	t.Helper()
	base, head := strings.Repeat("a", 40), fmt.Sprintf("%040x", n)
	capturePathPush(t, h, cfg, base, head)
	if worked, err := AdmitAutomationWithResolvers(t.Context(), cfg, h.store, h.verify, AutomationResolvers{Changes: resolvePathChanges(pathChanges(base, head, ChangedFile{Path: "docs/private-file", Status: "modified"}))}); err != nil || !worked {
		t.Fatalf("skip source: %v %v", worked, err)
	}
	return pathEvent(t, h, head)
}

func exhaustSourceCheck(t *testing.T, h *jobsHarness, id string) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET status='dead',attempts=$2,last_error='unavailable',lease_token=NULL,lease_until=NULL WHERE event_id=$1", id, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
}

func TestCISourceCheckRecoveryRestartPreservesSkipAndRemoteIdentity(t *testing.T) {
	h, profile, cfg, event := sourceCheckFixture(t)
	var before []byte
	if err := h.pool.QueryRow(t.Context(), "SELECT row_to_json(e)::text FROM ci_automation_events e WHERE id=$1", event.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var original ExecutionCheck
	for range api.CIGitHubMaxAttempts {
		worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) {
			original = c
			return 0, errors.New("private-provider-token")
		})
		if !worked || err == nil || strings.Contains(err.Error(), "private-provider-token") {
			t.Fatal("delivery failure was not bounded/redacted")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET next_attempt_at=clock_timestamp()"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, event.ID)
	if err != nil || !view.Retryable || view.Attempts != api.CIGitHubMaxAttempts || view.LastError != "publish_failed" || view.CommitSHA != event.CommitSHA {
		t.Fatalf("exhausted status: %+v %v", view, err)
	}
	// Feedback survives current automation/profile/binding disablement and a plan
	// downgrade. Those controls cannot turn the original skip into execution.
	if _, err := h.store.DisableAutomationPolicy(t.Context(), h.account, h.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DisableExecutionProfile(t.Context(), h.account, profile.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DisableGitHubBinding(t.Context(), h.account, h.binding.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", h.account); err != nil {
		t.Fatal(err)
	}
	cfg.AutomationEnabled, cfg.DispatchEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = false, false, false, false
	receipt, replay, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "lost-request-response", CheckRetryRequest{})
	if err != nil || replay || receipt.Generation != 1 || receipt.AppliedAt != nil {
		t.Fatalf("retry intent: %+v %v %v", receipt, replay, err)
	}
	view, err = h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, event.ID)
	if err != nil || !view.RetryPending || view.Retryable || view.RetryGeneration != 0 || view.DeliveryStatus != "dead" {
		t.Fatal("apid mutated publisher state")
	}
	if healthQueue(t, readHealth(t, h, h.account), "check_retries").Pending != 1 {
		t.Fatal("pending source recovery missing from health")
	}
	h.restart()
	again, replay, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "lost-request-response", CheckRetryRequest{})
	if err != nil || !replay || again.ID != receipt.ID {
		t.Fatal("restart duplicated retry intent")
	}
	// A prior remote identity is retained even after the budget is exhausted.
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET check_id=77 WHERE event_id=$1", event.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) {
		want := original
		want.CheckID = 77
		if !reflect.DeepEqual(want, c) || c.ExternalID() != "gregale-ci-source:"+event.ID {
			t.Fatal("retry replaced immutable Check projection")
		}
		return 77, nil
	}); err != nil || !worked {
		t.Fatalf("publisher recovery: %v %v", worked, err)
	}
	view, err = h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, event.ID)
	if err != nil || view.RetryGeneration != 1 || view.RetryPending || view.DeliveryStatus != "sent" || view.LastRetry == nil || view.LastRetry.AppliedAt == nil || view.CheckID != 77 {
		t.Fatalf("applied receipt: %+v %v", view, err)
	}
	again, replay, err = RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "lost-request-response", CheckRetryRequest{})
	if err != nil || !replay || again.ID != receipt.ID || again.AppliedAt == nil {
		t.Fatal("applied receipt no longer replays")
	}
	var after []byte
	if err := h.pool.QueryRow(t.Context(), "SELECT row_to_json(e)::text FROM ci_automation_events e WHERE id=$1", event.ID).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("feedback recovery mutated source decision")
	}
	if healthQueue(t, readHealth(t, h, h.account), "check_retries").Pending != 0 || h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM job_runs") != 0 || h.count(t, "SELECT count(*) FROM instances") != 0 {
		t.Fatal("feedback recovery created compute or retained pending intent")
	}
}

func TestCISourceCheckRecoveryConcurrentKeysAndLifetimeBound(t *testing.T) {
	h, _, cfg, event := sourceCheckFixture(t)
	exhaustSourceCheck(t, h, event.ID)
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			_, replay, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "same-key", CheckRetryRequest{})
			if err != nil {
				t.Error(err)
			}
			if !replay {
				created.Add(1)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 || h.count(t, "SELECT count(*) FROM ci_source_check_retries") != 1 {
		t.Fatal("concurrent requests duplicated intent")
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "different-key", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("second pending intent accepted")
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "same-key", CheckRetryRequest{ExpectedRetryGeneration: 1}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("durable key redirected to another generation")
	}
	other := captureSkippedCheck(t, h, cfg, 2)
	exhaustSourceCheck(t, h, other.ID)
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, other.ID, "same-key", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("key redirected to another source")
	}
	for generation := int32(0); generation < api.CICheckRetryMaxGenerations; generation++ {
		if generation > 0 {
			if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, uuid.NewString(), CheckRetryRequest{ExpectedRetryGeneration: generation}); err != nil {
				t.Fatal(err)
			}
		}
		var published atomic.Int32
		for range 2 {
			wg.Go(func() {
				if _, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) { published.Add(1); return 77, nil }); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if published.Load() != 1 {
			t.Fatal("concurrent publishers duplicated or lost recovered feedback")
		}
		exhaustSourceCheck(t, h, event.ID)
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "stale", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("stale generation accepted")
	}
	_, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "over-budget", CheckRetryRequest{ExpectedRetryGeneration: api.CICheckRetryMaxGenerations})
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "source_check_retry_generations" || h.count(t, "SELECT count(*) FROM ci_source_check_retries") != api.CICheckRetryMaxGenerations {
		t.Fatal("lifetime retry budget bypassed")
	}
}

func TestCISourceCheckRecoveryVisibilityAndPermanentStates(t *testing.T) {
	h, _, cfg, event := sourceCheckFixture(t)
	for _, status := range []string{"pending", "sending", "sent", "ignored", "dead"} {
		_, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET status=$2,attempts=8,last_error=CASE WHEN $2='ignored' THEN 'access_denied' ELSE 'unavailable' END,check_id=77,lease_token=CASE WHEN $2='sending' THEN $3::uuid ELSE NULL END,lease_until=CASE WHEN $2='sending' THEN clock_timestamp()+interval '1 minute' ELSE NULL END WHERE event_id=$1", event.ID, status, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		view, err := h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, event.ID)
		if err != nil || view.DeliveryStatus != status || view.Retryable != (status == "dead") || (view.LeaseExpiresAt != nil) != (status == "sending") {
			t.Fatalf("status: %+v %v", view, err)
		}
		if status != "dead" {
			if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, uuid.NewString(), CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
				t.Fatal("non-exhausted delivery retried")
			}
		}
	}
	off := cfg
	off.ExecutionChecksEnabled = false
	missing := captureSkippedCheck(t, h, off, 2)
	view, err := h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, missing.ID)
	if err != nil || view.DeliveryStatus != "not_requested" || view.Retryable || view.LastRetry != nil {
		t.Fatal("uncaptured feedback missing from status")
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, missing.ID, "must-not-capture", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("recovery manufactured uncaptured Check")
	}
	cfg.accounts[h.other] = true
	for _, scope := range []struct{ account, binding, event string }{{h.other, h.binding.ID, event.ID}, {h.account, uuid.NewString(), event.ID}, {h.account, h.binding.ID, uuid.NewString()}} {
		if _, err := h.store.SourceCheckDelivery(t.Context(), scope.account, scope.binding, scope.event); !errors.Is(err, ErrNotFound) {
			t.Fatal("status escaped source ownership")
		}
		if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, scope.account, scope.binding, scope.event, "foreign", CheckRetryRequest{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("retry escaped source ownership: %v", err)
		}
	}
	rows, err := h.store.SourceCheckDeliveries(t.Context(), h.account, h.binding.ID)
	if err != nil || len(rows) != 2 {
		t.Fatal("source delivery list incomplete")
	}
	if h.count(t, "SELECT count(*) FROM ci_source_check_retries") != 0 {
		t.Fatal("visibility changed retry intent")
	}
	for _, id := range []string{"", "../outside", "not-a-uuid"} {
		if _, err := h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, id); !errors.Is(err, ErrNotFound) {
			t.Fatal("malformed event ID became a list selector")
		}
	}
}

func TestCISourceCheckRecoveryFreshAccessGatesAndAtomicFailure(t *testing.T) {
	h, _, cfg, event := sourceCheckFixture(t)
	exhaustSourceCheck(t, h, event.ID)
	for _, mode := range []string{"internal", "account", "github", "checks", "execution_checks", "unavailable", "denied", "nil_verifier", "suspended", "projection"} {
		t.Run(mode, func(t *testing.T) {
			local := cfg
			verify := h.verify
			want := ErrDispatchDisabled
			called := false
			switch mode {
			case "internal":
				local.Enabled = false
			case "account":
				local.accounts = nil
			case "github":
				local.GitHubEnabled = false
			case "checks":
				local.GitHubChecksEnabled = false
			case "execution_checks":
				local.ExecutionChecksEnabled = false
			case "nil_verifier":
				verify = nil
				want = ErrGitHubUnavailable
			default:
				want = ErrGitHubUnavailable
				verify = func(ctx context.Context, _ string, _ int64, _ int64, _ string) error {
					called = true
					switch mode {
					case "denied":
						return ErrGitHubAccess
					case "suspended":
						_, err := h.pool.Exec(ctx, "UPDATE accounts SET status='suspended' WHERE id=$1", h.account)
						return err
					case "projection":
						_, err := h.pool.Exec(ctx, "UPDATE ci_automation_path_checks SET snapshot=jsonb_set(snapshot,'{CommitSHA}',to_jsonb($2::text)) WHERE event_id=$1", event.ID, strings.Repeat("e", 40))
						return err
					}
					return errors.New("private-provider-token")
				}
				if mode == "denied" || mode == "suspended" {
					want = ErrGitHubAccess
				}
				if mode == "projection" {
					want = ErrConflict
				}
			}
			_, _, err := RequestSourceCheckRetry(t.Context(), local, h.store, verify, h.account, h.binding.ID, event.ID, "reusable-after-failure", CheckRetryRequest{})
			if !errors.Is(err, want) || (err != nil && strings.Contains(err.Error(), "private-provider-token")) {
				t.Fatalf("access/gate: %v", err)
			}
			if errors.Is(want, ErrDispatchDisabled) && called {
				t.Fatal("disabled retry called provider")
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", h.account); err != nil {
				t.Fatal(err)
			}
			if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET snapshot=jsonb_set(snapshot,'{CommitSHA}',to_jsonb($2::text)) WHERE event_id=$1", event.ID, event.CommitSHA); err != nil {
				t.Fatal(err)
			}
			if h.count(t, "SELECT count(*) FROM ci_source_check_retries") != 0 {
				t.Fatal("failed retry reserved intent/key")
			}
		})
	}
	if _, err := h.pool.Exec(t.Context(), "ALTER TABLE ci_source_check_retries ADD CONSTRAINT reject_test_retry CHECK(false)"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "reusable-after-failure", CheckRetryRequest{}); err == nil {
		t.Fatal("journal failure accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "ALTER TABLE ci_source_check_retries DROP CONSTRAINT reject_test_retry"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "reusable-after-failure", CheckRetryRequest{}); err != nil {
		t.Fatal("rollback reserved request key")
	}
	data, err := os.ReadFile("../../migrations/20261017111942534_ci_source_check_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil || !strings.Contains(err.Error(), "retain CI source Check retry") {
		t.Fatal("rollback discarded recovery evidence")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil || h.count(t, "SELECT count(*) FROM ci_source_check_retries") != 0 {
		t.Fatal("account deletion retained source recovery")
	}
}

func TestCISourceCheckRecoveryPublisherLockAndStaleLease(t *testing.T) {
	h, _, cfg, event := sourceCheckFixture(t)
	claim, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET attempts=8,lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1", event.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "too-early", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("apid expired worker lease")
	}
	if _, err := h.store.claimExecutionCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
		t.Fatal("exhausted lease not retired")
	}
	tx, err := h.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM ci_automation_path_checks WHERE event_id=$1 FOR UPDATE", event.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := RequestSourceCheckRetry(ctx, cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "locked", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatalf("retry waited for publisher lock: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestSourceCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, event.ID, "recover", CheckRetryRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.deliverExecutionCheck(t.Context(), claim, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Fatal("expired publisher performed remote work")
		return 77, nil
	}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale lease unfenced: %v", err)
	}
	// Verification is repeated by the worker after request-time access succeeds.
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, func(context.Context, string, int64, int64, string) error { return ErrGitHubAccess }, func(context.Context, ExecutionCheck) (int64, error) {
		t.Fatal("publisher bypassed fresh access")
		return 77, nil
	}); err != nil || !worked {
		t.Fatal("revoked access did not settle safely")
	}
	view, err := h.store.SourceCheckDelivery(t.Context(), h.account, h.binding.ID, event.ID)
	if err != nil || view.DeliveryStatus != "ignored" || view.LastError != "access_denied" || view.Retryable {
		raw, _ := json.Marshal(view)
		t.Fatalf("revocation: %s %v", raw, err)
	}
}
