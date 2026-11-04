package edgerules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type rollbackReadStore struct {
	MemStore *state.MemStore
	read     chan struct{}
	once     sync.Once
}

func (s *rollbackReadStore) ListEdgeRulesForApp(ctx context.Context, app string) ([]state.EdgeRule, error) {
	rows, err := s.MemStore.ListEdgeRulesForApp(ctx, app)
	s.once.Do(func() { close(s.read) })
	return rows, err
}

type rollbackLockHost struct {
	attackModeHost
	store state.Store
	notif *payloadNotifier
}

func (h rollbackLockHost) Store() state.Store { return h.store }
func (h rollbackLockHost) Notifier() Notifier { return h.notif }

func TestRollbackReadsAllChangedHostsAfterTheMutationLock(t *testing.T) {
	ctx, mem := context.Background(), state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "rollback-lock@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollback-lock", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	add := func(host string) {
		t.Helper()
		_, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: host, MatchPath: "/", Enabled: true, Kind: state.EdgeRuleKindThrottle})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("first.example.com")
	store := &rollbackReadStore{MemStore: mem, read: make(chan struct{})}
	notifications := &payloadNotifier{}
	svc := &Service{Host: rollbackLockHost{attackModeHost: attackModeHost{store: mem, app: app, audits: &[]string{}}, store: statetest.NewMem(store), notif: notifications}}
	// Another accepted editor already owns the mutation lock. It adds a
	// hostname before the waiting rollback is allowed to replace the set.
	unlock := edgeRuleAccountLocks.lock([]string{"account:" + account.ID, "app:" + app.ID})
	var release sync.Once
	defer release.Do(unlock)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"version":1}`))
	req.SetPathValue("slug", app.Slug)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); svc.RollbackEdgeRules(rec, req, account) }()
	select {
	case <-store.read:
	case <-time.After(100 * time.Millisecond):
	}
	add("concurrent.example.com")
	release.Do(unlock)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rollback did not release its mutation lock")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback = %d %s", rec.Code, rec.Body)
	}
	for _, payload := range notifications.payloads {
		if !slices.Contains(payload.MatchHosts, "concurrent.example.com") {
			t.Fatalf("fleet phase %s omitted a replaced host: %+v", payload.Phase, payload)
		}
	}
	if len(notifications.payloads) != 2 {
		t.Fatalf("fleet phases = %+v", notifications.payloads)
	}
}
