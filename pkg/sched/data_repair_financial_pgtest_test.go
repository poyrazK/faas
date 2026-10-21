//go:build !no_pg

package sched

// adr: 566
// adr: 20261016 — a preview hold also fences repair sandbox admission.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialBudgetHoldRejectsDataRepairReservation(t *testing.T) {
	// Repair frames require canonical UUIDs; use the production store rather
	// than MemStore's historical unhyphenated account identifiers.
	s := state.NewPgStore(pgtest.OpenMigrated(t))
	account, app, _ := seedBudgetPreview(t, s)
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	operation := uuid.NewString()
	repair := state.DataRepairRehearsal{
		ID: uuid.NewString(), OperationID: operation, AccountID: account.ID,
		Scope:   core.Scope{AccountID: account.ID, AppID: app.ID, Environment: "production"},
		Binding: strings.Repeat("a", 64), PlanID: operation, PlanDigest: strings.Repeat("b", 64),
		ChecksSHA256: strings.Repeat("c", 64), PlanExpiresAt: now.Add(time.Minute),
	}
	runner := core.RunnerSpec{BaseKey: "repair/base", LayerKey: "repair/layer", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
	incarnation := uuid.NewString()
	if _, err := e.ReserveDataRepairRehearsal(t.Context(), repair, e.defaultLocalNodeID, runner, incarnation); err != nil {
		t.Fatalf("unheld repair fixture: %v", err)
	}
	e.ReleaseDataRepairRehearsal(repair.ID)
	crossPreviewBudget(t, s, account.ID, now, 0)
	if err := e.ReconcileFinancialBudgetStops(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	repair.ID = uuid.NewString()
	t.Cleanup(func() { e.ReleaseDataRepairRehearsal(repair.ID) })
	if _, err := e.ReserveDataRepairRehearsal(t.Context(), repair, e.defaultLocalNodeID, runner, incarnation); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("held preview reserved an unfunded repair sandbox: %v", err)
	}
	if e.ledger.ResidentRAM() != 0 {
		t.Fatal("budget refusal retained repair capacity")
	}
}
