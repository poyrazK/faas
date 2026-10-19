package ci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func archiveCancelled(t *testing.T, h *jobsHarness, key string) ExecutionArchive {
	t.Helper()
	row := h.admit(t, key)
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	receipt, replay, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
	if err != nil || replay {
		t.Fatalf("archive: replay=%v err=%v", replay, err)
	}
	return receipt
}

func TestCIArchiveReclaimsHistoryWithoutReplayingIdentity(t *testing.T) {
	h := newJobsHarness(t)
	var first Execution
	for i := 0; i < api.CIMaxJobsExecutionsPerAccount; i++ {
		key := uuid.NewString()
		if i == 0 {
			key = "reserved"
		}
		row := h.admit(t, key)
		if i == 0 {
			first = row
		}
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "full", h.req)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "jobs_executions" {
		t.Fatal("history bound not enforced")
	}
	receipt, replay, err := h.store.ArchiveExecution(t.Context(), h.account, first.ID)
	if err != nil || replay || receipt.ExecutionID != first.ID || receipt.Status != StatusCancelled {
		t.Fatalf("archive: %+v %v", receipt, err)
	}
	h.restart()
	again, replay, err := h.store.ArchiveExecution(t.Context(), h.account, first.ID)
	firstJSON, _ := json.Marshal(receipt)
	againJSON, _ := json.Marshal(again)
	if err != nil || !replay || string(firstJSON) != string(againJSON) {
		t.Fatal("lost archive response did not recover same receipt")
	}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "reserved", h.req); !errors.Is(err, ErrExecutionArchived) {
		t.Fatal("archived key created another execution")
	}
	h.admit(t, "reused-slot")
	rows, err := h.store.ListJobsExecutions(t.Context(), h.account)
	if err != nil || len(rows) != api.CIMaxJobsExecutionsPerAccount {
		t.Fatal("archival did not release exactly one detail slot")
	}
	for _, row := range rows {
		if row.ID == first.ID {
			t.Fatal("archive remained in detailed history")
		}
	}
	if _, err := h.store.Execution(t.Context(), h.account, first.ID); !errors.Is(err, ErrExecutionArchived) {
		t.Fatal("archived detail still readable")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, first.ID); !errors.Is(err, ErrExecutionArchived) {
		t.Fatal("archived detail mutated")
	}
	if _, err := h.store.ExecutionArchive(t.Context(), h.other, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("archive crossed account boundary")
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.other, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign account archived execution")
	}
	if rows, err := h.store.ExecutionArchives(t.Context(), h.other); err != nil || len(rows) != 0 {
		t.Fatal("foreign archive list leaked")
	}
}

