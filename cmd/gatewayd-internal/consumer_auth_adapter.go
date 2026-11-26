package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumerAuthStore adapts the durable state.Store consumer-key surface to the
// gateway's narrow, state-free interface. Keeping this conversion at the
// daemon boundary lets the request path use the same PgStore rows as apid
// without coupling pkg/gateway to sqlc-generated types.
type consumerAuthStore struct {
	store *state.PgStore
}

func newConsumerAuthStore(store *state.PgStore) *consumerAuthStore {
	if store == nil {
		return nil
	}
	return &consumerAuthStore{store: store}
}

func (s *consumerAuthStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (gateway.ConsumerAuthKey, error) {
	key, err := s.store.ConsumerKeyByAppAndPrefix(ctx, accountID, appID, prefix)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ConsumerAuthKey{}, gateway.ErrConsumerAuthNotFound
		}
		return gateway.ConsumerAuthKey{}, err
	}
	return gateway.ConsumerAuthKey{
		ID:         key.ID,
		AccountID:  key.AccountID,
		AppID:      key.AppID,
		ConsumerID: key.ConsumerID,
		Prefix:     key.Prefix,
		Hash:       append([]byte(nil), key.Hash...),
		Scopes:     append([]string(nil), key.Scopes...),
		ExpiresAt:  key.ExpiresAt,
		RevokedAt:  key.RevokedAt,
	}, nil
}

func (s *consumerAuthStore) GetAPIConsumerByID(ctx context.Context, accountID, consumerID string) (gateway.ConsumerAuthConsumer, error) {
	consumer, err := s.store.GetAPIConsumerByID(ctx, accountID, consumerID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ConsumerAuthConsumer{}, gateway.ErrConsumerAuthNotFound
		}
		return gateway.ConsumerAuthConsumer{}, err
	}
	return gateway.ConsumerAuthConsumer{
		ID:               consumer.ID,
		AccountID:        consumer.AccountID,
		AppID:            consumer.AppID,
		PlatformTenantID: consumer.PlatformTenantID,
		ExternalRef:      consumer.ExternalRef,
		Status:           string(consumer.Status),
		RevokedAt:        consumer.RevokedAt,
	}, nil
}

func (s *consumerAuthStore) TouchConsumerKeyLastUsed(ctx context.Context, keyID string) error {
	return s.store.TouchConsumerKeyLastUsed(ctx, keyID)
}

var _ gateway.ConsumerAuthStore = (*consumerAuthStore)(nil)

func (s *consumerAuthStore) APISandboxPartnerKeyActive(ctx context.Context, appID, keyID string) (bool, error) {
	return s.store.APISandboxPartnerKeyActive(ctx, appID, keyID)
}

func (s *consumerAuthStore) RecordAPISandboxPartnerSuccess(ctx context.Context, appID, keyID string, at time.Time) error {
	return s.store.RecordAPISandboxPartnerSuccess(ctx, appID, keyID, at)
}

func (s *consumerAuthStore) ClaimAPISandboxPartnerStep(ctx context.Context, appID, deploymentID, keyID, method, target string, at time.Time) (api.APISandboxPartnerStep, error) {
	step, err := s.store.ClaimAPISandboxPartnerStep(ctx, appID, deploymentID, keyID, method, target, at)
	if errors.Is(err, state.ErrNotFound) {
		return api.APISandboxPartnerStep{}, gateway.ErrConsumerAuthNotFound
	}
	return step, err
}

func (s *consumerAuthStore) CompleteAPISandboxPartnerStep(ctx context.Context, permit api.APISandboxPartnerStep, at time.Time) error {
	return s.store.CompleteAPISandboxPartnerStep(ctx, permit, at)
}
