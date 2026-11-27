package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type executionAdmissionTestStore interface {
	state.ExecutionAdmissionStore
	ListExecutions(context.Context, string, int, int) ([]state.Execution, error)
	UpdateAccountPlan(context.Context, string, api.Plan) error
	ReserveIdempotent(context.Context, string, string, time.Duration) (state.IdempotencyReservation, error)
}

func admissionIdentity(accountID, key string) state.ExecutionAdmissionIdentity {
	return state.ExecutionAdmissionIdentity{
		AccountID: accountID, ScopeHash: sha256.Sum256([]byte("account")), KeyHash: sha256.Sum256([]byte(key)),
		Fingerprint: sha256.Sum256([]byte("request")), LegacyKey: "POST /v1/executions\n" + key,
	}
}

func exerciseExecutionAdmission(t *testing.T, store executionAdmissionTestStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpdateAccountPlan(ctx, accountID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	params := executionTestParams(t, accountID, time.Now().UTC().Truncate(time.Microsecond), 0, "sealed-payload")
	identity := admissionIdentity(accountID, "concurrent")
	var prepared atomic.Int64
	prepare := func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		prepared.Add(1)
		return params, nil
	}
	const callers = 12
	start := make(chan struct{})
	type result struct {
		row      state.Execution
		replayed bool
		err      error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			row, replayed, err := store.AdmitExecution(ctx, identity, prepare)
			results <- result{row, replayed, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var id string
	first := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent admission: %v", result.err)
		}
		if id == "" {
			id = result.row.ID
		}
		if id != result.row.ID {
			t.Fatalf("duplicate Run: %s != %s", id, result.row.ID)
		}
		if !result.replayed {
			first++
		}
		if !result.row.DeadlineAt.Equal(params.DeadlineAt) {
			t.Fatalf("retry changed deadline: %v", result.row.DeadlineAt)
		}
	}
	if first != 1 || prepared.Load() != 1 {
		t.Fatalf("first=%d prepared=%d", first, prepared.Load())
	}
	rows, err := store.ListExecutions(ctx, accountID, 100, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Runs=%d err=%v", len(rows), err)
	}
	// Quota is now full. Replay still bypasses preparation, while a new key
	// reaches the quota check and leaves no identity behind on failure.
	other := admissionIdentity(accountID, "new-key")
	if _, _, err := store.AdmitExecution(ctx, other, prepare); !errors.Is(err, state.ErrExecutionQuotaExceeded) {
		t.Fatalf("quota: %v", err)
	}
	changed := identity
	changed.Fingerprint[31] ^= 1
	if _, _, err := store.AdmitExecution(ctx, changed, prepare); !errors.Is(err, state.ErrExecutionAdmissionConflict) {
		t.Fatalf("changed body: %v", err)
	}
	changed = identity
	changed.Fingerprint[0] ^= 1
	if _, _, err := store.AdmitExecution(ctx, changed, prepare); !errors.Is(err, state.ErrExecutionAdmissionKeyChanged) {
		t.Fatalf("rotated key: %v", err)
	}
	if prepared.Load() != 2 {
		t.Fatalf("conflicts must not prepare: %d", prepared.Load())
	}
}

func TestMemExecutionAdmissionConcurrentReplay(t *testing.T) {
	store := state.NewMemStore()
	account := executionTestAccount(t, store, "keyed-concurrent")
	exerciseExecutionAdmission(t, store, account.ID)
}

func exerciseExecutionAdmissionFailures(t *testing.T, store executionAdmissionTestStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	identity := admissionIdentity(accountID, "recoverable")
	want := errors.New("preparation unavailable")
	if _, _, err := store.AdmitExecution(ctx, identity, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return state.CreateExecutionParams{}, want
	}); !errors.Is(err, want) {
		t.Fatalf("preparation failure: %v", err)
	}
	params := executionTestParams(t, accountID, time.Now().UTC(), 0, "sealed")
	params.ArtifactGrantRedemptions = []state.ExecutionArtifactGrantRedemption{{GrantID: "00000000-0000-0000-0000-000000000001", TokenHash: make([]byte, 32)}}
	prepare := func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return params, nil
	}
	if _, _, err := store.AdmitExecution(ctx, identity, prepare); !errors.Is(err, state.ErrExecutionArtifactGrantUnavailable) {
		t.Fatalf("grant failure: %v", err)
	}
	rows, err := store.ListExecutions(ctx, accountID, 100, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed admission leaked Run: %d, %v", len(rows), err)
	}
	params.ArtifactGrantRedemptions = nil
	row, replayed, err := store.AdmitExecution(ctx, identity, prepare)
	if err != nil || replayed || row.ID == "" {
		t.Fatalf("retry failed admission: %+v, %v, %v", row, replayed, err)
	}
	legacy := admissionIdentity(accountID, "legacy")
	if _, err := store.ReserveIdempotent(ctx, accountID, legacy.LegacyKey, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AdmitExecution(ctx, legacy, prepare); !errors.Is(err, state.ErrExecutionAdmissionLegacy) {
		t.Fatalf("legacy fence: %v", err)
	}
	// Separate namespaces and accounts must not inherit another request's key.
	otherScope := identity
	otherScope.ScopeHash = sha256.Sum256([]byte("another-principal"))
	otherScope.LegacyKey = "POST /v1/executions\nruns-principal:other\nrecoverable"
	other, replayed, err := store.AdmitExecution(ctx, otherScope, prepare)
	if err != nil || replayed || other.ID == row.ID {
		t.Fatalf("principal boundary: %s, %v, %v", other.ID, replayed, err)
	}
}

