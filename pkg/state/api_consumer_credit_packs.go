package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ConsumerCreditPurchasePending  = "pending"
	ConsumerCreditPurchaseCredited = "credited"
	ConsumerCreditPurchaseExpired  = "expired"
)

// APIConsumerCreditPurchase holds the checkout terms and the statement credit receipt.
type APIConsumerCreditPurchase struct {
	ID, AccountID, AppID, ConsumerID string
	Pack                             api.ConsumerCreditPack
	AmountMinor                      int64
	ReturnURL, Customer, SessionID   string
	Livemode                         bool
	Status                           string
	ExpiresAt, NextCheckAt           time.Time
}

// CreditGrantID identifies the grant of a credited purchase.
func (p APIConsumerCreditPurchase) CreditGrantID() string {
	if p.Status != ConsumerCreditPurchaseCredited {
		return ""
	}
	return consumerPurchaseGrantID(p.ID)
}

func consumerPurchaseGrantID(id string) string {
	// Purchase IDs come from callers. Generate a stable version-5 UUID in a
	// separate namespace from the random version-4 IDs used by manual grants.
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale:consumer-credit-purchase:"+id)).String()
}

type APIConsumerCreditPackStore interface {
	GetConsumerCreditPackSettings(ctx context.Context, accountID, appID string) (api.ConsumerCreditPackSettings, error)
	PutConsumerCreditPackSettings(ctx context.Context, accountID, appID string, settings api.ConsumerCreditPackSettings) error
	GetConsumerCreditPurchase(ctx context.Context, accountID, appID, consumerID, id string) (APIConsumerCreditPurchase, error)
	StartConsumerCreditPurchase(ctx context.Context, purchase APIConsumerCreditPurchase) (APIConsumerCreditPurchase, error)
	SetConsumerCreditPurchaseCheckout(ctx context.Context, id, sessionID string, nextCheckAt time.Time) (APIConsumerCreditPurchase, error)
	ClaimConsumerCreditPurchases(ctx context.Context, now, nextCheckAt time.Time, limit int32) ([]APIConsumerCreditPurchase, error)
	ExpireConsumerCreditPurchase(ctx context.Context, id, sessionID string) error
	CreditConsumerPurchase(ctx context.Context, id, sessionID string) error
}

var (
	_ APIConsumerCreditPackStore = (*MemStore)(nil)
	_ APIConsumerCreditPackStore = (*PgStore)(nil)
)

func (m *MemStore) GetConsumerCreditPackSettings(_ context.Context, accountID, appID string) (api.ConsumerCreditPackSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	settings, ok := m.apiConsumerCreditPackSettings[accountID+":"+appID]
	if !ok {
		return api.ConsumerCreditPackSettings{}, ErrNotFound
	}
	settings.Packs = slices.Clone(settings.Packs)
	return settings, nil
}

func (m *MemStore) PutConsumerCreditPackSettings(_ context.Context, accountID, appID string, settings api.ConsumerCreditPackSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiConsumerCreditPackSettings == nil {
		m.apiConsumerCreditPackSettings = map[string]api.ConsumerCreditPackSettings{}
	}
	settings.Packs = slices.Clone(settings.Packs)
	m.apiConsumerCreditPackSettings[accountID+":"+appID] = settings
	return nil
}

