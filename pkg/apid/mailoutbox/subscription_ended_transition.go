package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// SubscriptionEndedResult describes the transition committed by this call.
// Replays do not repeat plan notifications or report a new transition.
type SubscriptionEndedResult struct {
	Applied  bool
	FromPlan api.Plan
	Status   string
	EndedAt  time.Time
}

// EndSubscriptionWithConfirmation accepts only a verified provider delivery.
// Binding clear, Free downgrade, compatibility mirrors, streaming downgrade,
// edge invalidation, permanent receipts and any confirmation commit together.
func (s *Service) EndSubscriptionWithConfirmation(ctx context.Context, id, provider, eventID, customerID, subscriptionID string) (SubscriptionEndedResult, error) {
	var result SubscriptionEndedResult
	if !s.subscriptionEndedEnabled() {
		return result, internalmail.ErrDisabled
	}
	if !validID(id) || !validSubscriptionEnd(provider, eventID, customerID, subscriptionID) {
		return result, internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return result, err
	}
	a, err := s.queries.LockSubscriptionEndedAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrAccountUnavailable
	}
	if err != nil {
		return result, err
	}
	bound, err := s.queries.LockSubscriptionEndedIdentity(ctx, tx, q.LockSubscriptionEndedIdentityParams{AccountID: a.ID, Provider: provider, CustomerID: customerID})
	if err != nil {
		return result, ErrAccountUnavailable
	}
	customerHash, subHash := sha256.Sum256([]byte(customerID)), sha256.Sum256([]byte(subscriptionID))
	inserted, err := s.queries.RecordSubscriptionEndReceipt(ctx, tx, q.RecordSubscriptionEndReceiptParams{Provider: provider, EventID: eventID, AccountID: a.ID, CustomerHash: customerHash[:], SubscriptionHash: subHash[:]})
	if err != nil {
		return result, err
	}
	if inserted == 0 {
		saved, err := s.queries.SubscriptionEndReceipt(ctx, tx, q.SubscriptionEndReceiptParams{Provider: provider, EventID: eventID})
		if err != nil {
			return result, err
		}
		if saved.AccountID != a.ID || !bytes.Equal(saved.CustomerHash, customerHash[:]) || !bytes.Equal(saved.SubscriptionHash, subHash[:]) {
			return result, internalmail.ErrConflict
		}
	} else {
		ended, err := s.queries.SubscriptionAlreadyEnded(ctx, tx, q.SubscriptionAlreadyEndedParams{AccountID: a.ID, Provider: provider, SubscriptionHash: subHash[:]})
		if err != nil {
			return result, err
		}
		if !ended && bound == subscriptionID {
			result, err = s.commitSubscriptionEnd(ctx, tx, a, provider, customerID, subscriptionID, subHash[:])
			if err != nil {
				return SubscriptionEndedResult{}, err
			}
		} else if !ended && bound == "" && a.Plan != "free" {
			// A missing current paid binding is recoverable, never permission
			// to downgrade an unqualified or temporarily unbound subscription.
			return result, ErrAccountUnavailable
		}
	}
	if !s.subscriptionEndedEnabled() {
		return SubscriptionEndedResult{}, internalmail.ErrDisabled
	}
	return result, tx.Commit(ctx)
}

func validSubscriptionEnd(provider, eventID, customerID, subscriptionID string) bool {
	if !validBillingDelivery(provider, eventID, customerID) {
		return false
	}
	for _, value := range []string{customerID, subscriptionID} {
		if len(value) == 0 || len(value) > api.InternalMailSubscriptionEndedIDMaxBytes || strings.ContainsAny(value, "\r\n\x00") {
			return false
		}
	}
	return true
}

func (s *Service) commitSubscriptionEnd(ctx context.Context, tx pgx.Tx, a q.LockSubscriptionEndedAccountRow, provider, customerID, subscriptionID string, subHash []byte) (SubscriptionEndedResult, error) {
	var result SubscriptionEndedResult
	notify := a.Plan != "free" && a.Status == "active" && !a.PastDueAt.Valid && !a.DeletionRequestedAt.Valid
	var recovery pgtype.UUID
	if notify {
		state, err := s.recovery(ctx, tx)
		if err != nil {
			return result, err
		}
		if state.Held {
			return result, internalmail.ErrRecoveryHeld
		}
		recovery = state.RecoveryID
	}
	n, err := s.queries.ClearSubscriptionEndedIdentity(ctx, tx, q.ClearSubscriptionEndedIdentityParams{AccountID: a.ID, Provider: provider, CustomerID: customerID, SubscriptionID: subscriptionID})
	if err != nil {
		return result, err
	}
	if n != 1 {
		return result, internalmail.ErrConflict
	}
	if err := s.queries.ClearSubscriptionEndedStreaming(ctx, tx, a.ID); err != nil {
		return result, err
	}
	changed, err := s.queries.DowngradeEndedSubscription(ctx, tx, q.DowngradeEndedSubscriptionParams{ID: a.ID, ProviderCustomerID: pgtype.Text{String: customerID, Valid: true}})
	if err != nil {
		return result, err
	}
	if err := s.queries.MirrorEndedSubscription(ctx, tx, q.MirrorEndedSubscriptionParams{PersonalOwnerAccountID: a.ID, ProviderCustomerID: pgtype.Text{String: customerID, Valid: true}}); err != nil {
		return result, err
	}
	if a.Plan != "free" {
		if err := s.queries.NotifySubscriptionEndedPlan(ctx, tx, a.ID); err != nil {
			return result, err
		}
	}
	var intent pgtype.UUID
	if notify {
		intent, err = s.saveSubscriptionEnded(ctx, tx, a.ID, a.Email, recovery, api.Plan(a.Plan), changed.EndedAt.Time, changed.SubscriptionMailRevision)
		if err != nil {
			return result, err
		}
	}
	if err := s.queries.RecordSubscriptionEnd(ctx, tx, q.RecordSubscriptionEndParams{AccountID: a.ID, Provider: provider, SubscriptionHash: subHash, EndedAt: changed.EndedAt, FromPlan: a.Plan, IntentID: intent}); err != nil {
		return result, err
	}
	return SubscriptionEndedResult{Applied: true, FromPlan: api.Plan(a.Plan), Status: a.Status, EndedAt: changed.EndedAt.Time}, nil
}
