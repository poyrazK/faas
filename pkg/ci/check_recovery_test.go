package ci

import (
	"context"
	"encoding/json"
	"errors"
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

func exhaustCheckDelivery(t *testing.T, h *jobsHarness, id string) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), `UPDATE ci_execution_checks SET status='dead',attempts=$2,last_error='unavailable',lease_token=NULL,lease_until=NULL WHERE execution_id=$1`, id, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
}

func TestCICheckRecoveryPublishesLatestWithoutRerunningExecution(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "recover")
	publishExecutionCheckTest(t, h, "queued", "")
	before, err := h.store.CancelExecution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range api.CIGitHubMaxAttempts {
		worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) { return 0, ErrGitHubUnavailable })
		if !worked || err == nil {
			t.Fatal("publication did not fail")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET next_attempt_at=clock_timestamp()"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := h.store.CheckDelivery(t.Context(), h.account, row.ID)
	if err != nil || !view.Retryable || !view.VersionPending || view.CheckID != 77 || view.DeliveryStatus != "dead" {
		t.Fatalf("exhaustion diagnostics: %+v %v", view, err)
	}
	receipt, replay, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "recover", CheckRetryRequest{})
	if err != nil || replay || receipt.Generation != 1 || receipt.AppliedAt != nil {
		t.Fatalf("request: %+v %v %v", receipt, replay, err)
	}
	view, err = h.store.CheckDelivery(t.Context(), h.account, row.ID)
	if err != nil || !view.RetryPending || view.Retryable || view.DeliveryStatus != "dead" {
		t.Fatal("apid changed publisher bookkeeping or lost pending intent")
	}
	h.restart()
	again, replay, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "recover", CheckRetryRequest{})
	if err != nil || !replay || again.ID != receipt.ID {
		t.Fatal("request response loss duplicated retry")
	}
	off := h.backend.Config
	off.ExecutionChecksEnabled = false
	if worked, err := PublishExecutionCheckOnce(t.Context(), off, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Error("disabled worker published")
		return 77, nil
	}); err != nil || worked {
		t.Fatal("publication gate bypassed")
	}
	completed := publishExecutionCheckTest(t, h, "completed", "cancelled")
	if completed.CheckID != 77 {
		t.Fatal("recovery replaced remote Check identity")
	}
	view, err = h.store.CheckDelivery(t.Context(), h.account, row.ID)
	if err != nil || view.RetryGeneration != 1 || view.RetryPending || view.VersionPending || view.LastRetry == nil || view.LastRetry.AppliedAt == nil {
		t.Fatalf("recovery receipt: %+v %v", view, err)
	}
	again, replay, err = RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "recover", CheckRetryRequest{})
	if err != nil || !replay || again.ID != receipt.ID || again.AppliedAt == nil {
		t.Fatal("applied retry could not replay safely")
	}
	after, err := h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil || !reflect.DeepEqual(before, after) || h.count(t, "SELECT count(*) FROM job_runs") != 0 || h.count(t, "SELECT count(*) FROM instances") != 0 {
		t.Fatal("publication retry changed execution or compute intent")
	}
}

func TestCICheckRecoveryConcurrentRequestsAndLifetimeBound(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "concurrent")
	exhaustCheckDelivery(t, h, row.ID)
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			_, replay, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "same-key", CheckRetryRequest{})
			if err != nil {
				t.Error(err)
			}
			if !replay {
				created.Add(1)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 || h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != 1 {
		t.Fatal("concurrent retry requests duplicated intent")
	}
	if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "different-key", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("different key admitted a second pending retry")
	}
	for generation := int32(0); generation < api.CICheckRetryMaxGenerations; generation++ {
		if generation > 0 {
			if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, uuid.NewString(), CheckRetryRequest{ExpectedRetryGeneration: generation}); err != nil {
				t.Fatal(err)
			}
		}
		if worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) { return 77, nil }); err != nil || !worked {
			t.Fatalf("recovery cycle %d: %v %v", generation, worked, err)
		}
		exhaustCheckDelivery(t, h, row.ID)
	}
	if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "stale", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("stale generation admitted retry")
	}
	_, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "over-limit", CheckRetryRequest{ExpectedRetryGeneration: api.CICheckRetryMaxGenerations})
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Limit != api.CICheckRetryMaxGenerations || h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != api.CICheckRetryMaxGenerations {
		t.Fatal("retry journal exceeded lifetime bound")
	}
	other := h.admit(t, "other")
	exhaustCheckDelivery(t, h, other.ID)
	if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, other.ID, "same-key", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatal("request key redirected to another execution")
	}
}