func TestCIArchivePreservesAutomationChecksAndRetryAudit(t *testing.T) {
	h, _, cfg := automationFixture(t)
	cfg.ExecutionChecksEnabled, cfg.GitHubChecksEnabled = true, true
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	if _, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil {
		t.Fatal(err)
	}
	events, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil || len(events) != 1 {
		t.Fatal("missing event")
	}
	id := events[0].ExecutionID
	if _, err := h.store.CancelExecution(t.Context(), h.account, id); err != nil {
		t.Fatal(err)
	}
	exhaustCheckDelivery(t, h, id)
	retry, _, err := RequestCheckRetry(t.Context(), cfg, h.store, h.verify, h.account, id, "retry-audit", CheckRetryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, id); !errors.Is(err, ErrArchiveUnsafe) {
		t.Fatal("pending Check retry was archived")
	}
	h.backend.Config = cfg
	publishExecutionCheckTest(t, h, "completed", "cancelled")
	receipt, _, err := h.store.ArchiveExecution(t.Context(), h.account, id)
	if err != nil || receipt.Automation == nil || receipt.Automation.EventID != events[0].ID || receipt.Check == nil || receipt.Check.CheckID != 77 || len(receipt.Check.Retries) != 1 || receipt.Check.Retries[0].ID != retry.ID {
		t.Fatalf("archive lost audit identity: %+v %v", receipt, err)
	}
	h.restart()
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || worked {
		t.Fatal("archived push replayed after redelivery")
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 1 || h.count(t, "SELECT count(*) FROM ci_execution_check_retries") != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 1 {
		t.Fatal("archival erased deduplication or audit authority")
	}
	if worked, err := PublishExecutionCheckOnce(t.Context(), cfg, h.store, h.verify, func(context.Context, ExecutionCheck) (int64, error) {
		t.Error("archived Check republished")
		return 77, nil
	}); err != nil || worked {
		t.Fatal("archive remained in publisher queue")
	}
}

func TestCIArchiveRejectsUnfinishedOrUnsettledWork(t *testing.T) {
	for _, name := range []string{"queued", "qualification", "pending", "sending", "dead", "stale_sent", "ignored"} {
		t.Run(name, func(t *testing.T) {
			h := executionCheckFixture(t)
			row := h.admit(t, "safety")
			if name == "qualification" {
				var err error
				row, _, err = h.store.SubmitQualification(t.Context(), h.account, "qualification", h.req.SubmitRequest)
				if err != nil {
					t.Fatal(err)
				}
			}
			if name != "queued" {
				if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "sending":
				if _, err := h.store.claimExecutionCheck(t.Context(), h.account); err != nil {
					t.Fatal(err)
				}
			case "dead":
				exhaustCheckDelivery(t, h, row.ID)
			case "stale_sent":
				if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='sent' WHERE execution_id=$1", row.ID); err != nil {
					t.Fatal(err)
				}
			case "ignored":
				if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='ignored',last_error='access_denied' WHERE execution_id=$1", row.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
			if name == "ignored" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			want := ErrArchiveUnsafe
			if name == "qualification" {
				want = ErrNotFound
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s archive: %v", name, err)
			}
		})
	}
}

func TestCIArchiveRequiresConfirmedPayloadRetirement(t *testing.T) {
	h, row, proof, view := runtimeFixture(t)
	physical := newStoragePhysicalFake()
	blobs := &ManagedBlobs{Store: h.store, Physical: physical}
	managedRuntimeResult(t, h, blobs, proof, view)
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: blobs}
	row = stopManagedRuntime(t, h, row)
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); !errors.Is(err, ErrArchiveUnsafe) {
		t.Fatal("ready payloads were archived")
	}
	h.clock.advance(api.CIStorageRetentionDays*24*3600 + 1)
	physical.lostRetireAck = true
	if _, err := blobs.Sweep(t.Context()); err == nil {
		t.Fatal("retirement response loss not simulated")
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); !errors.Is(err, ErrArchiveUnsafe) {
		t.Fatal("unconfirmed retirement allowed archive")
	}
	if _, err := blobs.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipt, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(receipt)
	for _, private := range []string{"managed logs", "managed report", "private/", "reports/", "commands are stored", "key_digest", "capability_hash"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("archive retained private detail: %s", private)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_job_attempts WHERE output IS NOT NULL") != 0 || h.count(t, "SELECT count(*) FROM ci_job_runtime") != 0 {
		t.Fatal("runtime or CI output detail survived compaction")
	}
	owned, err := h.store.JobsRunOwned(t.Context(), h.account, proof.RunID)
	if err != nil || !owned {
		t.Fatal("archive removed Jobs replay fence")
	}
	blocked, err := h.store.JobsRetryBlocked(t.Context(), proof.RunID)
	if err != nil || !blocked {
		t.Fatal("archive removed task retry fence")
	}
	if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl); err == nil {
		t.Fatal("old runtime capability revived after archive")
	}
}

