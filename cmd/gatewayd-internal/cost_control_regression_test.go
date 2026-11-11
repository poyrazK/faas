package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// A slow admission started before a hold must not overwrite the newer held
// decision once it finishes. Both paths occur on uncached/expired keys.
func TestCostControlOlderAdmissionCannotOverwriteNewerHold(t *testing.T) {
	c := newBudgetAdmissionCache(time.Now, time.Minute)
	key := budgetAdmissionKey{app: "audit-app"}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_ = c.check(context.Background(), key, func(context.Context) error { close(started); <-release; return nil })
	}()
	<-started
	held := state.FinancialBudgetHeldProblem()
	if err := c.check(context.Background(), key, func(context.Context) error { return held }); err == nil {
		t.Fatal("newer hold was not observed")
	}
	close(release)
	<-finished
	if err := c.check(context.Background(), key, func(context.Context) error { return held }); err == nil {
		t.Error("older concurrent admission erased the newer held decision and allowed traffic")
	}
}
