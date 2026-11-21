package toolregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestMCPHostedTaskRehearsalMemory(t *testing.T) {
	exerciseHostedTaskRehearsal(t, state.NewMemStore(), nil)
}

func TestMCPHostedTaskRehearsalPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseHostedTaskRehearsal(t, state.NewPgStore(pool), pool)
}

func exerciseHostedTaskRehearsal(t *testing.T, store state.Store, pool *pgxpool.Pool) {
	t.Helper()
	f, _, opts := seedHostedTaskBindingFixture(t, store, pool)
	opts.RehearsalPlanEnabled = true
	f.executor = toolregistry.NewPrivateExecutor(store, opts)
	t.Cleanup(f.executor.Close)
	ctx := t.Context()
	owner := rehearsalTestOwner(ctx, t, store, f.account.ID, "rehearsal-owner")
	other := rehearsalTestOwner(ctx, t, store, f.account.ID, "other-admin")
	journal := store.(state.HostedTaskRehearsalStore)
	run := func(id string, executor *toolregistry.PrivateExecutor, key *state.APIKey, method, suffix string, in any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		requestContext := middleware.WithPrincipal(ctx, f.account, key, nil)
		req := httptest.NewRequestWithContext(requestContext, method, "/v1/task-tool-source-intents/"+id+"/rehearsal"+suffix, bytes.NewReader(raw))
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		executor.HostedTaskRehearsal()(w, req, f.account)
		return w
	}
	read := func(id string, w *httptest.ResponseRecorder, status string) api.HostedTaskRehearsalPlan {
		t.Helper()
		var out api.HostedTaskRehearsalPlan
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Status != status ||
			tasktoolrehearsal.Validate(out, f.account.ID, id) != nil || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("want %s: %d %s", status, w.Code, w.Body)
		}
		for _, private := range []string{owner.ID, other.ID, "private-owner", "MIGRATION_URL", "binding-test", "db.example.com", "ClaimAcquired", "ReservedAt", "ApprovalExpiresAt"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private rehearsal data exposed")
			}
		}
		return out
	}

	id, pinned := completeRehearsalQualification(t, f, "claim")
	disabled := toolregistry.NewPrivateExecutor(store, toolregistry.PrivateExecutionOptions{})
	t.Cleanup(disabled.Close)
	if w := run(id, disabled, &owner, http.MethodPut, "", pinned); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled plan: %d", w.Code)
	}
	for _, key := range []*state.APIKey{&f.key, nil} {
		if w := run(id, f.executor, key, http.MethodPut, "", pinned); w.Code == http.StatusOK {
			t.Fatal("non-personal-admin plan accepted")
		}
	}
	spoofed := map[string]any{"intent_sha256": pinned.IntentSHA256, "review_sha256": pinned.ReviewSHA256, "binding_sha256": pinned.BindingSHA256, "target_id": uuid.NewString()}
	if w := run(id, f.executor, &owner, http.MethodPut, "", spoofed); w.Code != http.StatusBadRequest {
		t.Fatalf("caller chose a target: %d", w.Code)
	}
	plan := read(id, run(id, f.executor, &owner, http.MethodPut, "", pinned), tasktoolrehearsal.Planned)
	retry := read(id, run(id, f.executor, &owner, http.MethodPut, "", pinned), tasktoolrehearsal.Planned)
	if plan.PlanSHA256 != retry.PlanSHA256 || plan.TargetID != retry.TargetID || !plan.ExpiresAt.Equal(retry.ExpiresAt) {
		t.Fatal("plan retry renewed immutable identity")
	}
	claim := state.HostedTaskRehearsalChange{OwnerKeyID: owner.ID, PlanSHA256: plan.PlanSHA256}
	if out, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.ClaimHostedTaskRehearsal, claim); err != nil || out.ClaimAcquired || out.Run != nil {
		t.Fatalf("unapproved claim acquired: %v", err)
	}
	decision := api.HostedTaskRehearsalDecision{PlanSHA256: plan.PlanSHA256, Decision: "approve"}
	wrong := api.HostedTaskRehearsalDecision{PlanSHA256: toolpublication.Digest([]byte("different plan")), Decision: decision.Decision}
	if w := run(id, f.executor, &owner, http.MethodPut, "/decision", wrong); w.Code != http.StatusConflict {
		t.Fatalf("wrong plan approved: %d", w.Code)
	}
	approve := read(id, run(id, f.executor, &owner, http.MethodPut, "/decision", decision), tasktoolrehearsal.Approved)
	replay := read(id, run(id, f.executor, &owner, http.MethodPut, "/decision", decision), tasktoolrehearsal.Approved)
	if !approve.ApprovedAt.Equal(*replay.ApprovedAt) {
		t.Fatal("approval renewed its timestamp")
	}
	changedOwner := claim
	changedOwner.OwnerKeyID = other.ID
	if out, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.ClaimHostedTaskRehearsal, changedOwner); !errors.Is(err, state.ErrConflict) || out.ClaimAcquired || out.Run != nil {
		t.Fatalf("a different key acquired the approval: %v", err)
	}
	if pool != nil {
		assertHostedTaskRunReservationRollback(ctx, t, pool, journal, approve, claim)
	}
	var wg sync.WaitGroup
	type attempt struct {
		out state.HostedTaskRehearsalResult
		err error
	}
	attempts := make(chan attempt, 4)
	for range 4 {
		wg.Go(func() {
			out, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.ClaimHostedTaskRehearsal, claim)
			attempts <- attempt{out: out, err: err}
		})
	}
	wg.Wait()
	close(attempts)
	acquired := 0
	var reservation state.HostedTaskRehearsalRun
	for a := range attempts {
		if a.err != nil || a.out.Plan.Status != tasktoolrehearsal.Claimed || a.out.Run == nil || a.out.Plan.ClaimedAt == nil {
			t.Fatalf("claim failed: %+v, %v", a.out, a.err)
		}
		got := *a.out.Run
		if got.ID != plan.TargetID || got.PlanSHA256 != plan.PlanSHA256 ||
			!got.ReservedAt.Equal(*a.out.Plan.ClaimedAt) || !got.ApprovalExpiresAt.Equal(plan.ExpiresAt) {
			t.Fatalf("run reservation did not preserve the approved plan: %+v", got)
		}
		if reservation.ID != "" && reservation != got {
			t.Fatalf("concurrent claim reserved a different run: %+v", got)
		}
		reservation = got
		// The result is a copy, including on the in-memory journal.
		a.out.Run.ID = uuid.NewString()
		if a.out.ClaimAcquired {
			acquired++
		}
	}
	if acquired != 1 {
		t.Fatalf("expected one claim, got %d", acquired)
	}
	if pool != nil {
		journal = state.NewPgStore(pool)
	}
	if out, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.ClaimHostedTaskRehearsal, claim); err != nil ||
		out.ClaimAcquired || out.Plan.Status != tasktoolrehearsal.Claimed || out.Run == nil || *out.Run != reservation {
		t.Fatalf("durable claim replay: %+v, %v", out, err)
	}
	read(id, run(id, disabled, &owner, http.MethodGet, "", nil), tasktoolrehearsal.Claimed)

	id, pinned = completeRehearsalQualification(t, f, "drift")
	plan = read(id, run(id, f.executor, &owner, http.MethodPut, "", pinned), tasktoolrehearsal.Planned)
	decision.PlanSHA256 = plan.PlanSHA256
	read(id, run(id, f.executor, &owner, http.MethodPut, "/decision", decision), tasktoolrehearsal.Approved)
	// Revoke the source approval without changing its immutable digests.
	source := store.(state.HostedTaskSourceIntentStore)
	if _, err := source.ChangeHostedTaskSourceIntent(ctx, f.account.ID, id, "decide", api.HostedTaskSourceDecision{
		IntentSHA256: pinned.IntentSHA256, ReviewSHA256: pinned.ReviewSHA256, Decision: "revoke",
	}, state.HostedTaskSourceOwner{KeyID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	claim.PlanSHA256 = plan.PlanSHA256
	if out, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.ClaimHostedTaskRehearsal, claim); err != nil || out.ClaimAcquired || out.Plan.Status != tasktoolrehearsal.Stale || out.Run != nil {
		t.Fatalf("source revocation did not fence the claim: %+v, %v", out, err)
	}
	read(id, run(id, disabled, &other, http.MethodGet, "", nil), tasktoolrehearsal.Stale)

}

