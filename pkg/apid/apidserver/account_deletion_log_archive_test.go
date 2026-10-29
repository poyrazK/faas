package apidserver_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
)

// archiveBucket is an in-memory runtime-log archive.
type archiveBucket struct {
	mu      sync.Mutex
	keys    map[string]bool
	failing bool
}

func (b *archiveBucket) ListObjectKeys(_ context.Context, prefix, marker string, _ int) ([]string, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failing {
		return nil, "", errors.New("archive bucket unavailable")
	}
	var keys []string
	for key := range b.keys {
		if strings.HasPrefix(key, prefix) && key > marker {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, "", nil
}

func (b *archiveBucket) DeleteObject(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.keys, key)
	return nil
}

func (b *archiveBucket) under(instance string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for key := range b.keys {
		if strings.HasPrefix(key, "faas-logs/"+instance+"/") {
			n++
		}
	}
	return n
}

// When the grace ends, apid deletes the account's archived runtime logs
// before the account row, keeps the row while the archive cannot be reached,
// and leaves other accounts' archives alone (DPA §11).
func TestGraceErasesArchivedLogsBeforeDeletingTheAccount(t *testing.T) {
	env := apidserver.SetupPGHandler(t, api.PlanPro)
	pool, id, srv, store := *env.Pool(), env.Acct().ID, *env.S(), *env.Store()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: id, Slug: "logs-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "keep-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{
		AccountID: other.ID, Slug: "keep-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute).Add(-48 * time.Hour)
	mine, theirs := uuid.NewString(), uuid.NewString()
	if err := store.AppendUsage(ctx, id, app.ID, mine, minute, 136, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, other.ID, otherApp.ID, theirs, minute, 136, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	bucket := &archiveBucket{keys: map[string]bool{
		"faas-logs/" + mine + "/2026/10/20.jsonl.gz":   true,
		"faas-logs/" + mine + "/2026/10/21.jsonl.gz":   true,
		"faas-logs/" + theirs + "/2026/10/21.jsonl.gz": true,
	}, failing: true}
	*srv.LogArchive() = bucket
	if _, err := pool.Exec(ctx, `UPDATE accounts SET status='deleted_pending', deletion_requested_at=now()-interval '31 days' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	accounts := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sweep := grace.New(grace.Params{Store: store, BeforeAccountDelete: srv.BeforeAccountDelete})

	if err := sweep.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if accounts() != 1 || bucket.under(mine) != 2 {
		t.Fatalf("with the archive unreachable: account rows %d, archived objects %d; want the account kept with both objects", accounts(), bucket.under(mine))
	}

	bucket.mu.Lock()
	bucket.failing = false
	bucket.mu.Unlock()
	if err := sweep.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := bucket.under(mine); n != 0 {
		t.Fatalf("%d archived objects of the deleted account remain", n)
	}
	if accounts() != 0 {
		t.Fatal("grace kept the account after its archived logs were erased")
	}
	if n := bucket.under(theirs); n != 1 {
		t.Fatalf("other account's archive has %d objects, want 1", n)
	}
}