func TestCIArchiveRefusesLiveNativeReceiptAndTransfer(t *testing.T) {
	for _, name := range []string{"instance", "task", "transfer"} {
		t.Run(name, func(t *testing.T) {
			h, row, proof, view := runtimeFixture(t)
			result := runtimeResult(t, h, proof, view)
			raw, _ := json.Marshal(result)
			if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
				t.Fatal(err)
			}
			h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
			row = stopManagedRuntime(t, h, row)
			var err error
			switch name {
			case "instance":
				_, err = h.pool.Exec(t.Context(), "UPDATE instances SET state='running' WHERE id=$1", proof.InstanceID)
			case "task":
				_, err = h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='claimed',finished_at=NULL,exit_code=NULL,lease_token=$2,lease_expires_at=clock_timestamp()+interval '1 minute',last_lease_node=$3 WHERE run_id=$1", proof.RunID, uuid.NewString(), h.node)
			case "transfer":
				_, err = h.pool.Exec(t.Context(), "UPDATE ci_job_runtime SET transfer_token=$2,transfer_until=clock_timestamp()+interval '1 minute' WHERE attempt_id=$1", proof.RunID, uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); !errors.Is(err, ErrArchiveUnsafe) {
				t.Fatalf("%s archive: %v", name, err)
			}
		})
	}
}

func TestCIArchiveConcurrentRequestsAndRollback(t *testing.T) {
	h := newJobsHarness(t)
	row := h.admit(t, "concurrent")
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			_, replay, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
			if err != nil {
				t.Error(err)
			}
			if !replay {
				created.Add(1)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 || h.count(t, "SELECT count(*) FROM ci_execution_archives") != 1 {
		t.Fatal("concurrent archive duplicated receipt")
	}
	data, err := os.ReadFile("../../migrations/20261016175839317_ci_execution_archival.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), strings.Split(string(data), "-- +goose Down")[1]); err == nil {
		t.Fatal("rollback lost compacted execution identity")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil {
		t.Fatal(err)
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_archives") != 0 {
		t.Fatal("account deletion retained archive")
	}
}

func TestCIArchiveLifetimeBoundAndFailedReceipt(t *testing.T) {
	h := newJobsHarness(t)
	for range api.CIMaxExecutionArchivesPerAccount {
		archiveCancelled(t, h, uuid.NewString())
	}
	row := h.admit(t, "archive-full")
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "execution_archives" {
		t.Fatal("archive receipt limit not enforced")
	}
	if _, err := h.store.Execution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal("failed archive lost detailed history")
	}
	rows, err := h.store.ExecutionArchives(t.Context(), h.account)
	if err != nil || len(rows) != api.CIMaxExecutionArchivesPerAccount {
		t.Fatal("archive list not bounded")
	}
	h = newJobsHarness(t)
	row = h.admit(t, "atomic")
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_ci_archive() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic archive failure'; END $$;
CREATE TRIGGER reject_ci_archive BEFORE INSERT ON ci_execution_archives FOR EACH ROW EXECUTE FUNCTION reject_ci_archive()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); err == nil {
		t.Fatal("archive without durable receipt succeeded")
	}
	if _, err := h.store.Execution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal("failed receipt compacted execution")
	}
}

func TestCIArchivePublisherLockIsNotWaitedOn(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "publisher")
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
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
	if _, _, err := h.store.ArchiveExecution(ctx, h.account, row.ID); !errors.Is(err, ErrArchiveUnsafe) {
		t.Fatalf("archive waited for provider or bypassed publisher: %v", err)
	}
}

func TestCIArchiveLateFailureRetainsRuntimeAndOutput(t *testing.T) {
	h, row, proof, view := runtimeFixture(t)
	result := runtimeResult(t, h, proof, view)
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
	row = stopManagedRuntime(t, h, row)
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION reject_ci_compaction() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic compaction failure'; END $$;
CREATE TRIGGER reject_ci_compaction BEFORE UPDATE ON ci_executions FOR EACH ROW WHEN (NEW.archived_at IS NOT NULL) EXECUTE FUNCTION reject_ci_compaction()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); err == nil {
		t.Fatal("late archive failure committed")
	}
	if h.count(t, "SELECT count(*) FROM ci_execution_archives") != 0 || h.count(t, "SELECT count(*) FROM ci_job_runtime") != 1 || h.count(t, "SELECT count(*) FROM ci_job_attempts WHERE output IS NOT NULL") != 1 {
		t.Fatal("late failure lost receipt, runtime or output authority")
	}
	if _, err := h.store.Execution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal("late failure removed execution detail")
	}
}
