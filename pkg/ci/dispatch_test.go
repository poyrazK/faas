package ci

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func dispatchDue(t *testing.T, h *jobsHarness) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET next_reconcile_at=clock_timestamp()-interval '1 second' WHERE mode='jobs'"); err != nil {
		t.Fatal(err)
	}
}

func dispatchConfig(h *jobsHarness) Config {
	cfg := h.backend.Config
	cfg.WorkerEnabled = true
	return cfg
}

func TestCIDispatchRestartBackoffAndPolicyDrain(t *testing.T) {
	h := newJobsHarness(t)
	h.store.executionClock = nil
	row := h.admit(t, "worker-restart")
	// Older blocked and qualification records must remain outside the queue.
	if _, _, err := h.store.Submit(t.Context(), h.account, "blocked", h.req.SubmitRequest); err != nil {
		t.Fatal(err)
	}
	qualification, _, err := h.store.SubmitQualification(t.Context(), h.account, "qualification", h.req.SubmitRequest)
	if err != nil {
		t.Fatal(err)
	}
	d := Dispatcher{Config: dispatchConfig(h), Store: h.store, Backend: h.backend}
	d.Config.WorkerEnabled = false
	if claimed, err := d.RunOnce(t.Context()); err != nil || claimed {
		t.Fatal("disabled worker ran")
	}
	d.Config.WorkerEnabled = true
	dispatchDue(t, h)
	if claimed, err := d.RunOnce(t.Context()); err != nil || !claimed {
		t.Fatalf("first worker tick: %t %v", claimed, err)
	}
	row, err = h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := row.Jobs[0].Attempt.ID
	h.publish(t, row, "test", StatusSucceeded, 0, []byte("durable logs\n"), true, true)
	restarted := NewPostgresStore(h.pool)
	h.backend.Store = restarted
	d.Store, d.Backend = restarted, h.backend
	dispatchDue(t, h)
	if _, err := d.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err = restarted.Execution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Jobs[0].Attempt.ID != attempt || row.Jobs[0].Status != StatusSucceeded || string(row.Jobs[0].Attempt.Logs) != "durable logs\n" {
		t.Fatal("restart lost immutable attempt/output")
	}
	// Removing admission and allowlist still drains existing work. The waiting
	// dependent never obtains a native run and completed output remains frozen.
	d.Config = Config{WorkerEnabled: true}
	dispatchDue(t, h)
	if _, err := d.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err = restarted.Execution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != StatusCancelled || row.Jobs[0].Status != StatusSucceeded || row.Jobs[1].Attempt != nil {
		t.Fatal("disabled policy did not cancel waiting work safely")
	}
	q, err := restarted.Execution(t.Context(), h.account, qualification.ID)
	if err != nil || q.Status != StatusQueued || q.Jobs[0].Attempt != nil {
		t.Fatal("qualification entered dispatcher")
	}
	if h.count(t, "SELECT count(*) FROM job_runs") != 1 {
		t.Fatal("worker duplicated native intent")
	}
}

