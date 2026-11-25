package consumersignup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// LinkOAuthConsumer points an OAuth caller (in's account, app, issuer,
// subject and email; in.ConsumerID is ignored) at the consumer whose key
// the caller proved, and revokes the consumer the caller's first sign-in
// enrolled (ADR-20261124094745). The gateway verified the caller's token;
// this verifies the key. It returns the identity and the revoked
// consumer's ID ("" when none). state.ErrAPIConsumerKeyRejected, state.ErrNotFound when
// the app is not the account's, state.ErrConflict when the identity is
// linked to another key's consumer, and state.ErrAPIConsumerOAuthOff when
// the app does not enroll OAuth callers.
func (s *Service) LinkOAuthConsumer(ctx context.Context, in state.APIConsumerIdentityLink, key string) (state.APIConsumerIdentity, string, error) {
	links, ok := s.Host.Store().(state.APIConsumerIdentityLinkStore)
	if !ok || !s.Host.Released(in.AccountID) {
		return state.APIConsumerIdentity{}, "", state.ErrAPIConsumerOAuthOff
	}
	app, err := s.oauthApp(ctx, in.AccountID, in.AppID)
	if err != nil {
		return state.APIConsumerIdentity{}, "", err
	}
	consumerID, err := s.consumerForKey(ctx, app, key)
	if err != nil {
		return state.APIConsumerIdentity{}, "", err
	}
	in.ConsumerID = consumerID
	identity, revoked, err := links.LinkAPIConsumerIdentity(ctx, in)
	if errors.Is(err, state.ErrNotFound) {
		// The consumer closed between the key check and the link.
		return state.APIConsumerIdentity{}, "", state.ErrAPIConsumerKeyRejected
	}
	if err != nil {
		return state.APIConsumerIdentity{}, "", fmt.Errorf("link oauth consumer: %w", err)
	}
	s.Host.Audit(ctx, "api_consumer.oauth_linked", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": consumerID,
		"identity_id": identity.ID, "issuer": identity.Issuer, "revoked_consumer_id": revoked})
	if revoked != "" {
		s.Host.Audit(ctx, "api_consumer.revoked", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": revoked,
			"reason": "oauth_linked"})
	}
	return identity, revoked, nil
}

// oauthApp returns the account's app when it enrolls OAuth callers.
func (s *Service) oauthApp(ctx context.Context, accountID, appID string) (state.App, error) {
	app, err := s.Host.Store().AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && app.AccountID != accountID) {
		return state.App{}, state.ErrNotFound
	}
	if err != nil {
		return state.App{}, fmt.Errorf("link oauth consumer: app: %w", err)
	}
	signups, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		return state.App{}, state.ErrAPIConsumerOAuthOff
	}
	set, err := signups.GetAPIConsumerSignupSettings(ctx, accountID, appID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (!set.Enabled || !set.OAuthConsumers)) {
		return state.App{}, state.ErrAPIConsumerOAuthOff
	}
	if err != nil {
		return state.App{}, fmt.Errorf("link oauth consumer: settings: %w", err)
	}
	return app, nil
}

// consumerForKey returns the active consumer key is a live key of on app.
func (s *Service) consumerForKey(ctx context.Context, app state.App, key string) (string, error) {
	if !api.ValidConsumerKeyFormat(key) {
		return "", state.ErrAPIConsumerKeyRejected
	}
	prefix, _, _ := strings.Cut(strings.TrimPrefix(key, api.ConsumerKeyPrefix), "_")
	row, err := s.Host.Store().ConsumerKeyByAppAndPrefix(ctx, app.AccountID, app.ID, prefix)
	if errors.Is(err, state.ErrNotFound) {
		return "", state.ErrAPIConsumerKeyRejected
	}
	if err != nil {
		return "", fmt.Errorf("link oauth consumer: key: %w", err)
	}
	if !state.CredentialMatches(state.CredentialColumnConsumerKey, api.HashConsumerKey(key), row.Hash) || row.RevokedAt != nil ||
		(row.ExpiresAt != nil && !row.ExpiresAt.After(s.now())) || row.ConsumerID == "" {
		return "", state.ErrAPIConsumerKeyRejected
	}
	return row.ConsumerID, nil
}
