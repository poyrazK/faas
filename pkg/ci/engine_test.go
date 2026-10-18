package ci

import (
	"bytes"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type qualificationStore interface {
	Store
	ExecutionStore
}

type qualificationClock struct{ nanos atomic.Int64 }

func (c *qualificationClock) now() time.Time      { return time.Unix(0, c.nanos.Load()).UTC() }
func (c *qualificationClock) advance(seconds int) { c.nanos.Add(int64(seconds) * int64(time.Second)) }

type executionHarness struct {
	store          qualificationStore
	restart        func() qualificationStore
	clock          *qualificationClock
	backend        *fakeBackend
	account, other string
}

type executionFactory func(*testing.T) *executionHarness

func newHarness(account, other string) *executionHarness {
	clock := &qualificationClock{}
	clock.nanos.Store(time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC).UnixNano())
	return &executionHarness{clock: clock, backend: newFakeBackend(), account: account, other: other}
}

func TestExecutionQualificationMemory(t *testing.T) {
	exerciseExecutionEngine(t, func(_ *testing.T) *executionHarness {
		h := newHarness(uuid.NewString(), uuid.NewString())
		s := NewMemoryStore()
		s.executionClock = h.clock.now
		h.store = s
		h.restart = func() qualificationStore { return s }
		return h
	})
}

func qualificationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "CREATE TABLE accounts (id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	// ADR-1656: metadata capture now shares the automation deduplication quota.
	// This metadata-only fixture intentionally excludes the native Jobs tables.
	for _, name := range []string{"20261016175839285_internal_ci.sql", "20261016175839288_ci_execution_qualification.sql", "20261016175839290_ci_github_sources.sql", "20261016175839303_ci_execution_dispatch.sql", "20261016175839307_ci_execution_profiles.sql", "20261016175839311_ci_github_automation.sql", "20261016175839317_ci_execution_archival.sql", "20261017070545475_ci_automation_event_recovery.sql", "20261017074525544_ci_automation_branch_concurrency.sql", "20261017085604854_ci_pull_request_automation.sql", "20261017095313973_ci_execution_reruns.sql", "20261017103328494_ci_automation_path_filters.sql", "20261017111942534_ci_source_check_recovery.sql"} {
		data, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func TestExecutionQualificationPostgres(t *testing.T) {
	pool := qualificationDatabase(t)
	exerciseExecutionEngine(t, func(t *testing.T) *executionHarness {
		h := newHarness(uuid.NewString(), uuid.NewString())
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id) VALUES ($1), ($2)", h.account, h.other); err != nil {
			t.Fatal(err)
		}
		h.restart = func() qualificationStore {
			s := NewPostgresStore(pool)
			s.executionClock = h.clock.now
			return s
		}
		h.store = h.restart()
		return h
	})
	// The real adapter obtains authoritative lease time from PostgreSQL, even
	// outside a transaction; fake time is injected only in the shared scenarios.
	s := NewPostgresStore(pool)
	before := time.Now().Add(-time.Second)
	now, err := s.executionNow(t.Context(), pool)
	if err != nil || now.Before(before) || now.After(time.Now().Add(time.Second)) {
		t.Fatalf("database lease clock unavailable: %v %v", now, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE ci_executions SET status='succeeded', finished_at=now() WHERE status='queued'"); err == nil {
		t.Fatal("schema allowed a status/snapshot disagreement")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE ci_executions SET mode='compute'"); err == nil {
		t.Fatal("schema allowed production compute provenance")
	}
	var account, execution string
	if err := pool.QueryRow(t.Context(), "SELECT account_id::text,id::text FROM ci_executions LIMIT 1").Scan(&account, &execution); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execution(t.Context(), account, execution); !errors.Is(err, ErrNotFound) {
		t.Fatalf("account deletion leaked execution output: %v", err)
	}
	// ADR-1656 / ADR-1592: roll back empty recovery/source journals before execution storage.
	// The append-only rollback still preserves the original metadata receipts.
	for _, name := range []string{"20261017111942534_ci_source_check_recovery.sql", "20261017103328494_ci_automation_path_filters.sql", "20261017095313973_ci_execution_reruns.sql", "20261017085604854_ci_pull_request_automation.sql", "20261017074525544_ci_automation_branch_concurrency.sql", "20261017070545475_ci_automation_event_recovery.sql", "20261016175839317_ci_execution_archival.sql", "20261016175839311_ci_github_automation.sql", "20261016175839288_ci_execution_qualification.sql"} {
		data, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err != nil {
			t.Fatal(err)
		}
	}
	var receipts int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_runs").Scan(&receipts); err != nil {
		t.Fatalf("execution rollback damaged metadata: %v", err)
	}
}