func TestMemExecutionAdmissionFailureRollback(t *testing.T) {
	store := state.NewMemStore()
	account := executionTestAccount(t, store, "keyed-failures")
	exerciseExecutionAdmissionFailures(t, store, account.ID)
}

type executionAdmissionGrantTestStore interface {
	state.ExecutionAdmissionStore
	state.ExecutionStore
	state.ExecutionArtifactGrantStore
}

func prepareExecutionAdmissionGrant(t *testing.T, store executionAdmissionGrantTestStore, accountID string) (state.ExecutionArtifactGrant, [32]byte, time.Time) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)
	source, err := store.CreateExecution(ctx, executionTestParams(t, accountID, base, 0, "sealed-source"))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "grant-source", base.Add(time.Millisecond), time.Minute)
	if err != nil || claim.ID != source.ID {
		t.Fatalf("claim source: %v", err)
	}
	if _, err := store.MarkExecutionRunning(ctx, source.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	content := []byte("one artifact")
	digest := sha256.Sum256(content)
	if _, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: source.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: []byte("null"),
		Artifacts:  []api.ExecutionArtifact{{Name: "result.txt", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:])}},
		FinishedAt: base.Add(3 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("one-time-token"))
	grant, err := store.CreateExecutionArtifactGrant(ctx, state.CreateExecutionArtifactGrantParams{
		ID: uuid.NewString(), AccountID: accountID, SourceExecutionID: source.ID, ArtifactName: "result.txt",
		TokenHash: tokenHash[:], CreatedAt: base, ExpiresAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant, tokenHash, base
}

func exerciseExecutionAdmissionGrantReplay(t *testing.T, store executionAdmissionGrantTestStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	grant, tokenHash, base := prepareExecutionAdmissionGrant(t, store, accountID)
	identity := admissionIdentity(accountID, "grant-replay")
	prepared := 0
	prepare := func(ctx context.Context, reader state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		prepared++
		loaded, err := reader.ExecutionArtifactGrantByToken(ctx, accountID, tokenHash[:], base.Add(5*time.Millisecond))
		if err != nil {
			return state.CreateExecutionParams{}, err
		}
		if _, err := reader.ExecutionByID(ctx, accountID, loaded.SourceExecutionID); err != nil {
			return state.CreateExecutionParams{}, err
		}
		params := executionTestParams(t, accountID, base.Add(5*time.Millisecond), 0, "sealed-consumer")
		params.ArtifactGrantRedemptions = []state.ExecutionArtifactGrantRedemption{{GrantID: loaded.ID, TokenHash: loaded.TokenHash}}
		return params, nil
	}
	first, replayed, err := store.AdmitExecution(ctx, identity, prepare)
	if err != nil || replayed {
		t.Fatalf("grant admission: %v, %v", replayed, err)
	}
	if _, err := store.ExecutionArtifactGrantByToken(ctx, accountID, tokenHash[:], base.Add(6*time.Millisecond)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("grant not consumed: %v", err)
	}
	if _, err := store.RevokeExecutionArtifactGrant(ctx, accountID, grant.ID, nil, true, base.Add(6*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestExecutionCancellation(ctx, accountID, first.ID, base.Add(7*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	second, replayed, err := store.AdmitExecution(ctx, identity, prepare)
	if err != nil || !replayed || second.ID != first.ID || second.Status != api.ExecutionStatusCancelled || prepared != 1 {
		t.Fatalf("terminal/grant replay: %+v, %v, %v; prepared=%d", second, replayed, err, prepared)
	}
	// Bounded sweeping can remove an expired binding without deleting the
	// durable receipt. Reusing that expired key is then a new admission.
	if _, err := store.SweepExecutions(ctx, base.Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	newParams := executionTestParams(t, accountID, time.Now().UTC(), 0, "new-payload")
	third, replayed, err := store.AdmitExecution(ctx, identity, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		return newParams, nil
	})
	if err != nil || replayed || third.ID == first.ID {
		t.Fatalf("expired key reuse: %s, %v, %v", third.ID, replayed, err)
	}
}

func TestMemExecutionAdmissionGrantAndTerminalReplay(t *testing.T) {
	store := state.NewMemStore()
	account := executionTestAccount(t, store, "grant-replay")
	exerciseExecutionAdmissionGrantReplay(t, store, account.ID)
}
