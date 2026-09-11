package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

func validateAPIConsumerInput(op, accountID, appID, externalRef, name string) error {
	if accountID == "" || appID == "" {
		return fmt.Errorf("memstore: %s: empty account_id or app_id", op)
	}
	if externalRef == "" {
		return fmt.Errorf("memstore: %s: external_ref cannot be empty", op)
	}
	if len(externalRef) > 256 {
		return fmt.Errorf("memstore: %s: external_ref exceeds 256 characters", op)
	}
	if name == "" {
		return fmt.Errorf("memstore: %s: name cannot be empty", op)
	}
	if len(name) > 128 {
		return fmt.Errorf("memstore: %s: name exceeds 128 characters", op)
	}
	return nil
}

func (m *MemStore) CreateAPIConsumer(_ context.Context, accountID, appID, externalRef, name string) (APIConsumer, error) {
	if err := validateAPIConsumerInput("CreateAPIConsumer", accountID, appID, externalRef, name); err != nil {
		return APIConsumer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.apiConsumers {
		if c.AppID == appID && c.ExternalRef == externalRef {
			return APIConsumer{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	c := APIConsumer{
		ID:          uuid.NewString(),
		AccountID:   accountID,
		AppID:       appID,
		ExternalRef: externalRef,
		Name:        name,
		Status:      APIConsumerStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.apiConsumers[c.ID] = c
	return c, nil
}

func (m *MemStore) GetAPIConsumerByID(_ context.Context, accountID, consumerID string) (APIConsumer, error) {
	if accountID == "" || consumerID == "" {
		return APIConsumer{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return APIConsumer{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) ListAPIConsumersForApp(_ context.Context, accountID, appID string) ([]APIConsumer, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumer
	for _, c := range m.apiConsumers {
		if c.AccountID == accountID && c.AppID == appID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) RevokeAPIConsumer(_ context.Context, accountID, consumerID string) (APIConsumer, error) {
	if accountID == "" || consumerID == "" {
		return APIConsumer{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return APIConsumer{}, ErrNotFound
	}
	if c.RevokedAt == nil {
		now := time.Now().UTC()
		c.RevokedAt = &now
		c.UpdatedAt = now
		c.Status = APIConsumerStatusRevoked
		m.apiConsumers[c.ID] = c
	}
	return c, nil
}

func validateConsumerKeyForConsumer(op, accountID, consumerID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) error {
	if accountID == "" || consumerID == "" {
		return fmt.Errorf("memstore: %s: empty account_id or consumer_id", op)
	}
	if name == "" || len(name) > 64 {
		return fmt.Errorf("memstore: %s: name must be 1-64 characters", op)
	}
	if prefix == "" || len(prefix) > 16 {
		return fmt.Errorf("memstore: %s: prefix must be 1-16 characters", op)
	}
	if len(hash) != 32 {
		return fmt.Errorf("memstore: %s: hash must be 32 bytes, got %d", op, len(hash))
	}
	if len(scopes) == 0 {
		return errors.New("memstore: CreateConsumerKeyForConsumer: scopes cannot be empty")
	}
	for _, scope := range scopes {
		switch scope {
		case "read", "write", "admin":
		default:
			return fmt.Errorf("memstore: %s: scope %q is not in the closed-set {read, write, admin}", op, scope)
		}
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return fmt.Errorf("memstore: %s: expires_at must be in the future", op)
	}
	return nil
}

func (m *MemStore) CreateConsumerKeyForConsumer(_ context.Context, accountID, consumerID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) (ConsumerKey, error) {
	if err := validateConsumerKeyForConsumer("CreateConsumerKeyForConsumer", accountID, consumerID, name, prefix, hash, scopes, expiresAt); err != nil {
		return ConsumerKey{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return ConsumerKey{}, ErrNotFound
	}
	if !c.Active() {
		return ConsumerKey{}, ErrConflict
	}
	for _, k := range m.consumerKeys {
		if k.AccountID == c.AccountID && k.AppID == c.AppID && k.Name == name {
			return ConsumerKey{}, ErrConflict
		}
		if k.AppID == c.AppID && k.Prefix == prefix {
			return ConsumerKey{}, ErrConflict
		}
	}
	k := ConsumerKey{
		ID:         newID(),
		AccountID:  c.AccountID,
		AppID:      c.AppID,
		ConsumerID: c.ID,
		Name:       name,
		Prefix:     prefix,
		Hash:       append([]byte(nil), hash...),
		Scopes:     append([]string(nil), scopes...),
		CreatedAt:  time.Now().UTC(),
		ExpiresAt:  expiresAt,
	}
	m.consumerKeys[k.ID] = k
	return k, nil
}