func executionDefinition() Definition {
	return Definition{Version: 1, Repository: "gregale/ci-qualification", Jobs: map[string]Job{
		"test": {Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), Steps: []Step{
			{Name: "prepare", Run: "commands are stored, never executed by tests"}, {Name: "unit", Run: "synthetic tests"}}, Artifacts: []string{"reports/**"}},
		"build": {Image: "registry.example/ci@sha256:" + strings.Repeat("b", 64), Needs: []string{"test"}, Steps: []Step{{Name: "build", Run: "synthetic build"}}},
	}}
}

func (h *executionHarness) submit(t *testing.T, def Definition) (Execution, SubmitRequest) {
	t.Helper()
	revision, err := h.store.PutRevision(t.Context(), h.account, "checks", def)
	if err != nil {
		t.Fatal(err)
	}
	req := SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}
	row, replay, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req)
	if err != nil || replay || row.Mode != ExecutionQualification || row.Status != StatusQueued {
		t.Fatalf("qualification admission: %+v %v", row, err)
	}
	return row, req
}

func (h *executionHarness) tick(t *testing.T, id string) Execution {
	t.Helper()
	row, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (h *executionHarness) finish(t *testing.T, id string) Execution {
	t.Helper()
	for range 20 {
		row := h.tick(t, id)
		if terminal(row.Status) {
			if row.FinishedAt == nil {
				t.Fatal("terminal execution has no finish time")
			}
			return row
		}
	}
	t.Fatal("execution did not settle")
	return Execution{}
}

func exerciseExecutionEngine(t *testing.T, factory executionFactory) {
	t.Helper()
	t.Run("two-job-success-and-immutable-plan", func(t *testing.T) {
		h := factory(t)
		row, req := h.submit(t, executionDefinition())
		initial := clone(row)
		row.Plan.Jobs[0].Steps[0].Run = "reader mutation"
		row = h.finish(t, row.ID)
		if row.Status != StatusSucceeded || !reflect.DeepEqual(row.Plan, initial.Plan) || !reflect.DeepEqual(h.backend.startOrder, []string{"test", "build"}) {
			t.Fatalf("pipeline did not respect immutable dependency order: %+v", row)
		}
		for _, job := range row.Jobs {
			if job.Status != StatusSucceeded || job.Attempt.Number != 1 || job.Attempt.FinishedAt == nil || *job.Attempt.ExitCode != 0 {
				t.Fatalf("missing attempt evidence: %+v", job)
			}
		}
		h.store = h.restart()
		recovered, replay, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req)
		if err != nil || !replay || !reflect.DeepEqual(recovered, row) {
			t.Fatalf("restart or retry changed the result: %v", err)
		}
		cancelled, err := h.store.CancelExecution(t.Context(), h.account, row.ID)
		if err != nil || !reflect.DeepEqual(cancelled, row) {
			t.Fatal("cancellation rewrote a terminal result")
		}
	})
	t.Run("failure-skips-dependents-and-retains-reports", func(t *testing.T) {
		h := factory(t)
		def := executionDefinition()
		def.Jobs["independent"] = Job{Image: def.Jobs["test"].Image, Steps: []Step{{Name: "lint", Run: "synthetic lint"}}}
		h.backend.scenarios["test"] = fakeScenario{fail: true, logs: []byte("test failed\n"), artifacts: []ArtifactRef{{Path: "reports/unit/results.xml", Digest: "sha256:" + strings.Repeat("c", 64), Bytes: 123}}}
		row, _ := h.submit(t, def)
		row = h.finish(t, row.ID)
		if row.Status != StatusFailed || row.Jobs[0].Status != StatusFailed || row.Jobs[1].Status != StatusSkipped || row.Jobs[1].Attempt != nil || row.Jobs[2].Status != StatusSucceeded {
			t.Fatalf("bad dependency failure propagation: %+v", row)
		}
		if row.Jobs[0].Attempt.Steps[1].Status != StatusSkipped || len(row.Jobs[0].Attempt.Artifacts) != 1 || string(row.Jobs[0].Attempt.Logs) != "test failed\n" {
			t.Fatal("failure lost step or report evidence")
		}
	})
	t.Run("cancellation-before-start", func(t *testing.T) {
		h := factory(t)
		row, req := h.submit(t, executionDefinition())
		row, err := h.store.CancelExecution(t.Context(), h.account, row.ID)
		if err != nil || row.Status != StatusCancelled {
			t.Fatalf("queued cancellation: %+v %v", row, err)
		}
		again := h.finish(t, row.ID)
		if !reflect.DeepEqual(row, again) || len(h.backend.attempts) != 0 {
			t.Fatal("queued cancellation dispatched work or changed history")
		}
		recovered, replay, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req)
		if err != nil || !replay || recovered.Status != StatusCancelled {
			t.Fatal("submission retry resurrected cancellation")
		}
	})
	t.Run("cancellation-waits-for-confirmed-stop", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{neverFinish: true, cancelDelay: 1}
		row, _ := h.submit(t, executionDefinition())
		row = h.tick(t, row.ID)
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		row = h.tick(t, row.ID)
		if row.Status != StatusCancelling || row.FinishedAt != nil || row.Jobs[0].Attempt.FinishedAt != nil {
			t.Fatal("reported cancelled before backend stopped")
		}
		row = h.finish(t, row.ID)
		if row.Status != StatusCancelled || row.Jobs[1].Attempt != nil || len(h.backend.startOrder) != 1 {
			t.Fatal("cancelled pipeline started its dependency")
		}
	})
	t.Run("timeout-survives-restart-and-confirms-stop", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{neverFinish: true, cancelDelay: 1}
		row, _ := h.submit(t, executionDefinition())
		row = h.tick(t, row.ID)
		h.clock.advance(api.CIDefaultTimeoutSeconds + 1)
		h.store = h.restart()
		row = h.tick(t, row.ID)
		if row.Jobs[0].Status != StatusCancelling || row.FinishedAt != nil {
			t.Fatal("timeout did not request and await cancellation")
		}
		row = h.finish(t, row.ID)
		if row.Status != StatusFailed || row.Jobs[0].Reason != ReasonTimeout || row.Jobs[1].Status != StatusSkipped || len(h.backend.startOrder) != 1 {
			t.Fatalf("timeout lost its durable deadline: %+v", row)
		}
	})
	t.Run("restart-after-start-before-receipt-fences-old-worker", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{logs: []byte("durable output\n")}
		row, _ := h.submit(t, executionDefinition())
		lease, err := h.store.claimExecution(t.Context(), h.account, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		a := lease.Execution.Jobs[0].Attempt
		key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: "test", AttemptID: a.ID, Fence: lease.Fence}
		start := StartRequest{Key: key, Repository: row.Plan.Repository, CommitSHA: row.CommitSHA, Job: row.Plan.Jobs[0], Deadline: a.Deadline}
		if err := h.backend.Start(t.Context(), start); err != nil {
			t.Fatal(err)
		}
		lost, err := h.backend.Observe(t.Context(), ObserveRequest{Key: key, LogLimit: api.CILogPageMaxBytes})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.claimExecution(t.Context(), h.account, row.ID); !errors.Is(err, ErrLeaseBusy) {
			t.Fatalf("parallel controller acquired live lease: %v", err)
		}
		h.clock.advance(api.CIExecutionLeaseSeconds + 1)
		if _, err := h.store.finishExecutionTick(t.Context(), lease, nil); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("expired lease accepted a write before takeover: %v", err)
		}
		h.store = h.restart()
		row = h.tick(t, row.ID)
		if row.Jobs[0].Attempt.ID != a.ID || string(row.Jobs[0].Attempt.Logs) != "durable output\n" || len(h.backend.startOrder) != 1 {
			t.Fatal("restart duplicated user work or lost replayed logs")
		}
		if _, err := h.store.finishExecutionTick(t.Context(), lease, []attemptObservation{{ID: a.ID, Observation: lost}}); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale controller overwrote result: %v", err)
		}
		if err := h.backend.Start(t.Context(), start); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("backend accepted a fenced worker: %v", err)
		}
		if h.finish(t, row.ID).Status != StatusSucceeded || len(h.backend.startOrder) != 2 {
			t.Fatal("recovered pipeline did not complete exactly once per job")
		}
	})
	t.Run("lost-start-ack-reuses-attempt", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{loseStartAck: true}
		row, _ := h.submit(t, executionDefinition())
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID); !errors.Is(err, errFakeTransport) {
			t.Fatalf("lost ack was swallowed: %v", err)
		}
		row = h.finish(t, row.ID)
		if row.Status != StatusSucceeded || len(h.backend.startOrder) != 2 || len(h.backend.attempts) != 2 {
			t.Fatal("transport retry created an extra attempt")
		}
	})
	t.Run("cancel-during-start-invalidates-receipt", func(t *testing.T) {
		h := factory(t)
		def := executionDefinition()
		def.Jobs["independent"] = Job{Image: def.Jobs["test"].Image, Steps: []Step{{Name: "lint", Run: "synthetic lint"}}}
		row, _ := h.submit(t, def)
		h.backend.onStart = func(StartRequest) {
			if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
				t.Error(err)
			}
		}
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("cancellation did not fence in-flight receipt: %v", err)
		}
		h.backend.onStart = nil
		h.clock.advance(api.CIExecutionLeaseSeconds + 1)
		row = h.finish(t, row.ID)
		if row.Status != StatusCancelled || len(h.backend.startOrder) != 1 {
			t.Fatal("worker dispatched more work after losing cancellation race")
		}
		// The other root had a durable intent but no Start. Cancel tombstones it,
		// including against a late Start with a newer controller fence.
		for _, job := range row.Jobs {
			if job.Name == "independent" {
				a := job.Attempt
				key := AttemptKey{AccountID: h.account, ExecutionID: row.ID, JobName: job.Name, AttemptID: a.ID, Fence: 100}
				if err := h.backend.Start(t.Context(), StartRequest{Key: key}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if len(h.backend.startOrder) != 1 {
			t.Fatal("late Start resurrected a cancelled attempt")
		}
	})
	t.Run("bounded-resumable-binary-logs", func(t *testing.T) {
		h := factory(t)
		content := bytes.Repeat([]byte{0xff, 0, 'x', '\n'}, api.CILogMaxBytesPerAttempt/4+7)
		h.backend.scenarios["test"] = fakeScenario{logs: content}
		row, _ := h.submit(t, executionDefinition())
		row = h.finish(t, row.ID)
		a := row.Jobs[0].Attempt
		if !a.LogTruncated || len(a.Logs) != api.CILogMaxBytesPerAttempt || !bytes.Equal(a.Logs, content[:api.CILogMaxBytesPerAttempt]) {
			t.Fatal("log retention did not preserve the bounded prefix")
		}
		h.store = h.restart()
		var recovered []byte
		for offset := 0; ; {
			page, err := ExecutionLogs(t.Context(), h.store, h.account, row.ID, a.ID, offset, 97)
			if err != nil || page.Offset != offset || !page.Truncated || page.NextOffset < offset {
				t.Fatalf("invalid resumable page: %+v %v", page, err)
			}
			recovered = append(recovered, page.Data...)
			offset = page.NextOffset
			if page.Complete {
				break
			}
		}
		if !bytes.Equal(recovered, a.Logs) {
			t.Fatal("resuming logs duplicated or omitted bytes")
		}
		if _, err := ExecutionLogs(t.Context(), h.store, h.other, row.ID, a.ID, 0, 1); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account logs leaked")
		}
		if _, err := ExecutionLogs(t.Context(), h.store, h.account, row.ID, a.ID, len(a.Logs)+1, 1); !errors.Is(err, ErrLogCursor) {
			t.Fatal("invalid cursor accepted")
		}
	})
	t.Run("account-isolation-and-metadata-remains-inert", func(t *testing.T) {
		h := factory(t)
		row, req := h.submit(t, executionDefinition())
		if _, err := h.store.Execution(t.Context(), h.other, row.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account execution leaked")
		}
		if _, err := h.store.CancelExecution(t.Context(), h.other, row.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account cancellation accepted")
		}
		if _, err := h.store.claimExecution(t.Context(), h.other, row.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account lease accepted")
		}
		if _, _, err := h.store.SubmitQualification(t.Context(), h.other, "foreign-revision", req); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign revision executed")
		}
		receipt, _, err := h.store.Submit(t.Context(), h.account, "qualification-1", req)
		if err != nil || receipt.Status != StatusBlocked || receipt.ID == row.ID {
			t.Fatal("metadata submission unexpectedly became executable")
		}
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, receipt.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("engine accepted a blocked receipt")
		}
		h.finish(t, row.ID)
		receipt, err = h.store.Run(t.Context(), h.account, receipt.ID)
		if err != nil || receipt.Status != StatusBlocked || receipt.BlockedReason != ComputeUnavailable {
			t.Fatal("qualification engine changed retained metadata")
		}
	})
	t.Run("terminal-output-drain-does-not-restart-or-timeout-task", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{logs: bytes.Repeat([]byte("x"), api.CILogPageMaxBytes*3)}
		row, _ := h.submit(t, executionDefinition())
		row = h.tick(t, row.ID)
		row = h.tick(t, row.ID)
		if row.Jobs[0].Attempt.BackendStatus != StatusSucceeded || terminal(row.Jobs[0].Status) {
			t.Fatal("did not preserve terminal backend evidence during output drain")
		}
		h.clock.advance(api.CIDefaultTimeoutSeconds + 1)
		row = h.finish(t, row.ID)
		if row.Status != StatusSucceeded || row.Jobs[0].Reason != "" || len(row.Jobs[0].Attempt.Logs) != api.CILogPageMaxBytes*3 {
			t.Fatal("log collection timed out a task already confirmed stopped")
		}
	})
	t.Run("invalid-observation-does-not-corrupt-durable-output", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{logs: []byte("valid prefix"), modify: func(report *Observation) { report.LogOffset++ }}
		row, _ := h.submit(t, executionDefinition())
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID); !errors.Is(err, ErrBackendProtocol) {
			t.Fatalf("invalid offset accepted: %v", err)
		}
		again, err := h.store.Execution(t.Context(), h.account, row.ID)
		if err != nil || len(again.Jobs[0].Attempt.Logs) != 0 || again.Jobs[0].Attempt.Steps[0].Status != StatusQueued {
			t.Fatal("invalid backend data committed to the ledger")
		}
		h.backend.scenarios["test"] = fakeScenario{logs: []byte("valid prefix")}
		row = h.finish(t, row.ID)
		if row.Status != StatusSucceeded || string(row.Jobs[0].Attempt.Logs) != "valid prefix" {
			t.Fatal("invalid report prevented idempotent recovery")
		}
	})
	t.Run("cancellation-retains-terminal-artifact-manifest", func(t *testing.T) {
		h := factory(t)
		h.backend.scenarios["test"] = fakeScenario{neverFinish: true, artifacts: []ArtifactRef{{Path: "reports/cancelled.txt", Digest: "sha256:" + strings.Repeat("d", 64), Bytes: 7}}}
		row, _ := h.submit(t, executionDefinition())
		h.tick(t, row.ID)
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		row = h.finish(t, row.ID)
		if row.Status != StatusCancelled || len(row.Jobs[0].Attempt.Artifacts) != 1 {
			t.Fatal("cancellation discarded collected artifacts")
		}
	})
	t.Run("maximum-job-and-log-shape-fits-durable-snapshot", func(t *testing.T) {
		h := factory(t)
		def := executionDefinition()
		def.Jobs = make(map[string]Job)
		for i := range api.CIMaxJobs {
			name := fmt.Sprintf("job-%d", i)
			steps := make([]Step, api.CIMaxStepsPerJob)
			for j := range steps {
				steps[j] = Step{Name: fmt.Sprintf("step-%d", j), Run: "synthetic"}
			}
			def.Jobs[name] = Job{Image: "registry.example/ci@sha256:" + strings.Repeat("e", 64), Steps: steps}
			h.backend.scenarios[name] = fakeScenario{logs: bytes.Repeat([]byte("l"), api.CILogMaxBytesPerAttempt+1)}
		}
		row, _ := h.submit(t, def)
		row = h.finish(t, row.ID)
		if row.Status != StatusSucceeded || len(h.backend.startOrder) != api.CIMaxJobs {
			t.Fatal("maximum admitted graph could not complete")
		}
		for _, job := range row.Jobs {
			if !job.Attempt.LogTruncated || len(job.Attempt.Logs) != api.CILogMaxBytesPerAttempt {
				t.Fatal("maximum graph exceeded a log allowance")
			}
		}
	})
	t.Run("concurrent-idempotency-leases-and-bounded-admission", func(t *testing.T) {
		h := factory(t)
		row, req := h.submit(t, executionDefinition())
		var wg sync.WaitGroup
		var claimed atomic.Int64
		for range 16 {
			wg.Go(func() {
				again, replay, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req)
				if err != nil || !replay || again.ID != row.ID {
					t.Errorf("concurrent retry: %+v %v", again, err)
				}
				if _, err := h.store.claimExecution(t.Context(), h.account, row.ID); err == nil {
					claimed.Add(1)
				} else if !errors.Is(err, ErrLeaseBusy) {
					t.Errorf("claim: %v", err)
				}
			})
		}
		wg.Wait()
		if claimed.Load() != 1 {
			t.Fatal("multiple controllers acquired one live lease")
		}
		req.CommitSHA = strings.Repeat("b", 40)
		if _, _, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req); !errors.Is(err, ErrConflict) {
			t.Fatal("changed commit reused execution identity")
		}
		req.CommitSHA = row.CommitSHA
		for i := 1; i < api.CIMaxQualificationExecutionsPerAccount; i++ {
			if _, _, err := h.store.SubmitQualification(t.Context(), h.account, fmt.Sprintf("run-%d", i), req); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := h.store.SubmitQualification(t.Context(), h.account, "overflow", req); !errors.Is(err, ErrLimit) {
			t.Fatal("execution admission is unbounded")
		}
		if _, replay, err := h.store.SubmitQualification(t.Context(), h.account, "qualification-1", req); err != nil || !replay {
			t.Fatal("quota prevented idempotent recovery")
		}
		if memory, ok := h.store.(*MemoryStore); ok {
			memory.RemoveAccount(h.account)
			if _, err := memory.Execution(t.Context(), h.account, row.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("memory account deletion leaked execution metadata")
			}
		}
	})
}
