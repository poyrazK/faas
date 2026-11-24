package state

import (
	"context"
	"errors"
	"fmt"
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
	// CountCardlessAPIConsumersOnPlan counts the app's active card-less
	// consumers whose plan at at is planID, or who move to it later: a
	// rate card that charges on that plan could not be collected from them.
	CountCardlessAPIConsumersOnPlan(ctx context.Context, accountID, appID, planID string, at time.Time) (int, error)
}

// CountCardlessAPIConsumersOnPlan implements APIConsumerUpgradeStore.
func (m *MemStore) CountCardlessAPIConsumersOnPlan(_ context.Context, accountID, appID, planID string, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type current struct {
		plan string
		from time.Time
		set  bool
	}
	now := map[string]current{}
	later := map[string]bool{}
	for _, a := range m.apiConsumerPlanAssignments {
		if a.AccountID != accountID || a.AppID != appID {
			continue
		}
		if a.EffectiveFrom.After(at) {
			later[a.ConsumerID] = later[a.ConsumerID] || a.PlanID == planID
			continue
		}
		if c := now[a.ConsumerID]; !c.set || a.EffectiveFrom.After(c.from) {
			now[a.ConsumerID] = current{plan: a.PlanID, from: a.EffectiveFrom, set: true}
		}
	}
	n := 0
	for _, c := range m.apiConsumers {
		if c.AccountID != accountID || c.AppID != appID || !c.Active() || !APIConsumerCardless(c.ExternalRef) {
			continue
		}
		if cur := now[c.ID]; (cur.set && cur.plan == planID) || later[c.ID] {
			n++
		}
	}
	return n, nil
}

// CountCardlessAPIConsumersOnPlan implements APIConsumerUpgradeStore.
func (s *PgStore) CountCardlessAPIConsumersOnPlan(ctx context.Context, accountID, appID, planID string, at time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select count(*) from api_consumers c
		where c.account_id = $1::uuid and c.app_id = $2::uuid and c.status = 'active' and c.revoked_at is null
		  and (c.external_ref like 'signup:%' or c.external_ref like 'oauth:%')
		  and exists (select 1 from api_consumer_plan_assignments a where a.consumer_id = c.id and a.plan_id = $3::uuid
		    and (a.effective_from > $4 or a.effective_from = (select max(b.effective_from) from api_consumer_plan_assignments b
		      where b.consumer_id = c.id and b.effective_from <= $4)))`, accountID, appID, planID, at).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count card-less consumers on plan: %w", err)
	}
	return n, nil
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

// APIConsumerHasStripeCustomer reports whether ref is a Stripe customer, so
// the consumer has a card to bill.
func APIConsumerHasStripeCustomer(ref string) bool { return validStripeCustomerID(ref) }

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
