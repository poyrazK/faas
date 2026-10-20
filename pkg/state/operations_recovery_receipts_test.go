// adr: 662
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRecoveryReceiptLegacyExpiryAndCleanup(t *testing.T) {
	for _, kind := range []string{"legacy", "expired", "retained"} {
		t.Run(kind, func(t *testing.T) {
			m := state.NewMemStore()
			now := time.Now().UTC()
			limits := api.MustLimitsFor(api.PlanPro)
			(*m.Accounts())["account"] = state.Account{ID: "account", Plan: api.PlanPro}
			op := state.Operation{AccountID: "account", ValueMaxBytes: limits.MaxSourceBytesPerInvocation, OperationResponse: api.OperationResponse{ID: "operation", State: api.OperationSucceeded, Generation: 2, ExpiresAt: now.Add(time.Hour)}}
			req := api.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified no provider effect"}
			fp, err := state.OperationRecoveryFingerprint(req, limits)
			if err != nil {
				t.Fatal(err)
			}
			data := m.OperationMemoryLocked()
			(*data.Operations())[op.ID] = op
			(*data.Recoveries())[op.ID+"/"+req.RecoveryID] = fp
			if kind != "legacy" {
				expiry := now.Add(time.Minute)
				if kind == "expired" {
					expiry = now.Add(-time.Second)
				}
				m.SaveOperationRecoveryDecisionLocked(op, req, fp, now.Add(-time.Hour), expiry)
			}
			decision, err := m.RecoverOperationWithReceipt(t.Context(), "account", op.ID, req)
			switch kind {
			case "legacy":
				if !errors.Is(err, state.ErrOperationRecoveryReceiptUnavailable) {
					t.Fatal("fabricated legacy receipt", decision, err)
				}
			case "expired":
				if !errors.Is(err, state.ErrOperationExpired) {
					t.Fatal("later work extended receipt", decision, err)
				}
			case "retained":
				if err != nil || decision.RecoveryID != req.RecoveryID {
					t.Fatal("retained replay", decision, err)
				}
			}
			if _, err = m.RecoverOperation(t.Context(), "account", "", op.ID, req); err != nil {
				t.Fatal("legacy deduplication changed", err)
			}
			if (*data.Operations())[op.ID].Generation != 2 || len((*data.Events())) != 0 || len((*m.Invocations())) != 0 {
				t.Fatal("historical replay repeated work")
			}
			(*m.Mu()).Lock()
			m.ForgetOperationLocked(op.ID)
			(*m.Mu()).Unlock()
			if len((*data.Recoveries())) != 0 || len((*data.RecoveryDecisions())) != 0 {
				t.Fatal("owner cleanup leaked receipts")
			}
		})
	}
}
