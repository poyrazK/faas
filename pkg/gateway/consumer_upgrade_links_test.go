package gateway

// adr: 20261124094745 — a plan-limit refusal carries the consumer's
// upgrade link, cached; a failed lookup leaves it out.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeUpgradeLinker struct {
	calls    int
	consumer string
	url      string
	err      error
}

func (f *fakeUpgradeLinker) UpgradeLink(_ context.Context, _, _, consumerID string) (string, time.Time, error) {
	f.calls++
	f.consumer = consumerID
	return f.url, time.Now().Add(24 * time.Hour), f.err
}

func TestPlanLimitRefusalCarriesUpgradeURL(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	refuse := func(h *Handler) api.Problem {
		t.Helper()
		w := httptest.NewRecorder()
		if h.enforceConsumerPlan(w, planRequest("POST /generate"), &statusRecorder{}, app, false) || w.Code != http.StatusTooManyRequests {
			t.Fatalf("not refused: %d %s", w.Code, w.Body)
		}
		var p api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	handler := func(l ConsumerUpgradeLinker) *Handler {
		store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{MaxUnitsPerMonth: 10},
			decision: ConsumerPlanDecision{Scope: "month", Limit: 10, Observed: 10, RetryAfterSeconds: 60}}
		h := (&Handler{apiMonetization: monetizationOn}).WithConsumerPlanStore(store)
		if l != nil {
			h.WithUpgradeLinks(l)
		}
		return h
	}

	linker := &fakeUpgradeLinker{url: "https://api.example/v1/consumer-signup/app/upgrade?token=t"}
	h := handler(linker)
	for range 2 {
		if p := refuse(h); p.UpgradeURL != linker.url || p.Code != api.CodeConsumerPlanLimitExceeded {
			t.Fatalf("problem = %+v", p)
		}
	}
	if linker.calls != 1 || linker.consumer != "consumer-1" {
		t.Fatalf("linker = %+v; want one cached call", linker)
	}
	down := &fakeUpgradeLinker{err: errors.New("apid down")}
	h = handler(down)
	for range 3 {
		if p := refuse(h); p.UpgradeURL != "" {
			t.Fatalf("failed lookup still set upgrade_url %q", p.UpgradeURL)
		}
	}
	// While apid is down, refusals skip the lookup.
	if down.calls != 1 {
		t.Fatalf("apid asked %d times during an outage; want 1", down.calls)
	}
	if p := refuse(handler(&fakeUpgradeLinker{})); p.UpgradeURL != "" {
		t.Fatalf("app without upgrades set upgrade_url %q", p.UpgradeURL)
	}
	if p := refuse(handler(nil)); p.UpgradeURL != "" {
		t.Fatalf("unwired gateway set upgrade_url %q", p.UpgradeURL)
	}
}
