package ci

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func executionCheckFixture(t *testing.T) *jobsHarness {
	t.Helper()
	h := newJobsHarness(t)
	h.backend.Config.ExecutionChecksEnabled, h.backend.Config.GitHubChecksEnabled = true, true
	return h
}

func publishExecutionCheckTest(t *testing.T, h *jobsHarness, wantStatus, wantConclusion string) ExecutionCheck {
	t.Helper()
	var got ExecutionCheck
	worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) {
		got = c
		return 77, nil
	})
	if err != nil || !worked || got.Status != wantStatus || got.Conclusion != wantConclusion {
		t.Fatalf("publish: worked=%v err=%v check=%+v", worked, err, got)
	}
	return got
}

func TestCIExecutionCheckLifecycleAndPrivateSummaries(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "checks")
	queued := publishExecutionCheckTest(t, h, "queued", "")
	row = h.tick(t, row)
	running := publishExecutionCheckTest(t, h, "in_progress", "")
	if running.Version <= queued.Version || running.ExternalID() != queued.ExternalID() || running.CheckID != 77 {
		t.Fatal("execution identity or version changed incorrectly")
	}
	// Lease-only versions coalesce without another provider call.
	row = h.tick(t, row)
	if worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Error("unchanged projection called GitHub")
		return 77, nil
	}); err != nil || !worked {
		t.Fatalf("coalescing: %v %v", worked, err)
	}
	secret := "synthetic-secret-value-never-publish"
	h.publish(t, row, "test", StatusSucceeded, 0, []byte(secret), true, true)
	row = h.tick(t, row)
	row = h.tick(t, row)
	h.publish(t, row, "build", StatusSucceeded, 0, []byte(secret), false, true)
	row = h.tick(t, row)
	if row.Status != StatusSucceeded {
		t.Fatal("execution did not complete")
	}
	h.restart()
	completed := publishExecutionCheckTest(t, h, "completed", "success")
	if strings.Contains(completed.Summary, secret) || strings.Contains(completed.Summary, "reports/") || strings.Contains(completed.Summary, "synthetic build") || len(completed.Summary) > api.CIExecutionCheckSummaryMaxBytes {
		t.Fatal("Check exposed private execution data")
	}
	if !strings.Contains(completed.Summary, "test: succeeded; steps completed: 2/2") {
		t.Fatal("job progress missing")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 1 || h.count(t, "SELECT count(*) FROM ci_github_checks") != 0 {
		t.Fatal("execution publication used metadata identity")
	}
	if worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Error("terminal Check republished")
		return 77, nil
	}); err != nil || worked {
		t.Fatal("settled Check remained pending")
	}
}

func TestCIExecutionCheckTerminalConclusions(t *testing.T) {
	for _, name := range []string{"failure", "cancelled", "timed_out"} {
		t.Run(name, func(t *testing.T) {
			h := executionCheckFixture(t)
			row := h.tick(t, h.admit(t, name))
			switch name {
			case "failure":
				h.publish(t, row, "test", StatusFailed, 2, nil, false, true)
				row = h.tick(t, row)
				row = h.tick(t, row)
			case "cancelled":
				if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
					t.Fatal(err)
				}
				row = h.tick(t, row)
			case "timed_out":
				h.fakeInstance(t, row, "test")
				h.clock.advance(row.Plan.Jobs[0].TimeoutSeconds + 1)
				row = h.tick(t, row)
				h.publish(t, row, "test", StatusCancelled, 143, nil, false, true)
				row = h.tick(t, row)
				row = h.tick(t, row)
			}
			if !terminal(row.Status) {
				t.Fatalf("not terminal: %s", row.Status)
			}
			publishExecutionCheckTest(t, h, "completed", name)
		})
	}
}

