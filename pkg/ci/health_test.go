package ci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func healthQueue(t *testing.T, h Health, name string) QueueHealth {
	t.Helper()
	for _, q := range h.Queues {
		if q.Name == name {
			return q
		}
	}
	t.Fatalf("missing queue %s", name)
	return QueueHealth{}
}
func healthQuota(t *testing.T, h Health, name string) QuotaHealth {
	t.Helper()
	for _, q := range h.Quotas {
		if q.Resource == name {
			return q
		}
	}
	t.Fatalf("missing quota %s", name)
	return QuotaHealth{}
}
func healthWorker(t *testing.T, h Health, kind WorkerKind) WorkerHealth {
	t.Helper()
	for _, w := range h.Workers {
		if w.Name == kind {
			return w
		}
	}
	t.Fatalf("missing worker %s", kind)
	return WorkerHealth{}
}
func readHealth(t *testing.T, h *jobsHarness, account string) Health {
	t.Helper()
	out, err := h.store.CIHealth(t.Context(), account)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCIHealthAccountIsolationBacklogAndArchival(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "health-private-identity")
	if _, _, err := h.store.SubmitQualification(t.Context(), h.account, "health-qualification", h.req.SubmitRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_executions SET created_at=now()-interval '1 hour',lease_until=now()-interval '1 minute',next_reconcile_at=now()-interval '2 minutes' WHERE id=$1", row.ID); err != nil {
		t.Fatal(err)
	}
	health := readHealth(t, h, h.account)
	q := healthQueue(t, health, "executions")
	if q.Pending != 1 || q.InFlight != 0 || q.Due != 1 || q.StaleLeases != 1 || q.OldestItemAgeSeconds < 3599 {
		t.Fatalf("execution backlog: %+v", q)
	}
	if q := healthQueue(t, health, "execution_checks"); q.Pending != 1 || q.Due != 1 {
		t.Fatalf("check backlog: %+v", q)
	}
	quota := healthQuota(t, health, "active_executions")
	if quota.Used != 1 || quota.Limit == nil || *quota.Limit != api.CIActiveExecutionsPerAccount || *quota.Headroom != api.CIActiveExecutionsPerAccount-1 {
		t.Fatalf("account capacity: %+v", quota)
	}
	if healthQuota(t, health, "execution_details").Used != 1 {
		t.Fatal("qualification affected Jobs quota")
	}
	other := readHealth(t, h, h.other)
	for _, q := range other.Queues {
		if q.Pending+q.InFlight+q.Dead+q.Due+q.StaleLeases != 0 {
			t.Fatal("foreign work leaked")
		}
	}
	for _, q := range other.Quotas {
		if q.Used != 0 {
			t.Fatal("foreign usage leaked")
		}
	}
	for _, invalid := range []string{"", "all", strings.ToUpper(h.account)} {
		if _, err := h.store.CIHealth(t.Context(), invalid); !errors.Is(err, ErrNotFound) {
			t.Fatal("noncanonical account became fleet selector")
		}
	}
	raw, _ := json.Marshal(health)
	for _, private := range []string{h.account, row.ID, row.CommitSHA, h.binding.ID, "health-private-identity", "registry.example", "stored, never executed", "lease_token", "token"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("health leaked %q", private)
		}
	}
	// Reading diagnostics neither claims work nor advances execution fences.
	current, err := h.store.Execution(t.Context(), h.account, row.ID)
	if err != nil || current.ID != row.ID || h.count(t, "SELECT max(version)::integer FROM ci_executions") != 1 || h.count(t, "SELECT max(fence)::integer FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM job_runs") != 0 || h.count(t, "SELECT count(*) FROM instances") != 0 {
		t.Fatal("health changed execution or allocated compute")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	publishExecutionCheckTest(t, h, "completed", "cancelled")
	if _, _, err := h.store.ArchiveExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	h.restart()
	health = readHealth(t, h, h.account)
	if healthQuota(t, health, "execution_details").Used != 0 || healthQuota(t, health, "archives").Used != 1 || healthQuota(t, health, "active_executions").Used != 0 || healthQueue(t, health, "execution_checks").Pending != 0 {
		t.Fatal("archive did not release health headroom")
	}
	fleet, err := h.store.CIFleetHealth(t.Context())
	if err != nil || healthQuota(t, fleet, "archives").Limit != nil || *healthQuota(t, fleet, "active_executions").Limit != api.CIActiveExecutionsGlobal {
		t.Fatal("fleet metrics invented account-only global limits")
	}
}

