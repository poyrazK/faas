package state

import (
	"context"
	"errors"
	"time"
)

// ErrOutboundAIAccessTokenLimit reports an app at its access-token ceiling.
var ErrOutboundAIAccessTokenLimit = errors.New("state: outbound AI access token limit reached")

// OutboundAIAccessToken is an AI gateway access token for one app
// (ADR-1431). TokenHash is the SHA-256 of the secret, which is never
// stored. apid owns the lifecycle; outboundd reads the hash.
type OutboundAIAccessToken struct {
	ID             string
	AccountID      string
	AppID          string
	Name           string
	Prefix         string
	TokenHash      []byte
	IntegrationIDs []string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
}

// OutboundAIAccessTokenStore owns apps' AI gateway access tokens.
type OutboundAIAccessTokenStore interface {
	// CreateOutboundAIAccessToken stores a token. Every listed integration
	// must be one of the account's enabled AI integrations bound to the
	// app; the app holds at most OutboundAIAccessTokensPerApp live tokens.
	CreateOutboundAIAccessToken(ctx context.Context, token OutboundAIAccessToken) (OutboundAIAccessToken, error)
	ListOutboundAIAccessTokens(ctx context.Context, accountID, appID string) ([]OutboundAIAccessToken, error)
	RevokeOutboundAIAccessToken(ctx context.Context, accountID, appID, tokenID string) error
}