func TestCIDispatchRetryIsDurableAndBounded(t *testing.T) {
	h := newJobsHarness(t)
	h.store.executionClock = nil
	row := h.admit(t, "worker-backoff")
	cfg := dispatchConfig(h)
	dispatchDue(t, h)
	lease, err := h.store.claimDispatch(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	attempt := lease.Execution.Jobs[0].Attempt.ID
	if _, err := h.store.finishExecutionTick(t.Context(), lease, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < api.CIDispatchMaxFailures+3; i++ {
		if err := h.store.finishDispatch(t.Context(), lease, true); err != nil {
			t.Fatal(err)
		}
	}
	var failures int
	var next, now time.Time
	if err := h.pool.QueryRow(t.Context(), "SELECT reconcile_failures,next_reconcile_at,clock_timestamp() FROM ci_executions WHERE id=$1", row.ID).Scan(&failures, &next, &now); err != nil {
		t.Fatal(err)
	}
	if failures != api.CIDispatchMaxFailures || next.Sub(now) > api.CIDispatchMaxBackoffSeconds*time.Second || next.Sub(now) < (api.CIDispatchMaxBackoffSeconds-1)*time.Second {
		t.Fatal("retry backoff escaped its durable bound")
	}
	fresh := NewPostgresStore(h.pool)
	if _, err := fresh.claimDispatch(t.Context(), cfg); !errors.Is(err, ErrNotFound) {
		t.Fatal("restart bypassed retry time")
	}
	dispatchDue(t, h)
	again, err := fresh.claimDispatch(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again.Execution.Jobs[0].Attempt.ID != attempt || again.Fence <= lease.Fence {
		t.Fatal("retry replaced attempt or failed to fence stale controller")
	}
	if _, err := fresh.finishExecutionTick(t.Context(), lease, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale controller retained authority")
	}
	// Cancellation must interrupt a persisted retry delay.
	if _, err := fresh.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	var due bool
	if err := h.pool.QueryRow(t.Context(), "SELECT next_reconcile_at<=clock_timestamp() FROM ci_executions WHERE id=$1", row.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("cancellation retained a retry delay")
	}
}

func dispatchAccount(t *testing.T, h *jobsHarness, account string) JobsSubmitRequest {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro') ON CONFLICT DO NOTHING", account, account+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	revision, err := h.store.PutRevision(t.Context(), account, "dispatch", executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := h.store.PutGitHubBinding(t.Context(), account, "dispatch", GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	req := JobsSubmitRequest{SubmitRequest: SubmitRequest{RevisionID: revision.ID, CommitSHA: h.req.CommitSHA}, BindingID: binding.ID, Templates: make(map[string]string)}
	for name, id := range h.req.Templates {
		target := uuid.NewString()
		req.Templates[name] = target
		if _, err := h.pool.Exec(t.Context(), `INSERT INTO jobs(id,account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key)
 SELECT $1,$2,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,retry_max,env_overrides,command,image_materialization_status,image_resolved_digest,image_storage_key FROM jobs WHERE id=$3`, target, account, id); err != nil {
			t.Fatal(err)
		}
	}
	return req
}

func TestCIDispatchGlobalAndAccountLeases(t *testing.T) {
	h := newJobsHarness(t)
	h.store.executionClock = nil
	cfg := dispatchConfig(h)
	accounts := []string{h.account, h.other, uuid.NewString()}
	for _, account := range accounts {
		cfg.accounts[account] = true
		req := h.req
		if account != h.account {
			req = dispatchAccount(t, h, account)
		}
		for i := range 3 {
			if _, _, err := AdmitJobs(t.Context(), cfg, h.store, func(context.Context, string, int64, int64, string) error { return nil }, account, fmt.Sprintf("lease-%d", i), req); err != nil {
				t.Fatal(err)
			}
		}
	}
	dispatchDue(t, h)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var leases []executionLease
	var failures []error
	for range api.CIDispatchWorkers * 3 {
		wg.Go(func() {
			lease, err := NewPostgresStore(h.pool).claimDispatch(t.Context(), cfg)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				leases = append(leases, lease)
			} else if !errors.Is(err, ErrNotFound) {
				failures = append(failures, err)
			}
		})
	}
	wg.Wait()
	if len(failures) > 0 || len(leases) != api.CIDispatchWorkers {
		t.Fatalf("global leases: %d errors=%v", len(leases), failures)
	}
	counts := make(map[string]int)
	for _, lease := range leases {
		counts[lease.Execution.AccountID]++
	}
	for _, count := range counts {
		if count > api.CIDispatchLeasesPerAccount {
			t.Fatal("account monopolized worker leases")
		}
	}
	// A process crash leaves the database lease in place until expiry; the next
	// owner recovers the same durable attempt after the lease expires.
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET lease_until=clock_timestamp()-interval '1 second',next_reconcile_at=clock_timestamp()-interval '1 second' WHERE lease_until IS NOT NULL"); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewPostgresStore(h.pool).claimDispatch(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range leases {
		if old.Execution.ID == recovered.Execution.ID && old.Execution.Jobs[0].Attempt.ID != recovered.Execution.Jobs[0].Attempt.ID {
			t.Fatal("crash recovery replaced attempt")
		}
	}
}

func TestCIDispatchAdmissionQuotaAndReplay(t *testing.T) {
	h := newJobsHarness(t)
	for i := range api.CIActiveExecutionsPerAccount {
		h.admit(t, fmt.Sprintf("active-%d", i))
	}
	if _, replay, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "active-0", h.req); err != nil || !replay {
		t.Fatal("full queue blocked idempotent replay")
	}
	if _, _, err := AdmitJobs(t.Context(), h.backend.Config, h.store, h.verify, h.account, "overflow", h.req); !errors.Is(err, ErrLimit) {
		t.Fatal("active account quota escaped")
	}
}

