package state_test

// ADR-20261122220000: verification contract and SQL-check handoff storage
// against PostgreSQL.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreVerificationContracts(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	var contracts state.VerificationContractStore = store
	var queries state.VerificationQueryStore = store
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	acct, err := store.CreateAccount(ctx, fmt.Sprintf("verify+%s@example.com", name), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, fmt.Sprintf("verify-other+%s@example.com", name), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "verify-" + name[:min(len(name), 30)], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contracts.AppVerificationContract(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty get: %v", err)
	}
	sum := strings.Repeat("a", 64)
	c := state.VerificationContract{AppID: app.ID, AccountID: acct.ID, Contract: []byte(`{"version":1,"checks":[{"name":"a","http":{"path":"/"}}]}`), SHA256: sum, CheckCount: 1}
	if _, err := contracts.PutAppVerificationContract(ctx, state.VerificationContract{AppID: app.ID, AccountID: other.ID, Contract: c.Contract, SHA256: sum, CheckCount: 1}, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account put: %v", err)
	}
	zero := int64(0)
	first, err := contracts.PutAppVerificationContract(ctx, c, &zero)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first put: %+v %v", first, err)
	}
	if _, err := contracts.PutAppVerificationContract(ctx, c, &zero); !errors.Is(err, state.ErrVerificationContractRevision) {
		t.Fatalf("stale put: %v", err)
	}
	second, err := contracts.PutAppVerificationContract(ctx, c, nil)
	if err != nil || second.Revision != 2 || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("second put: %+v %v", second, err)
	}
	got, err := contracts.AppVerificationContract(ctx, app.ID)
	if err != nil || got.SHA256 != sum || got.CheckCount != 1 || len(got.Contract) == 0 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err := contracts.DeleteAppVerificationContract(ctx, app.ID, other.ID, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account delete: %v", err)
	}
	stale := int64(1)
	if err := contracts.DeleteAppVerificationContract(ctx, app.ID, acct.ID, &stale); !errors.Is(err, state.ErrVerificationContractRevision) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := contracts.DeleteAppVerificationContract(ctx, app.ID, acct.ID, &second.Revision); err != nil {
		t.Fatal(err)
	}

	// SQL handoff: concurrent claimers never share a row.
	expires := time.Now().Add(5 * time.Minute)
	var ids []string
	for i := 0; i < 6; i++ {
		q, err := queries.CreateVerificationQuery(ctx, state.VerificationQuery{DeploymentID: "00000000-0000-0000-0000-000000000001", AppID: app.ID, AccountID: acct.ID, CheckName: "sql", Database: "main", Query: "select 1", ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, q.ID)
	}
	if _, err := queries.CreateVerificationQuery(ctx, state.VerificationQuery{DeploymentID: "00000000-0000-0000-0000-000000000001", AppID: app.ID, AccountID: acct.ID, CheckName: "sql", Database: "main", Query: "select 1", ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := queries.ClaimVerificationQueries(ctx, time.Now(), 4)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, q := range claimed {
				seen[q.ID]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("query %s claimed %d times (seen=%v)", id, seen[id], seen)
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("expired query was claimed: %v", seen)
	}
	value := "0"
	if err := queries.CompleteVerificationQuery(ctx, state.VerificationQuery{ID: ids[0], RowCount: 1, HasFirst: true, FirstValue: &value}); err != nil {
		t.Fatal(err)
	}
	if err := queries.CompleteVerificationQuery(ctx, state.VerificationQuery{ID: ids[0]}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second completion: %v", err)
	}
	done, err := queries.VerificationQueryByID(ctx, ids[0])
	if err != nil || done.Status != state.VerificationQueryDone || done.FirstValue == nil || *done.FirstValue != "0" || done.RowCount != 1 {
		t.Fatalf("done query: %+v %v", done, err)
	}
	// Claims nobody finished come back once they are stale; done rows never do.
	if fresh, err := queries.ClaimVerificationQueries(ctx, time.Now(), 10); err != nil || len(fresh) != 0 {
		t.Fatalf("live claims re-claimed: %d %v", len(fresh), err)
	}
	reclaimed, err := queries.ClaimVerificationQueries(ctx, time.Now().Add(api.VerificationQueryStaleClaim+time.Second), 10)
	if err != nil || len(reclaimed) != len(ids)-1 {
		t.Fatalf("stale reclaim = %d %v, want %d", len(reclaimed), err, len(ids)-1)
	}
	for _, q := range reclaimed {
		if q.ID == ids[0] {
			t.Fatal("a done query was reclaimed")
		}
	}
	if n, err := queries.DeleteExpiredVerificationQueries(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("expired sweep: n=%d err=%v", n, err)
	}
}
