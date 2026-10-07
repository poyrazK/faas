// adr: 678
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestInvokeSerializesIndependentClaimsAndReplaysAfterRestart(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	const calls = 16
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.manager.Invoke(t.Context(), f.id, "process-one", request(fmt.Sprint(i)), increment)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	assertCount(t, t.Context(), f.manager, f.id, calls, calls)
	restarted := openManager(t, f.store, f.clock)
	result, err := restarted.Invoke(t.Context(), f.id, "process-two", request("0"), func(context.Context, View) (Transition, error) {
		t.Error("replay ran guest callback")
		return Transition{}, errors.New("unexpected handler")
	})
	if err != nil || !result.Replayed || result.Version == 0 {
		t.Fatalf("replay = %+v %v", result, err)
	}
	if len(f.manager.locks) != 0 || len(restarted.locks) != 0 {
		t.Fatal("invocation retained idle entity locks")
	}
}

func TestEntityScopeIncludesEnvironmentAndCustomer(t *testing.T) {
	f := newFixture(t)
	for _, scope := range []struct{ env, tenant string }{{"env-one", "tenant-one"}, {"env-two", "tenant-one"}, {"env-one", "tenant-two"}, {"env-one", ""}} {
		id := f.id
		id.EnvironmentID, id.TenantID = scope.env, scope.tenant
		if _, err := f.manager.Invoke(t.Context(), id, "process", request("same"), increment); err != nil {
			t.Fatal(err)
		}
		assertCount(t, t.Context(), f.manager, id, 1, 1)
	}
}

func TestCancelledCallbackCannotPublishOrStrandAuthority(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err := f.manager.Invoke(ctx, f.id, "process-one", request("cancelled"), func(ctx context.Context, view View) (Transition, error) {
		cancel()
		return increment(ctx, view)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled callback = %v", err)
	}
	if _, err := f.manager.Invoke(t.Context(), f.id, "process-two", request("cancelled"), increment); err != nil {
		t.Fatal(err)
	}
	assertCount(t, t.Context(), f.manager, f.id, 1, 1)
}

func TestGuestTransitionProtocolFailsClosed(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":{},"result":1,"token":"secret"}`, `{"data":{},"result":1} {}`, `{"data":{},"result":1,"alarm_at":"0001-01-01T00:00:00Z"}`} {
		if _, err := DecodeTransition([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid transition %s: %v", body, err)
		}
	}
	transition, err := DecodeTransition([]byte(`{"data":{"count":1},"result":null}`))
	if err != nil || !json.Valid(transition.Data) {
		t.Fatalf("valid transition = %+v %v", transition, err)
	}
}
