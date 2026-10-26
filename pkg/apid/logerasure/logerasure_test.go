package logerasure_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/logerasure"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeObjects is an in-memory bucket. pageSize forces paged listings.
type fakeObjects struct {
	keys     map[string]bool
	pageSize int
	listErr  error
	lists    int
}

func (f *fakeObjects) ListObjectKeys(_ context.Context, prefix, marker string, maxKeys int) ([]string, string, error) {
	f.lists++
	if f.listErr != nil {
		return nil, "", f.listErr
	}
	var matched []string
	for key := range f.keys {
		if strings.HasPrefix(key, prefix) && key > marker {
			matched = append(matched, key)
		}
	}
	sort.Strings(matched)
	limit := maxKeys
	if f.pageSize > 0 && f.pageSize < limit {
		limit = f.pageSize
	}
	if len(matched) <= limit {
		return matched, "", nil
	}
	return matched[:limit], matched[limit-1], nil
}

func (f *fakeObjects) DeleteObject(_ context.Context, key string) error {
	delete(f.keys, key)
	return nil
}

type erasureFixture struct {
	store   *state.MemStore
	account state.Account
	other   []string
	mine    []string
}

// newFixture gives the account three instances with archived days, plus one
// instance of another account.
func newFixture(t *testing.T, objects *fakeObjects) erasureFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "erase-"+uuid.NewString()[:8]+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	other, err := store.CreateAccount(ctx, "keep-"+uuid.NewString()[:8]+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	minute := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	fx := erasureFixture{store: store}
	addInstance := func(accountID string, days int) string {
		instance := uuid.NewString()
		if err := store.AppendUsage(ctx, accountID, uuid.NewString(), instance, minute, 520, 1, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("AppendUsage: %v", err)
		}
		for d := 0; d < days; d++ {
			objects.keys["faas-logs/"+instance+"/2026/10/0"+string(rune('1'+d))+".jsonl.gz"] = true
		}
		return instance
	}
	for _, days := range []int{1, 3, 2} {
		fx.mine = append(fx.mine, addInstance(account.ID, days))
	}
	fx.other = append(fx.other, addInstance(other.ID, 2))
	requested := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	account.DeletionRequestedAt = &requested
	fx.account = account
	return fx
}

func remaining(objects *fakeObjects, instances []string) int {
	n := 0
	for key := range objects.keys {
		for _, instance := range instances {
			if strings.HasPrefix(key, "faas-logs/"+instance+"/") {
				n++
			}
		}
	}
	return n
}

func TestAccountErasureDeletesOnlyTheAccountsArchivedLogs(t *testing.T) {
	ctx := context.Background()
	objects := &fakeObjects{keys: map[string]bool{}, pageSize: 2}
	fx := newFixture(t, objects)
	var audited []string
	erasure := logerasure.AccountErasure{
		Store: fx.store, Objects: objects, Batch: 1,
		Audit: func(_ context.Context, kind string, _ *string, _ map[string]any) { audited = append(audited, kind) },
	}

	// One instance per tick: the account row is held until the last one.
	for tick := 1; tick < len(fx.mine); tick++ {
		if err := erasure.BeforeAccountDelete(ctx, fx.account); !errors.Is(err, logerasure.ErrPending) {
			t.Fatalf("tick %d: err = %v, want ErrPending", tick, err)
		}
	}
	if err := erasure.BeforeAccountDelete(ctx, fx.account); err != nil {
		t.Fatalf("last tick: %v", err)
	}
	if n := remaining(objects, fx.mine); n != 0 {
		t.Fatalf("%d archived objects of the deleted account remain", n)
	}
	if n := remaining(objects, fx.other); n != 2 {
		t.Fatalf("other account's archive has %d objects, want 2", n)
	}
	if len(audited) != 1 || audited[0] != "log_archive.erased" {
		t.Fatalf("audit = %v, want one log_archive.erased", audited)
	}

	// A later tick finds nothing left and does not list again.
	lists := objects.lists
	if err := erasure.BeforeAccountDelete(ctx, fx.account); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if objects.lists != lists {
		t.Fatalf("repeat listed the bucket %d more times", objects.lists-lists)
	}
}

func TestAccountErasureHoldsTheAccountWhenTheBucketFails(t *testing.T) {
	ctx := context.Background()
	objects := &fakeObjects{keys: map[string]bool{}, listErr: errors.New("503 slow down")}
	fx := newFixture(t, objects)
	erasure := logerasure.AccountErasure{Store: fx.store, Objects: objects}
	if err := erasure.BeforeAccountDelete(ctx, fx.account); !errors.Is(err, logerasure.ErrPending) {
		t.Fatalf("err = %v, want ErrPending", err)
	}
	pending, err := fx.store.PendingLogArchiveErasure(ctx, fx.account.ID, 10)
	if err != nil || len(pending) != len(fx.mine) {
		t.Fatalf("pending = %v, %v; want all %d instances still pending", pending, err, len(fx.mine))
	}

	objects.listErr = nil
	if err := erasure.BeforeAccountDelete(ctx, fx.account); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if n := remaining(objects, fx.mine); n != 0 {
		t.Fatalf("%d archived objects remain after recovery", n)
	}
}

func TestAccountErasureWithoutAnArchiveIsANoop(t *testing.T) {
	objects := &fakeObjects{keys: map[string]bool{}}
	fx := newFixture(t, objects)
	erasure := logerasure.AccountErasure{Store: fx.store}
	if err := erasure.BeforeAccountDelete(context.Background(), fx.account); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
