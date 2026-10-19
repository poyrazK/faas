package main

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBudgetAdmissionCacheReusesDecisionsNotFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newBudgetAdmissionCache(func() time.Time { return now })
	key := budgetAdmissionKey{app: "app", scope: "production"}
	reads := 0
	result := error(nil)
	read := func() error { reads++; return result }

	if err := c.check(key, read); err != nil || reads != 1 {
		t.Fatalf("first admission: %v reads=%d", err, reads)
	}
	if err := c.check(key, read); err != nil || reads != 1 {
		t.Fatalf("warm request re-read the store: %v reads=%d", err, reads)
	}
	now = now.Add(api.FinancialBudgetAdmissionCacheTTL)
	result = state.FinancialBudgetHeldProblem()
	if err := c.check(key, read); !errors.Is(err, state.ErrFinancialBudgetHeld) || reads != 2 {
		t.Fatalf("expired decision was reused: %v reads=%d", err, reads)
	}
	if err := c.check(key, read); !errors.Is(err, state.ErrFinancialBudgetHeld) || reads != 2 {
		t.Fatalf("held decision was not reused: %v reads=%d", err, reads)
	}

	other := budgetAdmissionKey{app: "app", scope: "preview"}
	result = errors.New("database unavailable")
	for range 2 {
		if err := c.check(other, read); err == nil {
			t.Fatal("unavailable store admitted traffic")
		}
	}
	if reads != 4 {
		t.Fatalf("an unavailable store was cached: reads=%d", reads)
	}
}
