//go:build !no_pg

package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreExecutionProfilePinsImageBeforeRunning(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	row := exerciseExecutionProfilePin(t, store, ctx)
	for _, query := range []string{
		`update executions set profile='standard' where id=$1::uuid`,
		`update executions set runtime_image_digest=NULL where id=$1::uuid`,
		`update executions set runtime_image_digest='sha256:' || repeat('b',64) where id=$1::uuid`,
	} {
		if _, err := pool.Exec(ctx, query, row.ID); err == nil {
			t.Fatalf("database rewrote terminal provenance: %s", query)
		}
	}
}

func TestMemStoreExecutionProfilePinsImageBeforeRunning(t *testing.T) {
	exerciseExecutionProfilePin(t, state.NewMemStore(), context.Background())
}

func exerciseExecutionProfilePin(t *testing.T, store interface {
	state.ExecutionStore
	state.ExecutionRuntimeSelectionStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
}, ctx context.Context) state.Execution {
	t.Helper()
	account, err := store.CreateAccount(ctx, "profile-pin@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Profile: api.ExecutionProfilePythonDataV1, Source: "def main(input, context): return input"}
	resolved, problem := req.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatal(problem)
	}
	base := time.Now().UTC()
	created, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account.ID, Request: resolved, SourceBytes: len(req.Source), AdmittedAt: base, DeadlineAt: base.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond), SealedPayload: []byte("sealed"), PayloadKID: "kid"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "profile-test", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err == nil {
		t.Fatal("dispatched unpinned profile")
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := store.PinExecutionRuntime(ctx, created.ID, "wrong-lease", digest, base.Add(3*time.Millisecond)); err == nil {
		t.Fatal("stale lease pinned image")
	}
	for i := 0; i < 2; i++ {
		if _, err := store.PinExecutionRuntime(ctx, created.ID, *claim.LeaseToken, digest, base.Add(3*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PinExecutionRuntime(ctx, created.ID, *claim.LeaseToken, "sha256:"+strings.Repeat("b", 64), base.Add(4*time.Millisecond)); err == nil {
		t.Fatal("changed pinned image")
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, base.Add(5*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	row, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: []byte("null"), FinishedAt: base.Add(6 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	if row.Profile != api.ExecutionProfilePythonDataV1 || row.RuntimeImageDigest != digest {
		t.Fatalf("runtime provenance lost: %+v", row)
	}
	if _, err := store.PinExecutionRuntime(ctx, created.ID, *claim.LeaseToken, digest, base.Add(7*time.Millisecond)); err == nil {
		t.Fatal("terminal receipt was rewritten")
	}
	return row
}

func TestPgStoreRuntimeSnapshotRetainsProfile(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	record := state.RuntimeSnapshotRecord{Profile: api.ExecutionProfilePythonDataV1, Runtime: api.ExecutionRuntimePython313, Architecture: "amd64", KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64), MemoryMB: 128, EphemeralDiskMB: 64, FormatVersion: 1, StorageKey: "execution-snapshots/data/mem", SnapshotDigest: strings.Repeat("d", 64), MemBytes: 128 << 20, VMStateBytes: 4096, Sanitized: true, PayloadFree: true, State: state.RuntimeSnapshotStateReady, CreatedAt: time.Now().UTC()}
	record.CatalogKey = "execution-snapshots/v1/python313/profile-python-data-v1/amd64/memory-128/disk-64/kernel-" + record.KernelDigest + "/executor-" + record.GuestExecutorDigest + "/base-" + record.BaseImageDigest
	if _, err := store.PublishRuntimeSnapshot(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.LookupRuntimeSnapshot(ctx, record.CatalogKey)
	if err != nil || got.Profile != record.Profile {
		t.Fatalf("catalog lost profile: %+v, %v", got, err)
	}
	if _, err := pool.Exec(ctx, `update runtime_snapshots set profile='standard' where id=$1::uuid`, got.ID); err == nil {
		t.Fatal("database changed immutable snapshot profile")
	}
	if err := store.RetireRuntimeSnapshot(ctx, record.CatalogKey, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func pgExecutionParams(t *testing.T, accountID string, admittedAt time.Time, timeoutMS int, payload string) state.CreateExecutionParams {
	t.Helper()
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimePython313,
		Source:  "def main(input, context):\n    return input",
		Input:   []byte(`{"value":42}`),
	}
	if timeoutMS != 0 {
		request.Limits = &api.ExecutionLimitRequest{TimeoutMS: timeoutMS}
	}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatalf("resolve execution: %v", problem)
	}
	return state.CreateExecutionParams{
		AccountID: accountID, Request: resolved,
		SourceBytes: len(request.Source), InputBytes: len(request.Input),
		AdmittedAt: admittedAt, DeadlineAt: admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload: []byte(payload), PayloadKID: "pg-execution-key",
	}
}

func pgExecutionAccount(t *testing.T, store *state.PgStore, ctx context.Context, suffix string) state.Account {
	t.Helper()
	account, err := store.CreateAccount(ctx, "pg-execution-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func pgExecutionPayloadCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, executionID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from execution_payloads where execution_id=$1::uuid`, executionID).Scan(&count); err != nil {
		t.Fatalf("count execution payload: %v", err)
	}
	return count
}

func TestPgStoreExecutionLifecycleAndAtomicAdmission(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "lifecycle")
	base := time.Now().UTC().Add(time.Second)
	created, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 0, "sealed-pg-payload"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if pgExecutionPayloadCount(t, pool, ctx, created.ID) != 1 {
		t.Fatal("create did not atomically persist payload")
	}
	if _, err := store.ExecutionByID(ctx, "00000000-0000-0000-0000-000000000000", created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read error = %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-pg", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if claim.ID != created.ID || string(claim.SealedPayload) != "sealed-pg-payload" {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	exitCode := 0
	completed, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"value":42}`), ExitCode: &exitCode,
		Usage:      api.ExecutionUsage{WallTimeMS: 2, CPUTimeMS: 1, PeakMemoryMB: 10},
		FinishedAt: base.Add(3 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	if completed.Status != api.ExecutionStatusSucceeded || pgExecutionPayloadCount(t, pool, ctx, created.ID) != 0 {
		t.Fatalf("completion = %#v payload_count=%d", completed, pgExecutionPayloadCount(t, pool, ctx, created.ID))
	}
	var ledgerRows int
	if err := pool.QueryRow(ctx, `select count(*) from execution_usage_ledger where execution_id=$1::uuid`, created.ID).Scan(&ledgerRows); err != nil {
		t.Fatalf("count execution usage ledger: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("execution usage ledger rows = %d, want 1", ledgerRows)
	}
	usage, err := store.ExecutionUsageByAccount(ctx, account.ID, base)
	if err != nil {
		t.Fatalf("ExecutionUsageByAccount: %v", err)
	}
	if usage.Runs != 1 || usage.WallTimeMS != 2 || usage.CPUTimeMS != 1 || usage.PeakMemoryMB != 10 || usage.Succeeded != 1 {
		t.Fatalf("execution usage = %+v, want one succeeded ledger row", usage)
	}
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed,
		FinishedAt: base.Add(4 * time.Millisecond),
	}); !errors.Is(err, state.ErrExecutionLeaseLost) {
		t.Fatalf("stale completion error = %v", err)
	}
	if _, err := pool.Exec(ctx, `update executions set status='failed' where id=$1::uuid`, created.ID); err == nil {
		t.Fatal("database allowed terminal state rewrite")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
			t.Fatalf("terminal rewrite error = %v", err)
		}
	}

	if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	params := pgExecutionParams(t, account.ID, base.Add(time.Second), 0, "atomic")
	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.CreateExecution(context.Background(), params)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount, quotaCount := 0, 0
	for err := range results {
		if err == nil {
			createdCount++
		} else if errors.Is(err, state.ErrExecutionQuotaExceeded) {
			quotaCount++
		} else {
			t.Fatalf("parallel admission: %v", err)
		}
	}
	if createdCount != 1 || quotaCount != callers-1 {
		t.Fatalf("parallel admission created=%d quota=%d", createdCount, quotaCount)
	}
}

func TestPgStoreExecutionOutboundIntegrationsAreGrantedAndClaimed(t *testing.T) {
	store, ctx := pgStore(t)
	account := pgExecutionAccount(t, store, ctx, "outbound-integration")
	offer := pgCustomerOutboundOffer(account.ID, "agent-api")
	createdOffer, err := store.CreateOutboundIntegration(ctx, offer)
	if err != nil {
		t.Fatalf("CreateOutboundIntegration: %v", err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, createdOffer.ID, []byte("sealed-provider-credential")); err != nil {
		t.Fatalf("SetOutboundCredential: %v", err)
	}
	base := time.Now().UTC().Add(time.Second)
	params := pgExecutionParams(t, account.ID, base, 0, "sealed-run")
	params.OutboundIntegrationIDs = []string{createdOffer.ID}
	if _, err := store.CreateExecution(ctx, params); !errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable) {
		t.Fatalf("CreateExecution without Runs grant = %v, want unavailable", err)
	}
	if err := store.SetOutboundIntegrationRunsEnabled(ctx, account.ID, createdOffer.ID, true); err != nil {
		t.Fatalf("SetOutboundIntegrationRunsEnabled: %v", err)
	}
	created, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatalf("CreateExecution with granted integration: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "integration-claim", base.Add(time.Millisecond), time.Minute)
	if err != nil || claim.ID != created.ID {
		t.Fatalf("ClaimExecution = %q, %v; want %q", claim.ID, err, created.ID)
	}
	if len(claim.OutboundIntegrationIDs) != 1 || claim.OutboundIntegrationIDs[0] != createdOffer.ID {
		t.Fatalf("claim integration IDs = %v, want [%s]", claim.OutboundIntegrationIDs, createdOffer.ID)
	}

	if err := store.SetOutboundIntegrationRunsEnabled(ctx, account.ID, createdOffer.ID, false); err != nil {
		t.Fatalf("revoke Runs grant: %v", err)
	}
	params = pgExecutionParams(t, account.ID, base.Add(2*time.Millisecond), 0, "sealed-run-2")
	params.OutboundIntegrationIDs = []string{createdOffer.ID}
	if _, err := store.CreateExecution(ctx, params); !errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable) {
		t.Fatalf("CreateExecution after grant revocation = %v, want unavailable", err)
	}
}

func TestPgStoreExecutionWorkflowQueriesKeepPrincipalScope(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "workflow-scope")
	principalA := "11111111-1111-4111-8111-111111111111"
	principalB := "22222222-2222-4222-8222-222222222222"
	workflowID := "pg-agent-workflow"
	base := time.Now().UTC().Add(time.Second)
	create := func(admittedAt time.Time, principalID, flowID string) state.Execution {
		params := pgExecutionParams(t, account.ID, admittedAt, 0, "sealed-workflow")
		params.RunsPrincipalID = &principalID
		params.WorkflowID = flowID
		row, err := store.CreateExecution(ctx, params)
		if err != nil {
			t.Fatalf("CreateExecution(%s): %v", flowID, err)
		}
		return row
	}
	first := create(base, principalA, workflowID)
	create(base.Add(time.Millisecond), principalB, workflowID)
	create(base.Add(2*time.Millisecond), principalA, "private-agent-flow")

	claim, err := store.ClaimExecution(ctx, "workflow-test", base.Add(3*time.Millisecond), time.Second)
	if err != nil || claim.ID != first.ID {
		t.Fatalf("ClaimExecution = %q, %v; want %q", claim.ID, err, first.ID)
	}
	if _, err := store.MarkExecutionRunning(ctx, first.ID, *claim.LeaseToken, base.Add(4*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: first.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte("null"), Usage: api.ExecutionUsage{WallTimeMS: 5, CPUTimeMS: 3, PeakMemoryMB: 64},
		FinishedAt: base.Add(5 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		principal *string
		wantRuns  int
		wantState api.ExecutionStatus
	}{{&principalA, 1, api.ExecutionStatusSucceeded}, {&principalB, 1, api.ExecutionStatusQueued}, {nil, 2, ""}} {
		rows, err := store.ListExecutionsByWorkflow(ctx, account.ID, workflowID, check.principal, "", 10, 0)
		if err != nil || len(rows) != check.wantRuns {
			t.Fatalf("workflow list principal=%v: rows=%#v err=%v", check.principal, rows, err)
		}
		summary, err := store.ExecutionWorkflowSummary(ctx, account.ID, workflowID, check.principal)
		if err != nil || summary.RunCount != int64(check.wantRuns) {
			t.Fatalf("workflow summary principal=%v: summary=%+v err=%v", check.principal, summary, err)
		}
		if check.wantState != "" && rows[0].Status != check.wantState {
			t.Fatalf("workflow receipt status=%s, want %s", rows[0].Status, check.wantState)
		}
		if check.principal != nil && *check.principal == principalA && (summary.Usage.WallTimeMS != 5 || summary.Usage.CPUTimeMS != 3 || summary.Usage.PeakMemoryMB != 64) {
			t.Fatalf("agent A workflow usage = %+v", summary.Usage)
		}
	}
	if _, err := store.ExecutionWorkflowSummary(ctx, account.ID, "private-agent-flow", &principalB); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-principal workflow lookup = %v, want ErrNotFound", err)
	}
}

func TestPgStoreAgentWorkflowStepAdmissionIsUniquePerPrincipal(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "workflow-step-unique")
	principalA := "11111111-1111-4111-8111-111111111111"
	principalB := "22222222-2222-4222-8222-222222222222"
	base := time.Now().UTC().Add(time.Second)
	params := pgExecutionParams(t, account.ID, base, 0, "sealed-agent-step")
	params.WorkflowID = "pg-workflow-step-unique"
	params.StepLabel = "gwf:0123456789abcdef01234567:inspect"
	params.RunsPrincipalID = &principalA
	if _, err := store.CreateExecution(ctx, params); err != nil {
		t.Fatalf("CreateExecution(first step): %v", err)
	}
	duplicate := pgExecutionParams(t, account.ID, base.Add(time.Millisecond), 0, "sealed-agent-step-again")
	duplicate.WorkflowID = params.WorkflowID
	duplicate.StepLabel = params.StepLabel
	duplicate.RunsPrincipalID = &principalA
	if _, err := store.CreateExecution(ctx, duplicate); !errors.Is(err, state.ErrExecutionWorkflowStepExists) {
		t.Fatalf("CreateExecution(duplicate step) = %v, want workflow step conflict", err)
	}
	otherPrincipal := pgExecutionParams(t, account.ID, base.Add(2*time.Millisecond), 0, "sealed-other-agent-step")
	otherPrincipal.WorkflowID = params.WorkflowID
	otherPrincipal.StepLabel = params.StepLabel
	otherPrincipal.RunsPrincipalID = &principalB
	if _, err := store.CreateExecution(ctx, otherPrincipal); err != nil {
		t.Fatalf("CreateExecution(other key family): %v", err)
	}
}

func TestPgStoreExecutionArtifactGrantRedemptionIsAtomic(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "artifact-grant")
	base := time.Now().UTC().Add(time.Second)
	source, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 0, "source-payload"))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "artifact-grant-pg", base.Add(time.Millisecond), time.Minute)
	if err != nil || claim.ID != source.ID {
		t.Fatalf("claim source = %q, %v", claim.ID, err)
	}
	if _, err := store.MarkExecutionRunning(ctx, source.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	content := []byte("dataset\n1\n")
	digest := sha256.Sum256(content)
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: source.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte("null"), Artifacts: []api.ExecutionArtifact{{Name: "dataset.csv", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:])}},
		FinishedAt: base.Add(3 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}

	principal := "a1fb8f25-f2e1-4e44-901a-5111c449ce39"
	secret := []byte("rag_single-use-capability-test")
	tokenHash := sha256.Sum256(secret)
	grant, err := store.CreateExecutionArtifactGrant(ctx, state.CreateExecutionArtifactGrantParams{
		ID: "e78003cd-46c3-49ab-a1b1-dc4a503ea019", AccountID: account.ID,
		SourceExecutionID: source.ID, ArtifactName: "dataset.csv", CreatorPrincipalID: &principal,
		TokenHash: tokenHash[:], ExpiresAt: base.Add(5 * time.Minute), CreatedAt: base,
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	loaded, err := store.ExecutionArtifactGrantByToken(ctx, account.ID, tokenHash[:], base.Add(time.Second))
	if err != nil || loaded.ID != grant.ID {
		t.Fatalf("load grant: %#v, %v", loaded, err)
	}

	params := pgExecutionParams(t, account.ID, base.Add(10*time.Millisecond), 0, "consumer-payload")
	consumerPrincipal := "50a9b1e6-1b81-46e0-a447-12c6c69fb0b1"
	params.RunsPrincipalID = &consumerPrincipal
	params.ArtifactGrantRedemptions = []state.ExecutionArtifactGrantRedemption{{GrantID: grant.ID, TokenHash: tokenHash[:]}}
	consumer, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatalf("create consumer execution: %v", err)
	}
	var redeemedAt time.Time
	var redeemedExecutionID string
	if err := pool.QueryRow(ctx, `select redeemed_at, redeemed_execution_id::text from execution_artifact_grants where id=$1::uuid`, grant.ID).Scan(&redeemedAt, &redeemedExecutionID); err != nil {
		t.Fatalf("read grant redemption: %v", err)
	}
	if redeemedAt.IsZero() || redeemedExecutionID != consumer.ID {
		t.Fatalf("grant redemption = %v / %s, want consumer %s", redeemedAt, redeemedExecutionID, consumer.ID)
	}
	replay := pgExecutionParams(t, account.ID, base.Add(20*time.Millisecond), 0, "second-consumer")
	replay.ArtifactGrantRedemptions = params.ArtifactGrantRedemptions
	if _, err := store.CreateExecution(ctx, replay); !errors.Is(err, state.ErrExecutionArtifactGrantUnavailable) {
		t.Fatalf("second grant redemption = %v, want unavailable", err)
	}
}

func TestPgStoreExecutionSweepRecovery(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "sweep")
	base := time.Now().UTC().Add(time.Second)
	expired, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base, 100, "expired"))
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	restore, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base.Add(time.Millisecond), 5000, "restore"))
	if err != nil {
		t.Fatalf("create restore: %v", err)
	}
	running, err := store.CreateExecution(ctx, pgExecutionParams(t, account.ID, base.Add(2*time.Millisecond), 5000, "running"))
	if err != nil {
		t.Fatalf("create running: %v", err)
	}
	restoreClaim, err := store.ClaimExecution(ctx, "schedd-a", base.Add(150*time.Millisecond), 50*time.Millisecond)
	if err != nil || restoreClaim.ID != restore.ID {
		t.Fatalf("restore claim id=%s err=%v", restoreClaim.ID, err)
	}
	runningClaim, err := store.ClaimExecution(ctx, "schedd-b", base.Add(151*time.Millisecond), 50*time.Millisecond)
	if err != nil || runningClaim.ID != running.ID {
		t.Fatalf("running claim id=%s err=%v", runningClaim.ID, err)
	}
	if _, err := store.MarkExecutionRunning(ctx, running.ID, *runningClaim.LeaseToken, base.Add(152*time.Millisecond)); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	sweep, err := store.SweepExecutions(ctx, base.Add(300*time.Millisecond), 10)
	if err != nil {
		t.Fatalf("SweepExecutions: %v", err)
	}
	if sweep.ExpiredQueued != 1 || sweep.RequeuedRestores != 1 || sweep.FinishedRuns != 1 || sweep.PayloadsDeleted != 2 {
		t.Fatalf("sweep = %#v", sweep)
	}
	expiredRow, _ := store.ExecutionByID(ctx, account.ID, expired.ID)
	restoreRow, _ := store.ExecutionByID(ctx, account.ID, restore.ID)
	runningRow, _ := store.ExecutionByID(ctx, account.ID, running.ID)
	if expiredRow.Status != api.ExecutionStatusTimedOut || restoreRow.Status != api.ExecutionStatusQueued ||
		runningRow.Status != api.ExecutionStatusFailed || runningRow.FailureCode == nil || *runningRow.FailureCode != "lease_expired" {
		t.Fatalf("states expired=%q restore=%q running=%#v", expiredRow.Status, restoreRow.Status, runningRow)
	}
	if pgExecutionPayloadCount(t, pool, ctx, expired.ID) != 0 ||
		pgExecutionPayloadCount(t, pool, ctx, running.ID) != 0 ||
		pgExecutionPayloadCount(t, pool, ctx, restore.ID) != 1 {
		t.Fatal("sweep payload retention diverged from lifecycle state")
	}
	usage, err := store.ExecutionUsageByAccount(ctx, account.ID, base)
	if err != nil {
		t.Fatalf("ExecutionUsageByAccount: %v", err)
	}
	if usage.Runs != 2 || usage.TimedOut != 1 || usage.Failed != 1 || usage.Succeeded != 0 {
		t.Fatalf("sweep usage = %+v, want timed_out=1 failed=1 exactly once", usage)
	}
}

func TestPgStoreExecutionArtifactsReceiptsAndAccounting(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "artifacts")
	exerciseExecutionArtifacts(t, store, account.ID)
}

func TestMemStoreExecutionArtifactsReceiptsAndAccounting(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "artifacts@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	exerciseExecutionArtifacts(t, store, account.ID)
}

func exerciseExecutionArtifacts(t *testing.T, store state.ExecutionStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Add(time.Second)
	params := pgExecutionParams(t, accountID, base, 0, "sealed")
	params.Request.Limits.MaxOutputBytes = 1024
	row, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "artifacts", base.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkExecutionRunning(ctx, row.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	content := []byte("a,b\n1,2\n")
	hash := sha256.Sum256(content)
	artifacts := []api.ExecutionArtifact{{Name: "data.csv", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(hash[:])}}
	completion := state.CompleteExecutionParams{ID: row.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: []byte("null"), Artifacts: artifacts, FinishedAt: base.Add(3 * time.Millisecond)}
	completion.Stdout = strings.Repeat("a", 1000)
	if _, err := store.CompleteExecution(ctx, completion); !errors.Is(err, state.ErrExecutionInvalidTerminal) {
		t.Fatalf("combined output limit: %v", err)
	}
	completion.Stdout = ""
	completed, err := store.CompleteExecution(ctx, completion)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Artifacts) != 1 || string(completed.Artifacts[0].Content) != string(content) {
		t.Fatalf("receipt lost artifacts: %+v", completed)
	}
	completed.Artifacts[0].Content[0] = 'X'
	read, err := store.ExecutionByID(ctx, accountID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Artifacts[0].Content) != string(content) {
		t.Fatal("receipt content aliased mutable memory")
	}
	if _, err := store.ExecutionByID(ctx, "00000000-0000-0000-0000-000000000000", row.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
	usageStore := store.(state.ExecutionUsageStore)
	month := time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC)
	usage, err := usageStore.ExecutionUsageByAccount(ctx, accountID, month)
	if err != nil {
		t.Fatal(err)
	}
	if usage.OutputBytes != int64(4+api.ExecutionArtifactsOutputBytes(artifacts)) {
		t.Fatalf("output bytes = %d", usage.OutputBytes)
	}
}
