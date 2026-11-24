package consumersignup

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// OAuth consumers (ADR-20261124094745): a person who signs in through an
// app's MCP oauth-proxy issuer becomes one of the app's API consumers on its
// free plan. A gateway verifies the token and asks apid, the only writer of
// consumers, through the Auth service's EnrollOAuthConsumer.

// EnrollOAuthConsumer returns the consumer an OAuth caller (in's account,
// app, issuer, subject and email) is, creating it on the app's free plan the
// first time, and reports created. It sets in's times itself.
// state.ErrNotFound when the app is not the account's or the free plan is
// gone, state.ErrAPIConsumerSignupLimit, and state.ErrAPIConsumerOAuthOff
// when API monetization is not released to the account, the app's sign-up
// or OAuth consumers are off, or the store cannot hold identities.
func (s *Service) EnrollOAuthConsumer(ctx context.Context, in state.APIConsumerEnrollment) (state.APIConsumer, bool, error) {
	if !s.Host.Released(in.AccountID) {
		return state.APIConsumer{}, false, state.ErrAPIConsumerOAuthOff
	}
	store, ok := s.Host.Store().(state.APIConsumerIdentityStore)
	if !ok {
		return state.APIConsumer{}, false, state.ErrAPIConsumerOAuthOff
	}
	app, err := s.Host.Store().AppByID(ctx, in.AppID)
	if err != nil {
		return state.APIConsumer{}, false, fmt.Errorf("enroll oauth consumer: app: %w", err)
	}
	if app.AccountID != in.AccountID {
		return state.APIConsumer{}, false, fmt.Errorf("enroll oauth consumer: %w", state.ErrNotFound)
	}
	now := s.now()
	in.EffectiveFrom, in.DayStart = now.Truncate(time.Minute), now.Truncate(24*time.Hour)
	identity, consumer, created, err := store.EnrollAPIConsumerIdentity(ctx, in)
	if err != nil {
		return state.APIConsumer{}, false, fmt.Errorf("enroll oauth consumer: %w", err)
	}
	if created {
		s.Host.Audit(ctx, "api_consumer.created", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
			"external_ref": consumer.ExternalRef, "identity_id": identity.ID})
		s.Host.Audit(ctx, "api_consumer.oauth_enrolled", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
			"identity_id": identity.ID, "issuer": identity.Issuer})
	}
	return consumer, created, nil
}
