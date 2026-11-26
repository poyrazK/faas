package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// StripeConsumerLink identifies the verified invoice customer for an API consumer.
type StripeConsumerLink struct {
	AccountID        string
	ConsumerID       string
	StripeAccountID  string
	Livemode         bool
	StripeCustomerID string
}

type StripeConsumerLinkStore interface {
	// LinkStripeConsumerCustomer creates the verified link against the
	// reviewed connection. Repeating the same link returns changed=false.
	LinkStripeConsumerCustomer(ctx context.Context, link StripeConsumerLink, connectionVersion time.Time) (changed bool, err error)
	GetStripeConsumerCustomer(ctx context.Context, accountID, consumerID, stripeAccountID string, livemode bool) (string, error)
}

type stripeConsumerScope struct {
	account  string
	livemode bool
}

func (m *MemStore) LinkStripeConsumerCustomer(_ context.Context, link StripeConsumerLink, expected time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	consumer, ok := m.apiConsumers[link.ConsumerID]
	if !ok || consumer.AccountID != link.AccountID {
		return false, ErrNotFound
	}
	key := link.AccountID + "/" + BillingProviderStripe
	conn, ok := m.billingConnections[key]
	if !ok || conn.Collection != CollectionNone || !conn.UpdatedAt.Equal(expected) || conn.Livemode != link.Livemode ||
		(conn.StripeAccountID != "" && conn.StripeAccountID != link.StripeAccountID) {
		return false, ErrConflict
	}
	scope := stripeConsumerScope{link.StripeAccountID, link.Livemode}
	prior := m.apiConsumerStripeLinks[link.ConsumerID][scope]
	if prior != "" && prior != link.StripeCustomerID {
		return false, ErrConflict
	}
	if m.apiConsumerStripeLinks == nil {
		m.apiConsumerStripeLinks = make(map[string]map[stripeConsumerScope]string)
	}
	if m.apiConsumerStripeLinks[link.ConsumerID] == nil {
		m.apiConsumerStripeLinks[link.ConsumerID] = make(map[stripeConsumerScope]string)
	}
	conn.StripeAccountID = link.StripeAccountID
	m.billingConnections[key] = conn
	m.apiConsumerStripeLinks[link.ConsumerID][scope] = link.StripeCustomerID
	return prior == "", nil
}

func (m *MemStore) GetStripeConsumerCustomer(_ context.Context, accountID, consumerID, stripeAccountID string, livemode bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	consumer, ok := m.apiConsumers[consumerID]
	customer := m.apiConsumerStripeLinks[consumerID][stripeConsumerScope{stripeAccountID, livemode}]
	if !ok || consumer.AccountID != accountID || customer == "" {
		return "", ErrNotFound
	}
	return customer, nil
}

func (s *PgStore) LinkStripeConsumerCustomer(ctx context.Context, link StripeConsumerLink, expected time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin Stripe customer link: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `update account_billing_connections set stripe_account_id = $2
		where account_id = $1::uuid and provider = 'stripe' and collection = 'none'
		  and livemode = $3 and updated_at = $4
		  and (stripe_account_id is null or stripe_account_id = $2)`, link.AccountID, link.StripeAccountID, link.Livemode, expected)
	if err != nil {
		return false, fmt.Errorf("verify Stripe connection for customer link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, ErrConflict
	}
	tag, err = tx.Exec(ctx, `insert into api_consumer_stripe_links (consumer_id, stripe_account_id, livemode, stripe_customer_id)
		select id, $3, $4, $5 from api_consumers where id = $2::uuid and account_id = $1::uuid
		on conflict (consumer_id, stripe_account_id, livemode) do nothing`,
		link.AccountID, link.ConsumerID, link.StripeAccountID, link.Livemode, link.StripeCustomerID)
	if err != nil {
		return false, fmt.Errorf("save Stripe customer link: %w", err)
	}
	changed := tag.RowsAffected() == 1
	if !changed {
		customer, err := stripeConsumerCustomer(ctx, tx, link.AccountID, link.ConsumerID, link.StripeAccountID, link.Livemode)
		if err != nil {
			return false, err
		}
		if customer != link.StripeCustomerID {
			return false, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit Stripe customer link: %w", err)
	}
	return changed, nil
}

func (s *PgStore) GetStripeConsumerCustomer(ctx context.Context, accountID, consumerID, stripeAccountID string, livemode bool) (string, error) {
	return stripeConsumerCustomer(ctx, s.pool, accountID, consumerID, stripeAccountID, livemode)
}

func stripeConsumerCustomer(ctx context.Context, q rowQuerier, accountID, consumerID, stripeAccountID string, livemode bool) (string, error) {
	var id string
	err := q.QueryRow(ctx, `select link.stripe_customer_id
		from api_consumer_stripe_links link join api_consumers c on c.id = link.consumer_id
		where c.account_id = $1::uuid and link.consumer_id = $2::uuid and link.stripe_account_id = $3 and link.livemode = $4`,
		accountID, consumerID, stripeAccountID, livemode).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load Stripe customer link: %w", err)
	}
	return id, nil
}
