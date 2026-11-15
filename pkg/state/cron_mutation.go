package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrCronReviewConflict = errors.New("reviewed cron state changed")
var ErrCronReviewExpired = errors.New("cron mutation review expired")

type CronMutationStore interface {
	ApplyCronMutation(context.Context, string, string, api.CronMutationRequest) (api.CronMutationReceipt, error)
	CronMutationReceipt(context.Context, string, string, string, string) (api.CronMutationReceipt, error)
}

type cronMutationRecord struct {
	RequestHash   string                  `json:"request_hash"`
	ProofHash     string                  `json:"proof_hash"`
	ExpectedState string                  `json:"expected_state"`
	Receipt       api.CronMutationReceipt `json:"receipt"`
}

// CronReviewToken covers every execution field, including identity, policy,
// suspension and environment. Scheduler fire stamps do not invalidate a review.
func CronReviewToken(c Cron) string {
	c.LastFiredAt = time.Time{}
	c.CreatedAt, c.ScheduleAnchorAt = c.CreatedAt.UTC(), c.ScheduleAnchorAt.UTC()
	if len(c.Command) == 0 {
		c.Command = nil
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newCronMutation(appID string, r api.CronMutationRequest, now time.Time) (cronMutationRecord, error) {
	if !r.Valid() || r.AppID != appID {
		return cronMutationRecord{}, ErrInvalidArgument
	}
	if !now.Before(r.ExpiresAt) || r.ExpiresAt.After(now.Add(api.CronMutationReviewMaxAge)) {
		return cronMutationRecord{}, ErrCronReviewExpired
	}
	data, _ := json.Marshal(r)
	hash := sha256.Sum256(data)
	return cronMutationRecord{RequestHash: hex.EncodeToString(hash[:]), ProofHash: envBatchProofHash(r.ReceiptProof), ExpectedState: r.ExpectedState,
		Receipt: api.CronMutationReceipt{SchemaVersion: 1, RequestID: r.RequestID, AppID: appID, CronID: r.CronID, Delete: r.Delete, Status: "accepted", Phase: "cancel_pending", AcceptedAt: now, UpdatedAt: now, ExpiresAt: r.ExpiresAt, RetainedUntil: now.Add(api.CronMutationReceiptRetention)}}, nil
}

func (r cronMutationRecord) matches(want cronMutationRecord, proof string) bool {
	return r.RequestHash == want.RequestHash && envBatchProofMatches(r.ProofHash, proof)
}

func validateCronMutationTarget(c Cron, request api.CronMutationRequest) error {
	// Management's resource lifecycle catalog currently delegates default scope.
	if c.DeploymentScope != "" || c.SuspendedReason == CronSuspendedAppDeleted || request.Path != nil && len(c.Command) > 0 {
		return ErrInvalidArgument
	}
	return nil
}

func (m *MemStore) CronMutationReceipt(ctx context.Context, accountID, appID, requestID, proof string) (api.CronMutationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return api.CronMutationReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	r, found := m.cronMutations[appID+"/"+requestID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted || !found || !time.Now().Before(r.Receipt.RetainedUntil) || !envBatchProofMatches(r.ProofHash, proof) {
		return api.CronMutationReceipt{}, ErrNotFound
	}
	return r.Receipt, nil
}

func (m *MemStore) ApplyCronMutation(ctx context.Context, accountID, appID string, request api.CronMutationRequest) (api.CronMutationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return api.CronMutationReceipt{}, err
	}
	now := time.Now().UTC()
	want, err := newCronMutation(appID, request, now)
	if err != nil {
		return api.CronMutationReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !time.Now().Before(request.ExpiresAt) {
		return api.CronMutationReceipt{}, ErrCronReviewExpired
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.CronMutationReceipt{}, ErrNotFound
	}
	if m.cronMutations == nil {
		m.cronMutations = map[string]cronMutationRecord{}
	}
	key := appID + "/" + request.RequestID
	r, exists := m.cronMutations[key]
	if exists {
		if !r.matches(want, request.ReceiptProof) {
			return api.CronMutationReceipt{}, ErrConflict
		}
		if r.Receipt.Status == "completed" || r.Receipt.Status == "conflict" {
			return r.Receipt, nil
		}
	} else {
		r = want
	}
	c, ok := m.crons[request.CronID]
	if !ok || c.AppID != appID || CronReviewToken(c) != request.ExpectedState {
		if !exists {
			return api.CronMutationReceipt{}, ErrCronReviewConflict
		}
		r.Receipt.Status, r.Receipt.UpdatedAt = "conflict", now
		m.cronMutations[key] = r
		return r.Receipt, nil
	}
	if err := validateCronMutationTarget(c, request); err != nil {
		return api.CronMutationReceipt{}, err
	}
	if !exists {
		count := 0
		for id, old := range m.cronMutations {
			if !now.Before(old.Receipt.RetainedUntil) {
				delete(m.cronMutations, id)
				continue
			}
			if old.Receipt.AppID != appID {
				continue
			}
			count++
		}
		if count >= api.CronMutationReceiptMax {
			return api.CronMutationReceipt{}, ErrQuotaExceeded
		}
	}
	if !request.Delete {
		updated, err := m.updateCronWithOptionsLocked(c.ID, request.Schedule, request.Path, request.Enabled, request.Timezone, nil, nil)
		if err != nil {
			return api.CronMutationReceipt{}, err
		}
		_ = updated
		r.Receipt.Status, r.Receipt.Phase = "completed", "complete"
	} else if exists {
		if err := m.advanceCronDeletionLocked(&r, now); err != nil {
			return api.CronMutationReceipt{}, err
		}
	}
	r.Receipt.UpdatedAt = now
	if !exists {
		for id, old := range m.cronMutations {
			if old.Receipt.AppID == appID && old.Receipt.CronID == c.ID && old.Receipt.Status != "completed" && old.Receipt.Status != "conflict" {
				old.Receipt.Status, old.Receipt.UpdatedAt = "conflict", now
				m.cronMutations[id] = old
			}
		}
	}
	m.cronMutations[key] = r
	return r.Receipt, nil
}

func (m *MemStore) advanceCronDeletionLocked(r *cronMutationRecord, now time.Time) error {
	c := m.crons[r.Receipt.CronID]
	n := 0
	switch r.Receipt.Phase {
	case "cancel_pending":
		for id, inv := range m.invocations {
			if inv.CronID == nil || *inv.CronID != c.ID || inv.State != InvocationPending {
				continue
			}
			if err := m.cancelPendingInvocationLocked(id, inv, now); err != nil {
				return err
			}
			n++
			if n == cronPendingCancelBatch {
				break
			}
		}
		r.Receipt.Cancelled += int64(n)
		if n < cronPendingCancelBatch {
			r.Receipt.Phase = "detach_invocations"
		}
	case "detach_invocations":
		for id, inv := range m.invocations {
			if inv.CronID == nil || *inv.CronID != c.ID || inv.State == InvocationPending {
				continue
			}
			inv.CronID = nil
			m.setInvocationLocked(id, inv)
			n++
			if n == cronDeleteBatch {
				break
			}
		}
		r.Receipt.DetachedInvocations += int64(n)
		if n < cronDeleteBatch {
			r.Receipt.Phase = "detach_tasks"
		}
	case "detach_tasks":
		for id, task := range m.appTasks {
			if task.CronID != c.ID {
				continue
			}
			task.CronID = ""
			m.appTasks[id] = task
			n++
			if n == cronDeleteBatch {
				break
			}
		}
		r.Receipt.DetachedTasks += int64(n)
		if n < cronDeleteBatch {
			r.Receipt.Phase = "delete"
		}
	case "delete":
		for _, inv := range m.invocations {
			if inv.CronID != nil && *inv.CronID == c.ID {
				r.Receipt.Phase = "cancel_pending"
				return nil
			}
		}
		for _, task := range m.appTasks {
			if task.CronID == c.ID {
				r.Receipt.Phase = "detach_tasks"
				return nil
			}
		}
		if len(c.Command) == 0 {
			m.invalidateDeploymentCronsLocked(c.AppID)
		}
		delete(m.crons, c.ID)
		delete(m.exclusiveTriggerBindings, "cron\x00"+c.ID)
		r.Receipt.Status, r.Receipt.Phase = "completed", "complete"
		return nil
	default:
		return ErrInvalidArgument
	}
	r.Receipt.Status = "partial"
	return nil
}