func TestCIHealthChecksTrackDirtySentAndRetryIntent(t *testing.T) {
	h := executionCheckFixture(t)
	row := h.admit(t, "health-check")
	publishExecutionCheckTest(t, h, "queued", "")
	if healthQueue(t, readHealth(t, h, h.account), "execution_checks").Pending != 0 {
		t.Fatal("settled version reported pending")
	}
	if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
		t.Fatal(err)
	}
	if healthQueue(t, readHealth(t, h, h.account), "execution_checks").Pending != 1 {
		t.Fatal("dirty sent version omitted")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='sending',attempts=1,lease_token=$2,lease_until=now()-interval '1 second' WHERE execution_id=$1", row.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	q := healthQueue(t, readHealth(t, h, h.account), "execution_checks")
	if q.InFlight != 1 || q.StaleLeases != 1 || q.Due != 1 {
		t.Fatalf("abandoned check claim: %+v", q)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='dead',attempts=$2,last_error='unavailable',lease_token=NULL,lease_until=NULL WHERE execution_id=$1", row.ID, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "INSERT INTO ci_execution_check_retries(id,account_id,execution_id,generation,key_digest) VALUES ($1,$2,$3,1,$4)", uuid.NewString(), h.account, row.ID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	health := readHealth(t, h, h.account)
	if healthQueue(t, health, "execution_checks").Dead != 1 || healthQueue(t, health, "check_retries").Pending != 1 {
		t.Fatal("dead check or pending retry omitted")
	}
	h.restart()
	publishExecutionCheckTest(t, h, "completed", "cancelled")
	health = readHealth(t, h, h.account)
	if healthQueue(t, health, "execution_checks").Dead != 0 || healthQueue(t, health, "check_retries").Pending != 0 {
		t.Fatal("recovered delivery remained unhealthy")
	}
}

func TestCIHealthWorkerRestartFencingAndBoundedSlots(t *testing.T) {
	h := newJobsHarness(t)
	claim := func(kind WorkerKind, enabled, ready bool) workerHealthLease {
		t.Helper()
		l, err := h.store.claimWorkerHealth(t.Context(), kind, uuid.NewString(), enabled, ready, 0)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	lease := claim(WorkerDispatcher, true, true)
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 1, false); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "healthy" {
		t.Fatal("restart lost worker evidence")
	}
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 2, true); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "degraded" {
		t.Fatal("failure not reported")
	}
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 2, false); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "degraded" {
		t.Fatal("heartbeat erased failed cycle")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_worker_health SET cycle_at=now()-interval '3 minutes' WHERE worker=$1", string(WorkerDispatcher)); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "stalled" {
		t.Fatal("fresh heartbeat masked stalled work")
	}
	// Replaying a committed pulse after losing its acknowledgement refreshes
	// presence only, never progress or the most recent cycle verdict.
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 2, false); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "stalled" {
		t.Fatal("lost pulse acknowledgement refreshed old progress")
	}
	replay, err := h.store.claimWorkerHealth(t.Context(), WorkerDispatcher, lease.token, true, true, 0)
	if err != nil || replay.slot != lease.slot || h.count(t, "SELECT count(*) FROM ci_worker_health") != 1 {
		t.Fatal("lost claim acknowledgement consumed another slot")
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "stalled" {
		t.Fatal("claim replay erased old progress")
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_worker_health SET heartbeat_at=now()-interval '2 minutes' WHERE worker=$1", string(WorkerDispatcher)); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "stale" {
		t.Fatal("crashed worker stayed healthy")
	}
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 1, false); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("expired incarnation resurrected itself")
	}
	replacement := claim(WorkerDispatcher, true, true)
	if replacement.slot != lease.slot || replacement.token == lease.token {
		t.Fatal("restart did not reclaim bounded slot")
	}
	if err := h.store.pulseWorkerHealth(t.Context(), lease, 1, false); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old process overwrote replacement")
	}
	if err := h.store.pulseWorkerHealth(t.Context(), replacement, 1, false); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < api.CIWorkerHealthSlots; i++ {
		claim(WorkerDispatcher, true, true)
	}
	if _, err := h.store.claimWorkerHealth(t.Context(), WorkerDispatcher, uuid.NewString(), true, true, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("diagnostics grew beyond slot cap")
	}
	claim(WorkerMetadataAdmission, false, true)
	claim(WorkerAutomationAdmission, true, false)
	health := readHealth(t, h, h.account)
	if healthWorker(t, health, WorkerMetadataAdmission).Status != "disabled" || healthWorker(t, health, WorkerAutomationAdmission).Status != "blocked" || healthWorker(t, health, WorkerExecutionChecks).Status != "unknown" {
		t.Fatal("disabled, blocked and unseen workers conflated")
	}
	if _, err := h.pool.Exec(t.Context(), "INSERT INTO ci_worker_health(worker,slot,token,enabled,ready) VALUES ('arbitrary',0,$1,true,true)", uuid.NewString()); err == nil {
		t.Fatal("unbounded worker kind accepted")
	}
	if _, err := h.pool.Exec(t.Context(), "INSERT INTO ci_worker_health(worker,slot,token,enabled,ready) VALUES ('dispatcher',$1,$2,true,true)", api.CIWorkerHealthSlots, uuid.NewString()); err == nil {
		t.Fatal("schema disagrees with diagnostic slot cap")
	}
	migration, err := os.ReadFile("../../migrations/20261016175839322_ci_health_observability.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	if _, err := h.pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CIHealth(t.Context(), h.account); err == nil {
		t.Fatal("missing observation table became healthy zeros")
	}
	if _, err := h.pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	if healthWorker(t, readHealth(t, h, h.account), WorkerDispatcher).Status != "unknown" {
		t.Fatal("recreated observation table invented worker health")
	}
}