func rehearsalTestOwner(ctx context.Context, t *testing.T, store state.Store, account, label string) state.APIKey {
	t.Helper()
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, account, hash, label, api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestMCPHostedTaskRehearsalCandidateMemory(t *testing.T) {
	exerciseHostedTaskRehearsalCandidate(t, state.NewMemStore(), nil)
}

func TestMCPHostedTaskRehearsalCandidatePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseHostedTaskRehearsalCandidate(t, state.NewPgStore(pool), pool)
}

func exerciseHostedTaskRehearsalCandidate(t *testing.T, store state.Store, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	f, _, opts := seedHostedTaskBindingFixture(t, store, pool)
	opts.RehearsalPlanEnabled = true
	f.executor = toolregistry.NewPrivateExecutor(store, opts)
	t.Cleanup(f.executor.Close)
	owner := rehearsalTestOwner(ctx, t, store, f.account.ID, "candidate-owner").ID
	other := rehearsalTestOwner(ctx, t, store, f.account.ID, "candidate-other").ID
	worker := append([]api.HostedTaskSourceEntry(nil), f.request.Sources["worker"]...)
	worker = append(worker, api.HostedTaskSourceEntry{Path: "worker-marker.txt", Mode: 0600, Content: []byte("reviewed worker only")})
	digest, err := tasktoolsource.Digest(worker)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Sources["worker"] = worker
	f.review.Sources["worker"] = digest
	f.review.Fingerprint = tasktoolsource.Fingerprint(f.review)
	f.request.Review, err = json.Marshal(f.review)
	if err != nil {
		t.Fatal(err)
	}
	id, pinned := completeRehearsalQualification(t, f, "candidate")
	journal := f.store.(state.HostedTaskRehearsalStore)
	in := state.HostedTaskRehearsalChange{OwnerKeyID: owner, Prepare: pinned}
	prepared, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.PrepareHostedTaskRehearsal, in)
	if err != nil {
		t.Fatal(err)
	}
	plan := prepared.Plan
	in.Decision = api.HostedTaskRehearsalDecision{PlanSHA256: plan.PlanSHA256, Decision: "approve"}
	if _, err := journal.ChangeHostedTaskRehearsal(ctx, f.account.ID, id, state.DecideHostedTaskRehearsal, in); err != nil {
		t.Fatal(err)
	}
	runnerOpts := opts
	runnerOpts.TaskRehearsalProvisioner = &taskrehearsal.Provisioner{AdmissionEnabled: true}
	runnerOpts.TaskRehearsalRunner = &taskrehearsal.Runner{AdmissionEnabled: true}
	runner := toolregistry.NewPrivateExecutor(store, runnerOpts)
	t.Cleanup(runner.Close)
	hostErr := errors.New("acceptance host unavailable")
	host := &smokeHostCheck{err: hostErr}
	blocked, evidence, err := runner.SmokeHostedTaskRehearsal(ctx, f.account, id, owner, plan.PlanSHA256, host)
	if !errors.Is(err, hostErr) || blocked.ID != "" || evidence != (tasktoolrehearsal.SmokeEvidence{}) || host.accountID != f.account.ID {
		t.Fatalf("host rejection must occur before consuming approval: %v", err)
	}
	if out, _, err := runner.RunHostedTaskRehearsal(ctx, f.account, id, owner, plan.PlanSHA256); !errors.Is(err, state.ErrConflict) || out.ID != "" {
		t.Fatalf("runner claimed a source without a reviewed migration entry: %v", err)
	}
	if out, err := f.executor.ClaimHostedTaskRehearsalCandidate(ctx, f.account.ID, id, other, plan.PlanSHA256); !errors.Is(err, state.ErrConflict) || out.Run.ID != "" {
		t.Fatalf("another owner received the candidate: %v", err)
	}
	wrongDigest := toolpublication.Digest([]byte("a different rehearsal plan"))
	if out, err := f.executor.ClaimHostedTaskRehearsalCandidate(ctx, f.account.ID, id, owner, wrongDigest); !errors.Is(err, state.ErrConflict) || out.Run.ID != "" {
		t.Fatalf("candidate was handed off for the wrong plan: %v", err)
	}
	opts.ConnectionIdentities = nil
	unavailable := toolregistry.NewPrivateExecutor(f.store, opts)
	t.Cleanup(unavailable.Close)
	if out, err := unavailable.ClaimHostedTaskRehearsalCandidate(ctx, f.account.ID, id, owner, plan.PlanSHA256); !errors.Is(err, state.ErrConflict) || out.Run.ID != "" {
		t.Fatalf("an unopenable candidate reached the claim: %v", err)
	}

	archives := map[string][]byte{}
	for role, entries := range f.request.Sources {
		digest, err := tasktoolsource.Digest(entries)
		if err != nil {
			t.Fatal(err)
		}
		archives[role], err = tasktoolsource.PackReviewedArchive(entries, digest)
		if err != nil {
			t.Fatal(err)
		}
	}
	type attempt struct {
		candidate toolregistry.HostedTaskRehearsalCandidate
		err       error
	}
	attempts := make(chan attempt, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			out, err := f.executor.ClaimHostedTaskRehearsalCandidate(ctx, f.account.ID, id, owner, plan.PlanSHA256)
			attempts <- attempt{out, err}
		})
	}
	wg.Wait()
	close(attempts)
	withArchives := 0
	for a := range attempts {
		if a.err != nil || a.candidate.Run.ID != plan.TargetID || a.candidate.Run.PlanSHA256 != plan.PlanSHA256 {
			t.Fatalf("candidate claim failed: %v", a.err)
		}
		if len(a.candidate.WebArchive) > 0 || len(a.candidate.WorkerArchive) > 0 {
			withArchives++
			if !bytes.Equal(a.candidate.WebArchive, archives["web"]) || !bytes.Equal(a.candidate.WorkerArchive, archives["worker"]) {
				t.Fatal("claim handed off different source bytes")
			}
			receipt, err := json.Marshal(a.candidate)
			if err != nil || bytes.Contains(receipt, []byte("Archive")) {
				t.Fatal("private source archives appeared in the encoded result")
			}
		}
	}
	if withArchives != 1 {
		t.Fatalf("archives handed off %d times", withArchives)
	}
	replay, err := unavailable.ClaimHostedTaskRehearsalCandidate(ctx, f.account.ID, id, owner, plan.PlanSHA256)
	if err != nil || replay.Run.ID != plan.TargetID || len(replay.WebArchive) != 0 || len(replay.WorkerArchive) != 0 {
		t.Fatalf("replay attempted to reopen retained source: %v", err)
	}

	// A provisioner with no store would fail if called. Even a fresh
	// executor can replay only the original reservation.
	opts.TaskRehearsalProvisioner = &taskrehearsal.Provisioner{AdmissionEnabled: true}
	restarted := toolregistry.NewPrivateExecutor(f.store, opts)
	t.Cleanup(restarted.Close)
	reserved := replay.Run
	runReplay, execution, err := runner.RunHostedTaskRehearsal(ctx, f.account, id, owner, plan.PlanSHA256)
	if err != nil || runReplay.ID != reserved.ID || execution != (taskrehearsal.Execution{}) {
		t.Fatalf("runner replay started work for an existing reservation: %v", err)
	}
	host.err = nil
	smokeReplay, evidence, err := runner.SmokeHostedTaskRehearsal(ctx, f.account, id, owner, plan.PlanSHA256, host)
	if err != nil || smokeReplay.ID != reserved.ID || evidence != (tasktoolrehearsal.SmokeEvidence{}) {
		t.Fatalf("smoke replay exercised an existing rehearsal: %v", err)
	}
	replayed, target, err := restarted.ProvisionHostedTaskRehearsal(ctx, f.account, id, owner, plan.PlanSHA256)
	if err != nil || target != nil || replayed.Run.ID != reserved.ID || replayed.Run.PlanSHA256 != reserved.PlanSHA256 ||
		!replayed.Run.ReservedAt.Equal(reserved.ReservedAt) || !replayed.Run.ApprovalExpiresAt.Equal(reserved.ApprovalExpiresAt) ||
		len(replayed.WebArchive) != 0 || len(replayed.WorkerArchive) != 0 {
		t.Fatalf("provisioning replay did more than return its reservation: %v", err)
	}
}

