package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func udpListenerFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "udp-listener-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "udp-" + uuid.NewString(), Status: AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, app
}

func TestMemStoreUDPListenerLifecycleAndRouteLookup(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	created, err := m.CreateUDPListener(ctx, UDPListener{
		AccountID: account.ID, AppID: app.ID, ListenerName: "Postgres",
		GuestPort: 5432, PublicPort: 40123, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Protocol != "udp" || created.ListenerName != "postgres" {
		t.Fatalf("normalized listener = %+v", created)
	}
	byName, err := m.UDPListenerByAppAndName(ctx, app.ID, "POSTGRES")
	if err != nil || byName.ID != created.ID {
		t.Fatalf("lookup by name = %+v, %v", byName, err)
	}
	byPort, err := m.UDPListenerByPublicPort(ctx, created.PublicPort)
	if err != nil || byPort.ID != created.ID {
		t.Fatalf("lookup by port = %+v, %v", byPort, err)
	}
	disabled, err := m.SetUDPListenerEnabled(ctx, created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, created.PublicPort); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled public lookup = %v, want ErrNotFound", err)
	}
	rows, err := m.ListUDPListenersForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	if err := m.DeleteUDPListener(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.UDPListenerByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}

func TestMemStoreUDPListenerRejectsCollisionsAndInvalidPorts(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	first, err := m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "one", GuestPort: 1001, PublicPort: 40124})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "two", GuestPort: 1002, PublicPort: first.PublicPort})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("public collision = %v, want ErrConflict", err)
	}
	_, err = m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "udp", GuestPort: 53, PublicPort: 40125, Protocol: "tcp"})
	if !errors.Is(err, ErrInvalidUDPListener) {
		t.Fatalf("protocol error = %v, want ErrInvalidUDPListener", err)
	}
}

func TestMemStoreUDPListenerSeparateNamespaceAndAppDeletion(t *testing.T) {
	m, ctx, account, app := udpListenerFixture(t)
	if _, err := m.CreateTCPListener(ctx, TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: 40126, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	listener, err := m.CreateUDPListener(ctx, UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: 40126})
	if err != nil {
		t.Fatalf("TCP port blocked independent UDP namespace: %v", err)
	}
	if listener.Enabled {
		t.Fatal("UDP endpoint enabled by default")
	}
	if _, err := m.SetUDPListenerEnabled(ctx, listener.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, listener.PublicPort); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateUDPListener(ctx, UDPListener{AccountID: "different-account", AppID: app.ID, ListenerName: "other", GuestPort: 5353, PublicPort: 40127}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account create: %v", err)
	}
	if err := m.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UDPListenerByPublicPort(ctx, listener.PublicPort); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted app remained public: %v", err)
	}
	enabled, err := m.ListEnabledUDPListeners(ctx)
	if err != nil || len(enabled) != 0 {
		t.Fatalf("deleted app remained in bind set: %+v %v", enabled, err)
	}
}