func TestCICheckRecoveryVisibilityAndNonRetryableStates(t *testing.T) {
	h := executionCheckFixture(t)
	for _, status := range []string{"pending", "sending", "sent", "ignored", "not_requested", "dead"} {
		if status == "not_requested" {
			h.backend.Config.ExecutionChecksEnabled = false
		}
		row := h.admit(t, status)
		h.backend.Config.ExecutionChecksEnabled = true
		switch status {
		case "sending":
			if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='sending',lease_token=$2,lease_until=clock_timestamp()+interval '1 minute' WHERE execution_id=$1", row.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		case "sent":
			if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='sent' WHERE execution_id=$1", row.ID); err != nil {
				t.Fatal(err)
			}
		case "ignored":
			if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='ignored',last_error='access_denied' WHERE execution_id=$1", row.ID); err != nil {
				t.Fatal(err)
			}
		case "dead":
			exhaustCheckDelivery(t, h, row.ID)
		}
		view, err := h.store.CheckDelivery(t.Context(), h.account, row.ID)
		if err != nil || view.DeliveryStatus != status || view.Retryable != (status == "dead") {
			t.Fatalf("diagnostics %s: %+v %v", status, view, err)
		}
		if status != "dead" {
			if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, uuid.NewString(), CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
				t.Fatalf("nonexhausted %s retried: %v", status, err)
			}
		}
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	views, err := h.store.CheckDeliveries(t.Context(), h.account)
	if err != nil || len(views) != 6 {
		t.Fatalf("list visibility: %d %v", len(views), err)
	}
	raw, _ := json.Marshal(views)
	for _, private := range []string{"lease_token", "key_digest", "installation_id", "private/", "synthetic build", "commands are stored"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("diagnostics exposed %s", private)
		}
	}
	if _, err := h.store.CheckDelivery(t.Context(), h.other, views[0].ExecutionID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign account read delivery")
	}
	if views, err := h.store.CheckDeliveries(t.Context(), h.other); err != nil || len(views) != 0 {
		t.Fatal("foreign account listed delivery")
	}
}

func TestCICheckRecoveryRechecksAuthorityAndGates(t *testing.T) {
	for _, name := range []string{"internal", "github", "checks", "execution_checks", "allowlist", "nil_verifier", "repository", "account"} {
		t.Run(name, func(t *testing.T) {
			h := executionCheckFixture(t)
			row := h.admit(t, "gates")
			exhaustCheckDelivery(t, h, row.ID)
			cfg := h.backend.Config
			verify := h.verify
			want := ErrDispatchDisabled
			switch name {
			case "internal":
				cfg.Enabled = false
			case "github":
				cfg.GitHubEnabled = false
			case "checks":
				cfg.GitHubChecksEnabled = false
			case "execution_checks":
				cfg.ExecutionChecksEnabled = false
			case "allowlist":
				cfg.accounts = map[string]bool{}
			case "nil_verifier":
				verify = nil
				want = ErrGitHubUnavailable
			case "repository":
				verify = func(context.Context, string, int64, int64, string) error { return ErrGitHubAccess }
				want = ErrGitHubAccess
			case "account":
				verify = func(ctx context.Context, _ string, _, _ int64, _ string) error {
					_, err := h.pool.Exec(ctx, "UPDATE accounts SET abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", h.account)
					return err
				}
				want = ErrGitHubAccess
			}
			if _, _, err := RequestCheckRetry(t.Context(), cfg, h.store, verify, h.account, row.ID, "gate", CheckRetryRequest{}); !errors.Is(err, want) {
				t.Fatalf("gate %s: %v", name, err)
			}
			if h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != 0 {
				t.Fatal("revoked authority retained retry intent")
			}
		})
	}
}

func TestCICheckRecoveryDoesNotWaitForActivePublisher(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "active")
	claim, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.store.deliverExecutionCheck(t.Context(), claim, h.verify, func(context.Context, ExecutionCheck) (int64, error) { close(started); <-release; return 77, nil })
	}()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := RequestCheckRetry(ctx, h.backend.Config, h.store, h.verify, h.account, row.ID, "busy", CheckRetryRequest{}); !errors.Is(err, ErrCheckRetryConflict) {
		t.Fatalf("retry blocked or bypassed publisher: %v", err)
	}
}

func TestCICheckRecoveryAuditAtomicityRollbackAndDeletion(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "audit")
	exhaustCheckDelivery(t, h, row.ID)
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_ci_retry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$;
CREATE TRIGGER reject_ci_retry BEFORE INSERT ON ci_execution_check_retries FOR EACH ROW EXECUTE FUNCTION reject_ci_retry()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "atomic", CheckRetryRequest{}); err == nil {
		t.Fatal("missing audit receipt committed")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != 0 || h.count(t, "SELECT count(*) FROM ci_execution_checks WHERE status='dead' AND retry_generation=0") != 1 {
		t.Fatal("audit failure changed delivery")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER reject_ci_retry ON ci_execution_check_retries"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RequestCheckRetry(t.Context(), h.backend.Config, h.store, h.verify, h.account, row.ID, "atomic", CheckRetryRequest{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/20261016175839315_ci_check_delivery_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil {
		t.Fatal("rollback discarded audit and request identity")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil {
		t.Fatal(err)
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != 0 || h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
		t.Fatal("deletion retained retry authority")
	}
}