type unavailableWorkerHealth struct{}

func (unavailableWorkerHealth) claimWorkerHealth(context.Context, WorkerKind, string, bool, bool, int64) (workerHealthLease, error) {
	return workerHealthLease{}, errors.New("private DB error")
}
func (unavailableWorkerHealth) pulseWorkerHealth(context.Context, workerHealthLease, int64, bool) error {
	return errors.New("private DB error")
}

func TestCIHealthObservationNeverBlocksWorkOrRunsDisabledWorker(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			ran := make(chan struct{})
			go func() {
				defer close(done)
				ObserveWorker(ctx, unavailableWorkerHealth{}, WorkerDispatcher, enabled, true, func(ctx context.Context) {
					close(ran)
					recordWorkerCycle(ctx, errors.New("never serialized"))
					<-ctx.Done()
				})
			}()
			if enabled {
				select {
				case <-ran:
				case <-time.After(2 * time.Second):
					t.Fatal("diagnostic DB failure blocked work")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("observation did not join")
			}
			if !enabled {
				select {
				case <-ran:
					t.Fatal("disabled worker ran")
				default:
				}
			}
		})
	}
	if (Config{}).HealthMonitoringEnabled() {
		t.Fatal("all-off config activated observations")
	}
}

func TestCIHealthSourceJournalsAndRetirementKeepPermanentQuota(t *testing.T) {
	h, _, cfg := automationFixture(t)
	off := cfg
	off.AutomationEnabled = false
	captureAutomationTest(t, h, off, strings.Repeat("a", 40))
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	health := readHealth(t, h, h.account)
	if healthQueue(t, health, "metadata_admission").Pending != 1 || healthQueue(t, health, "automation_admission").Pending != 1 || healthQuota(t, health, "source_events").Used != 2 {
		t.Fatal("source journals not combined or isolated")
	}
	if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.ClaimAutomationEvent(t.Context(), h.account); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ci_github_triggers", "ci_automation_events"} {
		// Static fixture table names only, never production SQL construction.
		if _, err := h.pool.Exec(t.Context(), "UPDATE "+table+" SET lease_until=now()-interval '1 second'"); err != nil {
			t.Fatal(err)
		}
	}
	health = readHealth(t, h, h.account)
	for _, name := range []string{"metadata_admission", "automation_admission"} {
		q := healthQueue(t, health, name)
		if q.InFlight != 1 || q.Due != 1 || q.StaleLeases != 1 {
			t.Fatalf("abandoned %s: %+v", name, q)
		}
	}
	execution, attempt, job := uuid.NewString(), uuid.NewString(), uuid.NewString()
	key := "ci/accounts/" + h.account + "/executions/" + execution + "/attempts/" + attempt + "/source/" + strings.Repeat("b", 64) + ".tar.gz"
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at,ready_at)
 VALUES ($1,$2,$3,$4,$5,$6,'source',$7,10,'ready',now()-interval '2 days',now()-interval '1 day',now()-interval '2 days')`, key, uuid.NewString(), h.account, execution, attempt, job, "sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	physical := newStoragePhysicalFake()
	physical.lostRetireAck = true
	blobs := &ManagedBlobs{Store: h.store, Physical: physical}
	if _, err := blobs.Sweep(t.Context()); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatal("lost provider acknowledgement not represented")
	}
	health = readHealth(t, h, h.account)
	if healthQueue(t, health, "retirement").Pending != 1 || healthQuota(t, health, "storage_bytes").Used != 10 || healthQuota(t, health, "storage_objects").Used != 1 || healthQuota(t, health, "storage_journal").Used != 1 {
		t.Fatal("ambiguous retirement prematurely freed quota")
	}
	h.restart()
	blobs.Store = h.store
	if n, err := blobs.Sweep(t.Context()); err != nil || n != 1 {
		t.Fatalf("retirement recovery: %d %v", n, err)
	}
	health = readHealth(t, h, h.account)
	if healthQueue(t, health, "retirement").Pending != 0 || healthQuota(t, health, "storage_bytes").Used != 0 || healthQuota(t, health, "storage_objects").Used != 0 || healthQuota(t, health, "storage_journal").Used != 1 || healthQuota(t, health, "source_events").Used != 2 {
		t.Fatal("retirement erased permanent quota evidence")
	}
}
