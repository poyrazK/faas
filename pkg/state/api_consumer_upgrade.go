package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Upgrading a card-less consumer (ADR-20261124094745): a consumer that
// joined the free plan through the sign-up page or an MCP OAuth sign-in has
// no Stripe customer. When it saves a card to move to a paid plan, the new
// Stripe customer becomes its external_ref, the ref billing exports use.

// APIConsumerUpgradeStore attaches a Stripe customer to a card-less
// consumer. Optional, like the other pricing stores.
type APIConsumerUpgradeStore interface {
	// AttachAPIConsumerStripeCustomer sets the consumer's external_ref to
	// customerID when the ref is a card-less one ("signup:" or "oauth:");
	// it is a no-op when the ref already is customerID. ErrConflict when
	// the consumer is revoked, already has another ref, or another
	// consumer of the app has customerID; ErrNotFound when it is not the
	// account's.
	AttachAPIConsumerStripeCustomer(ctx context.Context, accountID, consumerID, customerID string) (APIConsumer, error)
}

var (
	_ APIConsumerUpgradeStore = (*MemStore)(nil)
	_ APIConsumerUpgradeStore = (*PgStore)(nil)
)

// APIConsumerCardless reports whether ref names a consumer without a
// Stripe customer: a free-plan sign-up or an OAuth enrollment.
func APIConsumerCardless(ref string) bool {
	return strings.HasPrefix(ref, "signup:") || strings.HasPrefix(ref, "oauth:")
}

func validStripeCustomerID(id string) bool {
	return strings.HasPrefix(id, "cus_") && len(id) <= 256
}

// AttachAPIConsumerStripeCustomer implements APIConsumerUpgradeStore.
func (m *MemStore) AttachAPIConsumerStripeCustomer(_ context.Context, accountID, consumerID, customerID string) (APIConsumer, error) {
	if !validStripeCustomerID(customerID) {
		return APIConsumer{}, errors.New("state: attach stripe customer: customer must be a cus_ id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return APIConsumer{}, ErrNotFound
	}
	if c.ExternalRef == customerID {
		return c, nil
	}
	if !c.Active() || !APIConsumerCardless(c.ExternalRef) {
		return APIConsumer{}, ErrConflict
	}
	for _, other := range m.apiConsumers {
		if other.AppID == c.AppID && other.ExternalRef == customerID {
			return APIConsumer{}, ErrConflict
		}
	}
	c.ExternalRef, c.UpdatedAt = customerID, time.Now().UTC()
	m.apiConsumers[consumerID] = c
	return c, nil
}

// AttachAPIConsumerStripeCustomer implements APIConsumerUpgradeStore.
func (s *PgStore) AttachAPIConsumerStripeCustomer(ctx context.Context, accountID, consumerID, customerID string) (APIConsumer, error) {
	if !validStripeCustomerID(customerID) {
		return APIConsumer{}, errors.New("pgstore: attach stripe customer: customer must be a cus_ id")
	}
	if accountID == "" || consumerID == "" {
		return APIConsumer{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`update api_consumers
		    set external_ref = $3, updated_at = now()
		  where id = $1::uuid and account_id = $2::uuid
		    and status = 'active' and revoked_at is null
		    and (external_ref like 'signup:%' or external_ref like 'oauth:%')
		 returning `+apiConsumerSelectCols,
		consumerID, accountID, customerID)
	c, err := scanAPIConsumerRow(row)
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return APIConsumer{}, ErrConflict
	case errors.Is(err, pgx.ErrNoRows):
		current, getErr := s.GetAPIConsumerByID(ctx, accountID, consumerID)
		if getErr != nil {
			return APIConsumer{}, getErr
		}
		if current.ExternalRef == customerID {
			return current, nil
		}
		return APIConsumer{}, ErrConflict
	}
	return c, err
}