type smokeHostCheck struct {
	tasktoolrehearsal.SmokeDriver
	accountID string
	err       error
}

func (d *smokeHostCheck) CheckHost(_ context.Context, accountID string) error {
	d.accountID = accountID
	return d.err
}

func assertHostedTaskRunReservationRollback(ctx context.Context, t *testing.T, pool *pgxpool.Pool, journal state.HostedTaskRehearsalStore, plan api.HostedTaskRehearsalPlan, claim state.HostedTaskRehearsalChange) {
	t.Helper()
	// A conflicting reservation forces a native insertion failure for this
	// fixture only. The approval must remain available after rollback.
	if _, err := pool.Exec(ctx, `INSERT INTO hosted_task_rehearsal_runs
		(account_id, id, run_id, plan_sha256, reserved_at, approval_expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		plan.AccountID, plan.IntentID, plan.TargetID, plan.PlanSHA256, plan.ApprovedAt, plan.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	out, err := journal.ChangeHostedTaskRehearsal(ctx, plan.AccountID, plan.IntentID, state.ClaimHostedTaskRehearsal, claim)
	var conflict *pgconn.PgError
	if !errors.As(err, &conflict) || conflict.Code != "23505" || out.ClaimAcquired || out.Run != nil {
		t.Fatalf("reservation collision did not reject the claim: %+v, %v", out, err)
	}
	out, err = journal.ChangeHostedTaskRehearsal(ctx, plan.AccountID, plan.IntentID, state.ReadHostedTaskRehearsal, claim)
	if err != nil || out.Plan.Status != tasktoolrehearsal.Approved || out.Plan.ClaimedAt != nil {
		t.Fatalf("failed run reservation committed an approval claim: %+v, %v", out, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM hosted_task_rehearsal_runs WHERE account_id=$1 AND id=$2`,
		plan.AccountID, plan.IntentID); err != nil {
		t.Fatal(err)
	}
}