func TestMemStoreUDPListenerReservationConcurrency(t *testing.T) {
	store, ctx, account, app := udpListenerFixture(t)
	accountID, appID := account.ID, app.ID

	for i := 0; i < api.UDPListenerReservationsPerAppMax-1; i++ {
		if _, err := store.CreateUDPListener(ctx, UDPListener{AccountID: accountID, AppID: appID, ListenerName: fmt.Sprintf("slot-%d", i), GuestPort: 1000 + i, PublicPort: 40000 + i}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		listener UDPListener
		err      error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			listener, err := store.CreateUDPListener(ctx, UDPListener{AccountID: accountID, AppID: appID, ListenerName: fmt.Sprintf("racing-%d", i), GuestPort: 2000 + i, PublicPort: 41000 + i})
			results <- result{listener, err}
		}(i)
	}
	wg.Wait()
	close(results)
	var winner UDPListener
	successes := 0
	for r := range results {
		if r.err == nil {
			successes++
			winner = r.listener
			continue
		}
		var quota *UDPListenerLimitError
		if !errors.Is(r.err, ErrUDPListenerLimit) || !errors.As(r.err, &quota) || quota.Limit != api.UDPListenerReservationsPerAppMax || quota.Observed != api.UDPListenerReservationsPerAppMax+1 {
			t.Fatalf("unexpected creation result: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("last slot admitted %d creators", successes)
	}
	rows, err := store.ListUDPListenersForApp(ctx, appID)
	if err != nil || len(rows) != api.UDPListenerReservationsPerAppMax {
		t.Fatalf("reservation count=%d error=%v", len(rows), err)
	}
	if _, err := store.CreateUDPListener(ctx, UDPListener{AccountID: accountID, AppID: appID, ListenerName: "slot-0", GuestPort: 3000, PublicPort: 42000}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name at quota=%v", err)
	}
	if _, err := store.SetUDPListenerEnabled(ctx, winner.ID, false); err != nil {
		t.Fatal(err)
	}
	candidate := UDPListener{AccountID: accountID, AppID: appID, ListenerName: "replacement", GuestPort: 3001, PublicPort: 42001}
	if _, err := store.CreateUDPListener(ctx, candidate); !errors.Is(err, ErrUDPListenerLimit) {
		t.Fatalf("disable freed a reservation: %v", err)
	}
	if err := store.DeleteUDPListener(ctx, winner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUDPListener(ctx, candidate); err != nil {
		t.Fatalf("delete did not free a reservation: %v", err)
	}
	other, err := store.CreateApp(ctx, App{AccountID: accountID, Slug: "udp-quota-other", Status: AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	candidate.AppID = other.ID
	candidate.PublicPort = 42002
	if _, err := store.CreateUDPListener(ctx, candidate); err != nil {
		t.Fatalf("another app inherited quota: %v", err)
	}
}

func TestMemStoreUDPListenerCanceledMutationsPreserveIntent(t *testing.T) {
	store, ctx, account, app := udpListenerFixture(t)
	listener, err := store.CreateUDPListener(ctx, UDPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	candidate := listener
	candidate.ID = ""
	candidate.ListenerName = "other"
	candidate.PublicPort++
	if _, err := store.CreateUDPListener(canceled, candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.SetUDPListenerEnabled(canceled, listener.ID, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("update: %v", err)
	}
	if err := store.DeleteUDPListener(canceled, listener.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete: %v", err)
	}
	rows, err := store.ListUDPListenersForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0] != listener {
		t.Fatalf("intent changed: rows=%+v err=%v", rows, err)
	}
}

// Signal after capturing the first Err result, before the mutation acquires mu.
type udpMutationContext struct {
	context.Context
	calls   atomic.Int32
	checked chan struct{}
}

func (c *udpMutationContext) Err() error {
	err := c.Context.Err()
	if c.calls.Add(1) == 1 {
		close(c.checked)
	}
	return err
}
func TestMemStoreUDPListenerCancellationWhileWaitingForLock(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			store, base, account, app := udpListenerFixture(t)
			listener, err := store.CreateUDPListener(base, UDPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40100, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			parent, cancel := context.WithCancel(base)
			defer cancel()
			ctx := &udpMutationContext{Context: parent, checked: make(chan struct{})}
			store.mu.Lock()
			done := make(chan error, 1)
			go func() {
				switch action {
				case "create":
					candidate := listener
					candidate.ID = ""
					candidate.ListenerName = "other"
					candidate.PublicPort++
					_, err := store.CreateUDPListener(ctx, candidate)
					done <- err
				case "update":
					_, err := store.SetUDPListenerEnabled(ctx, listener.ID, false)
					done <- err
				case "delete":
					done <- store.DeleteUDPListener(ctx, listener.ID)
				}
			}()
			select {
			case <-ctx.checked:
			case <-time.After(time.Second):
				store.mu.Unlock()
				t.Fatal("mutation never checked context")
			}
			cancel()
			store.mu.Unlock()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("mutation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("mutation hung")
			}
			rows, err := store.ListUDPListenersForApp(base, app.ID)
			if err != nil || len(rows) != 1 || rows[0] != listener {
				t.Fatalf("intent changed: rows=%+v err=%v", rows, err)
			}
		})
	}
}
