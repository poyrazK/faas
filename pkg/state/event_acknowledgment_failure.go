package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func acknowledgmentFailureReason(action string, payload json.RawMessage) (string, error) {
	if action != "retry" && action != "dead-letter" {
		return "", nil
	}
	var reason string
	if len(payload) > api.EventAcknowledgmentFailureRequestMaxBytes || json.Unmarshal(payload, &reason) != nil || len(reason) == 0 || len(reason) > api.EventAcknowledgmentFailureReasonMaxBytes {
		return "", ErrInvalidArgument
	}
	for _, c := range []byte(reason) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' && c != '-' {
			return "", ErrInvalidArgument
		}
	}
	return reason, nil
}
func validateAcknowledgmentFailure(action string, payload json.RawMessage) error {
	_, err := acknowledgmentFailureReason(action, payload)
	return err
}
func acknowledgmentFailureDigest(action, reason string) []byte {
	digest := sha256.Sum256([]byte(action + "\n" + reason))
	return digest[:]
}
func replayAcknowledgmentFailure(inv Invocation, ack eventAcknowledgment, action string, payload json.RawMessage) (api.EventAcknowledgmentStatus, error) {
	var saved api.EventAcknowledgmentStatus
	if json.Unmarshal(ack.rejectionStatus, &saved) != nil || string(inv.State) != saved.State {
		return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
	}
	if action == "accept" {
		return saved, nil
	}
	reason, err := acknowledgmentFailureReason(action, payload)
	if err != nil || (action != "retry" && action != "dead-letter") || !bytes.Equal(ack.completionDigest, acknowledgmentFailureDigest(action, reason)) {
		return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
	}
	return saved, nil
}
func acknowledgmentFailureStatus(inv Invocation, ack eventAcknowledgment, action, reason string, now time.Time) api.EventAcknowledgmentStatus {
	out := acknowledgmentStatus(inv, ack, now)
	out.Disposition = action
	out.FailureReason = reason
	if inv.State == InvocationPending {
		out.NextAttemptAt = cloneEventReceiptTime(&inv.DueAt)
	}
	return out
}
func (s *PgStore) rejectEventAcknowledgmentTx(ctx context.Context, tx pgx.Tx, inv Invocation, ack eventAcknowledgment, action string, payload json.RawMessage, now time.Time) (api.EventAcknowledgmentStatus, error) {
	reason, err := acknowledgmentFailureReason(action, payload)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	q := sqlc.New()
	// Invocation -> account quota -> diagnostic rows, matching completion and expiry.
	if err := q.EventAckMetricQuotaLock(ctx, tx, mustPgUUID(inv.AccountID)); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	row, err := q.EventAckReject(ctx, tx, sqlc.EventAckRejectParams{InvocationID: mustPgUUID(inv.ID), Permanent: action == "dead-letter", NowAt: pgtypeFromTime(now), FailureReason: "event acknowledgment " + action + ": " + reason})
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if inv.QuotaReserved {
		if err := decrementAccountAsyncInflightTx(ctx, tx, inv.AccountID); err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
	}
	inv.State = InvocationState(row.State)
	inv.DueAt = timeFromPgtype(row.DueAt)
	inv.LeaseExpiresAt = nil
	out := acknowledgmentFailureStatus(inv, ack, action, reason, now)
	saved, err := json.Marshal(out)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := q.EventAckRejectReceipt(ctx, tx, sqlc.EventAckRejectReceiptParams{InvocationID: mustPgUUID(inv.ID), RequestDigest: acknowledgmentFailureDigest(action, reason), RejectionStatus: saved}); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := recordAcknowledgmentOutcome(ctx, tx, inv.ID, acknowledgmentFailureKind(action)); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	return out, nil
}
func acknowledgmentFailureKind(action string) string {
	if action == "retry" {
		return "retry_requested"
	}
	return "dead_letter_requested"
}
func (m *MemStore) rejectEventAcknowledgmentLocked(inv Invocation, ack eventAcknowledgment, action string, payload json.RawMessage, now time.Time) (api.EventAcknowledgmentStatus, error) {
	reason, err := acknowledgmentFailureReason(action, payload)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	reserved := inv.QuotaReserved
	inv.QuotaReserved = false
	inv.InstanceID = ""
	inv.LeaseExpiresAt = nil
	inv.LastError = "event acknowledgment " + action + ": " + reason
	inv.State = InvocationPending
	inv.Outcome = nil
	inv.CompletedAt = nil
	inv.DueAt = now.Add(ack.retryDelay)
	if action == "dead-letter" || inv.Attempts >= ack.maxAttempts {
		inv.State = InvocationDeadLetter
		outcome := OutcomeDeadLetter
		inv.Outcome = &outcome
		inv.CompletedAt = &now
	}
	out := acknowledgmentFailureStatus(inv, ack, action, reason, now)
	saved, err := json.Marshal(out)
	if err != nil {
		return api.EventAcknowledgmentStatus{}, err
	}
	ack.phase = "rejected"
	ack.completionDigest = acknowledgmentFailureDigest(action, reason)
	ack.rejectionStatus = saved
	m.eventAcknowledgments[inv.ID] = ack
	m.setInvocationLocked(inv.ID, inv)
	if reserved {
		m.decrementAccountAsyncInflightLocked(inv.AccountID)
	}
	m.recordAcknowledgmentOutcomeLocked(inv, ack, acknowledgmentFailureKind(action), now)
	return out, nil
}