func completeRehearsalQualification(t *testing.T, f *hostedSourceFixture, name string) (string, api.HostedTaskRehearsalPrepare) {
	t.Helper()
	ctx := t.Context()
	id := toolpublication.Digest([]byte(f.account.ID + ":rehearsal:" + name))
	w := f.call(t, http.MethodPut, id, f.request, &f.key)
	var source api.HostedTaskSourceIntent
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &source) != nil {
		t.Fatalf("source prepare: %d %s", w.Code, w.Body)
	}
	owner := state.HostedTaskSourceOwner{Session: true}
	if _, err := f.store.(state.HostedTaskSourceIntentStore).ChangeHostedTaskSourceIntent(ctx, f.account.ID, id, "decide", api.HostedTaskSourceDecision{
		IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Decision: "approve",
	}, owner); err != nil {
		t.Fatal(err)
	}
	selection := api.HostedTaskBindingSelection{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Namespace: "binding-test",
		WebDatabaseRef: "secret:DATABASE_URL", WorkerDatabaseRef: "secret:DATABASE_URL", MigrationDatabaseRef: "secret:MIGRATION_URL", ObserverDatabaseRef: "secret:DATABASE_URL"}
	body, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(ctx, http.MethodPut, "/v1/task-tool-source-intents/"+id+"/bindings", bytes.NewReader(body))
	r.SetPathValue("id", id)
	r = r.WithContext(middleware.WithPrincipal(r.Context(), f.account, nil, nil))
	w = httptest.NewRecorder()
	f.executor.HostedTaskBindings()(w, r, f.account)
	var binding api.HostedTaskBindingVerification
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &binding) != nil {
		t.Fatalf("binding selection: %d %s", w.Code, w.Body)
	}
	journal := f.store.(state.HostedTaskBindingStore)
	change := state.HostedTaskBindingChange{Owner: owner, BindingSHA256: binding.BindingSHA256, Claim: uuid.NewString()}
	if _, err := journal.ChangeHostedTaskBindings(ctx, f.account.ID, id, "claim", change); err != nil {
		t.Fatal(err)
	}
	// SQL role probing has its own native acceptance test. This fixture
	// records the completed observation to exercise the rehearsal barrier.
	change.Checks = tasktoolbinding.Results("passed")
	out, err := journal.ChangeHostedTaskBindings(ctx, f.account.ID, id, "record", change)
	if err != nil || out.Public.Status != "passed" {
		t.Fatalf("binding record: %+v, %v", out.Public, err)
	}
	return id, api.HostedTaskRehearsalPrepare{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, BindingSHA256: binding.BindingSHA256}
}
