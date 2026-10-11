// adr: 662
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecoveryReceiptLegacyExpiryAndCleanup(t *testing.T) {
	for _, kind := range []string{"legacy", "expired", "retained"} {
		t.Run(kind, func(t *testing.T) {
			m := NewMemStore()
			now := time.Now().UTC()
			limits := api.MustLimitsFor(api.PlanPro)
			m.accounts["account"] = Account{ID: "account", Plan: api.PlanPro}
			op := Operation{AccountID: "account", ValueMaxBytes: limits.MaxSourceBytesPerInvocation, OperationResponse: api.OperationResponse{ID: "operation", State: api.OperationSucceeded, Generation: 2, ExpiresAt: now.Add(time.Hour)}}
			req := api.OperationRecoveryRequest{RecoveryID: "decision", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified no provider effect"}
			fp, err := operationRecoveryFingerprint(req, limits)
			if err != nil {
				t.Fatal(err)
			}
			data := m.operationMemoryLocked()
			data.operations[op.ID] = op
			data.recoveries[op.ID+"/"+req.RecoveryID] = fp
			if kind != "legacy" {
				expiry := now.Add(time.Minute)
				if kind == "expired" {
					expiry = now.Add(-time.Second)
				}
				m.saveOperationRecoveryDecisionLocked(op, req, fp, now.Add(-time.Hour), expiry)
			}
			decision, err := m.RecoverOperationWithReceipt(t.Context(), "account", op.ID, req)
			switch kind {
			case "legacy":
				if !errors.Is(err, ErrOperationRecoveryReceiptUnavailable) {
					t.Fatal("fabricated legacy receipt", decision, err)
				}
			case "expired":
				if !errors.Is(err, ErrOperationExpired) {
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
			if data.operations[op.ID].Generation != 2 || len(data.events) != 0 || len(m.invocations) != 0 {
				t.Fatal("historical replay repeated work")
			}
			m.mu.Lock()
			m.forgetOperationLocked(op.ID)
			m.mu.Unlock()
			if len(data.recoveries) != 0 || len(data.recoveryDecisions) != 0 {
				t.Fatal("owner cleanup leaked receipts")
			}
		})
	}
}