func TestCIExecutionCheckGatesNeverBackfill(t *testing.T) {
	h := newJobsHarness(t)
	old := h.admit(t, "off")
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
		t.Fatal("flag-off execution captured publication")
	}
	for _, flags := range [][2]bool{{true, false}, {false, true}} {
		h.backend.Config.ExecutionChecksEnabled, h.backend.Config.GitHubChecksEnabled = flags[0], flags[1]
		h.admit(t, uuid.NewString())
		if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
			t.Fatal("one publication gate captured Check intent")
		}
	}
	h.backend.Config.ExecutionChecksEnabled, h.backend.Config.GitHubChecksEnabled = true, true
	if _, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "off", h.req); err != nil || !replay {
		t.Fatal("idempotent admission failed")
	}
	h.tick(t, old)
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
		t.Fatal("old execution backfilled after opt-in")
	}
	h.admit(t, "on")
	for _, gate := range []string{"checks", "execution_checks", "internal", "github", "accounts"} {
		cfg := h.backend.Config
		switch gate {
		case "checks":
			cfg.GitHubChecksEnabled = false
		case "execution_checks":
			cfg.ExecutionChecksEnabled = false
		case "internal":
			cfg.Enabled = false
		case "github":
			cfg.GitHubEnabled = false
		case "accounts":
			cfg.accounts = map[string]bool{}
		}
		worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
			t.Error("disabled publication called GitHub")
			return 77, nil
		})
		if err != nil || worked {
			t.Fatalf("gate %s failed: %v %v", gate, worked, err)
		}
	}
	if _, err := h.store.claimExecutionCheck(t.Context(), h.other); !errors.Is(err, ErrNoGitHubWork) {
		t.Fatal("foreign account claimed Check")
	}
	publishExecutionCheckTest(t, h, "queued", "")
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil {
		t.Fatal(err)
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 0 {
		t.Fatal("account deletion leaked Check delivery authority")
	}
	for _, value := range []string{"", "true", "01", "1 "} {
		cfg := ConfigFromEnv(func(key string) string {
			if key == ExecutionChecksEnv {
				return value
			}
			return ""
		})
		if cfg.ExecutionChecksEnabled {
			t.Fatal("non-exact flag enabled publication")
		}
	}
}

func TestCIExecutionCheckLeaseRecoveryAndLatestState(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "lease")
	stale, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	h.restart()
	current, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil || current.token == stale.token {
		t.Fatal("claim did not recover")
	}
	pub := func(context.Context, ExecutionCheck) (int64, error) { t.Error("stale claim published"); return 77, nil }
	if err := h.store.deliverExecutionCheck(t.Context(), stale, h.verify, pub); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale claim: %v", err)
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.store.deliverExecutionCheck(t.Context(), current, h.verify, func(_ context.Context, c ExecutionCheck) (int64, error) {
		if c.Status != "completed" || c.Conclusion != "cancelled" {
			t.Error("claim used old queued state")
		}
		return 77, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCIExecutionCheckSerialDeliveryDoesNotBlockCancellation(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "serial")
	claim, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.store.deliverExecutionCheck(t.Context(), claim, h.verify, func(context.Context, ExecutionCheck) (int64, error) { close(started); <-release; return 77, nil })
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not start")
	}
	finished := false
	defer func() {
		if !finished {
			close(release)
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	// Another claim skips the sending row; cancellation needs no delivery lock.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := h.store.CancelExecution(ctx, h.account, row.ID); err != nil {
		t.Fatalf("Check delivery blocked cancellation: %v", err)
	}
	if _, err := h.store.claimExecutionCheck(ctx, h.account); !errors.Is(err, ErrNoGitHubWork) {
		t.Fatalf("concurrent publisher claimed sending row: %v", err)
	}
	close(release)
	err = <-done
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	// Acknowledging the earlier queued version must leave cancellation dirty.
	publishExecutionCheckTest(t, h, "completed", "cancelled")
}

func TestCIExecutionCheckBoundedRetriesAndAccessRevocation(t *testing.T) {
	for _, reason := range []string{"unavailable", "access_denied", "crash"} {
		t.Run(reason, func(t *testing.T) {
			h := executionCheckFixture(t)
			h.admit(t, "retry")
			verify := h.verify
			if reason == "access_denied" {
				verify = func(context.Context, string, int64, int64, string) error { return ErrGitHubAccess }
			}
			for i := 0; i < api.CIGitHubMaxAttempts; i++ {
				if reason == "crash" {
					if _, err := h.store.claimExecutionCheck(t.Context(), h.account); err != nil {
						t.Fatal(err)
					}
					if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
						t.Fatal(err)
					}
				} else {
					worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, verify, func(context.Context, ExecutionCheck) (int64, error) {
						if reason == "access_denied" {
							t.Error("revoked ownership published")
						}
						return 0, errors.New("provider URL with synthetic credential")
					})
					if !worked || (reason == "access_denied") != (err == nil) {
						t.Fatalf("retry: %v %v", worked, err)
					}
					if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET next_attempt_at=clock_timestamp()"); err != nil {
						t.Fatal(err)
					}
				}
				if reason == "access_denied" {
					break
				}
			}
			if worked, err := PublishExecutionCheckOnce(t.Context(), h.backend.Config, h.store, verify, func(context.Context, ExecutionCheck) (int64, error) {
				t.Error("exhausted Check published")
				return 77, nil
			}); err != nil || worked {
				t.Fatal("retry exhaustion failed")
			}
			want := "dead"
			if reason == "access_denied" {
				want = "ignored"
			}
			if h.count(t, "SELECT count(*) FROM ci_execution_checks WHERE status=$1 AND last_error NOT LIKE '%credential%'", want) != 1 {
				t.Fatal("retry verdict or safe reason missing")
			}
		})
	}
}