func (m *MemStore) GetConsumerCreditPurchase(_ context.Context, accountID, appID, consumerID, id string) (APIConsumerCreditPurchase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiConsumerCreditPurchases[id]
	if !ok || p.AccountID != accountID || p.AppID != appID || p.ConsumerID != consumerID {
		return APIConsumerCreditPurchase{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) StartConsumerCreditPurchase(_ context.Context, p APIConsumerCreditPurchase) (APIConsumerCreditPurchase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.apiConsumerCreditPurchases[p.ID]; ok {
		if old.AccountID == p.AccountID && old.AppID == p.AppID && old.ConsumerID == p.ConsumerID && old.Pack.ID == p.Pack.ID {
			return old, nil
		}
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	consumer, ok := m.apiConsumers[p.ConsumerID]
	if !ok || consumer.AccountID != p.AccountID || consumer.AppID != p.AppID || consumer.RevokedAt != nil {
		return APIConsumerCreditPurchase{}, ErrNotFound
	}
	for _, old := range m.apiConsumerCreditPurchases {
		if old.ConsumerID == p.ConsumerID && old.Status == ConsumerCreditPurchasePending {
			return APIConsumerCreditPurchase{}, ErrConflict
		}
	}
	if m.countReservedConsumerCreditGrantsLocked(p.ConsumerID) >= api.ConsumerCreditOpenGrantsMax {
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	if m.apiConsumerCreditPurchases == nil {
		m.apiConsumerCreditPurchases = map[string]APIConsumerCreditPurchase{}
	}
	p.Status, p.NextCheckAt = ConsumerCreditPurchasePending, p.ExpiresAt
	m.apiConsumerCreditPurchases[p.ID] = p
	return p, nil
}

func (m *MemStore) SetConsumerCreditPurchaseCheckout(_ context.Context, id, sessionID string, nextCheckAt time.Time) (APIConsumerCreditPurchase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiConsumerCreditPurchases[id]
	if !ok || p.Status != ConsumerCreditPurchasePending || (p.SessionID != "" && p.SessionID != sessionID) {
		return APIConsumerCreditPurchase{}, ErrConflict
	}
	for otherID, other := range m.apiConsumerCreditPurchases {
		if otherID != id && other.SessionID == sessionID {
			return APIConsumerCreditPurchase{}, ErrConflict
		}
	}
	p.SessionID, p.NextCheckAt = sessionID, nextCheckAt
	m.apiConsumerCreditPurchases[id] = p
	return p, nil
}

func (m *MemStore) ClaimConsumerCreditPurchases(_ context.Context, now, nextCheckAt time.Time, limit int32) ([]APIConsumerCreditPurchase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	due := []APIConsumerCreditPurchase{}
	for _, p := range m.apiConsumerCreditPurchases {
		if p.Status == ConsumerCreditPurchasePending && !p.NextCheckAt.After(now) {
			due = append(due, p)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].NextCheckAt.Equal(due[j].NextCheckAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].NextCheckAt.Before(due[j].NextCheckAt)
	})
	due = due[:min(len(due), int(limit))]
	for _, p := range due {
		p.NextCheckAt = nextCheckAt
		m.apiConsumerCreditPurchases[p.ID] = p
	}
	return due, nil
}

func (m *MemStore) ExpireConsumerCreditPurchase(_ context.Context, id, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.apiConsumerCreditPurchases[id]; ok && p.Status == ConsumerCreditPurchasePending && p.SessionID == sessionID {
		p.Status = ConsumerCreditPurchaseExpired
		m.apiConsumerCreditPurchases[id] = p
	}
	return nil
}

func (m *MemStore) CreditConsumerPurchase(_ context.Context, id, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiConsumerCreditPurchases[id]
	if !ok || p.SessionID != sessionID || sessionID == "" {
		return ErrNotFound
	}
	if p.Status == ConsumerCreditPurchaseCredited {
		return nil
	}
	if p.Status != ConsumerCreditPurchasePending {
		return ErrConflict
	}
	if m.apiConsumerCreditGrants == nil {
		m.apiConsumerCreditGrants = map[string]APIConsumerCreditGrant{}
	}
	grantID := consumerPurchaseGrantID(id)
	now := time.Now().UTC()
	m.apiConsumerCreditGrants[grantID] = APIConsumerCreditGrant{
		ID: grantID, AccountID: p.AccountID, AppID: p.AppID, ConsumerID: p.ConsumerID,
		Currency: p.Pack.Currency, AmountMillicents: p.Pack.AmountMillicents, RemainingMillicents: p.Pack.AmountMillicents,
		Note: "Stripe credit pack", CreatedAt: now,
	}
	if cap, ok := m.apiConsumerSpendCaps[p.ConsumerID]; ok && cap.AccountID == p.AccountID && cap.Currency == p.Pack.Currency {
		cap.UpdatedAt = now
		if now.Before(cap.NextCheckAt) {
			cap.NextCheckAt = now
		}
		m.apiConsumerSpendCaps[p.ConsumerID] = cap
	}
	p.Status = ConsumerCreditPurchaseCredited
	m.apiConsumerCreditPurchases[id] = p
	return nil
}