func TestCIDispatchGlobalAdmissionQuota(t *testing.T) {
	h := newJobsHarness(t)
	cfg := dispatchConfig(h)
	for i := 0; i < api.CIActiveExecutionsGlobal/api.CIActiveExecutionsPerAccount+1; i++ {
		account := uuid.NewString()
		cfg.accounts[account] = true
		req := dispatchAccount(t, h, account)
		for j := range api.CIActiveExecutionsPerAccount {
			_, _, err := AdmitJobs(t.Context(), cfg, h.store, func(context.Context, string, int64, int64, string) error { return nil }, account, fmt.Sprintf("global-%d", j), req)
			if i*api.CIActiveExecutionsPerAccount+j >= api.CIActiveExecutionsGlobal {
				var limit *LimitError
				if !errors.As(err, &limit) || limit.Resource != "global_active_executions" {
					t.Fatal("global active admission bound escaped")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCIDispatchSourceFailureRetriesSameIntent(t *testing.T) {
	h := newJobsHarness(t)
	h.store.executionClock = nil
	row := h.admit(t, "source-retry")
	h.stream.sha = "invalid-provider-sha"
	d := Dispatcher{Config: dispatchConfig(h), Store: h.store, Backend: h.backend}
	dispatchDue(t, h)
	if claimed, err := d.RunOnce(t.Context()); !claimed || err == nil {
		t.Fatal("source failure was silently accepted")
	}
	current, err := h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := current.Jobs[0].Attempt.ID
	var failures int
	if err := h.pool.QueryRow(t.Context(), "SELECT reconcile_failures FROM ci_executions WHERE id=$1", row.ID).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("failed source created native intent or lost retry state")
	}
	h.stream.sha = ""
	h.store = NewPostgresStore(h.pool)
	h.backend.Store = h.store
	d.Store, d.Backend = h.store, h.backend
	dispatchDue(t, h)
	if _, err := d.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Jobs[0].Attempt.ID != attempt || h.count(t, "SELECT count(*) FROM job_runs") != 1 {
		t.Fatal("source retry replaced native identity")
	}
}

func TestCIDispatchCancellationFencesRetrySchedule(t *testing.T) {
	h := newJobsHarness(t)
	h.store.executionClock = nil
	row := h.admit(t, "cancel-race")
	dispatchDue(t, h)
	lease, err := h.store.claimDispatch(t.Context(), dispatchConfig(h))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.finishExecutionTick(t.Context(), lease, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	// A backend failure's delayed scheduling acknowledgement cannot put a
	// newer cancellation back behind the old retry delay.
	if err := h.store.finishDispatch(t.Context(), lease, true); err != nil {
		t.Fatal(err)
	}
	var due bool
	var failures int
	if err := h.pool.QueryRow(t.Context(), "SELECT next_reconcile_at<=clock_timestamp(),reconcile_failures FROM ci_executions WHERE id=$1", row.ID).Scan(&due, &failures); err != nil {
		t.Fatal(err)
	}
	if !due || failures != 0 {
		t.Fatal("stale retry schedule postponed cancellation")
	}
}