func TestCIExecutionCheckAutomationAndAdmissionAtomicity(t *testing.T) {
	h, _, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || !worked {
		t.Fatal("automated admission failed")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 1 {
		t.Fatal("automated execution lost Check intent")
	}
	// A missing outbox acknowledgement must roll back execution admission.
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_ci_check() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic outbox failure'; END $$;
CREATE TRIGGER reject_ci_check BEFORE INSERT ON ci_execution_checks FOR EACH ROW EXECUTE FUNCTION reject_ci_check()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, uuid.NewString(), h.req); err == nil {
		t.Fatal("outbox failure committed execution")
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("execution admission was not atomic with outbox")
	}
	data, err := os.ReadFile("../../migrations/20261016175839313_ci_execution_checks.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil {
		t.Fatal("rollback discarded delivery identity")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks") != 1 {
		t.Fatal("rollback damaged delivery journal")
	}
}

func TestCIExecutionCheckExpiredPublisherCannotAcknowledge(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "expired")
	claim, err := h.store.claimExecutionCheck(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET lease_until=clock_timestamp()+interval '300 milliseconds'"); err != nil {
		t.Fatal(err)
	}
	err = h.store.deliverExecutionCheck(t.Context(), claim, h.verify, func(ctx context.Context, _ ExecutionCheck) (int64, error) {
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		// Expired publishers are still serialized until their HTTP call returns.
		if _, err := h.store.claimExecutionCheck(ctx, h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Errorf("expired in-flight publisher was superseded: %v", err)
		}
		return 77, nil
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired publisher acknowledged: %v", err)
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_checks WHERE delivered_version=0") != 1 {
		t.Fatal("expired receipt committed")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	publishExecutionCheckTest(t, h, "completed", "cancelled")
}

func TestCIExecutionCheckFinalResultSurvivesPolicyAndProfileDisable(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	if _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil {
		t.Fatal(err)
	}
	events, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil || len(events) != 1 {
		t.Fatal("automation receipt missing")
	}
	if _, err := h.store.DisableAutomationPolicy(t.Context(), h.account, h.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DisableExecutionProfile(t.Context(), h.account, profile.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DisableGitHubBinding(t.Context(), h.account, h.binding.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, events[0].ExecutionID); err != nil {
		t.Fatal(err)
	}
	h.backend.Config = cfg
	publishExecutionCheckTest(t, h, "completed", "cancelled")
}

func TestCIExecutionCheckWorkerRedactsProviderErrorsAndStops(t *testing.T) {
	h := executionCheckFixture(t)
	h.admit(t, "worker")
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, nil))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		RunExecutionCheckWorker(ctx, h.backend.Config, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
			close(called)
			return 0, errors.New("provider URL with synthetic-credential-123")
		}, log)
	}()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not publish")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	if !strings.Contains(output.String(), "CI execution Check delivery unavailable") || strings.Contains(output.String(), "synthetic-credential") || strings.Contains(output.String(), "provider URL") {
		t.Fatal("worker exposed provider error")
	}
}
