package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const PlatformTenantAccessTokenLimit = 10

type PlatformTenantAccessTokenQuotaError struct {
	Limit    int
	Observed int
}

func (e *PlatformTenantAccessTokenQuotaError) Error() string {
	return "platform tenant access token quota reached"
}

// PlatformTenantAccessToken is the stored, hashed read-only capability for a
// single downstream platform tenant. Plaintext is never part of state.
type PlatformTenantAccessToken struct {
	ID         string
	AccountID  string
	TenantID   string
	Name       string
	Prefix     string
	TokenHash  []byte
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type PlatformTenantAccessTokenInput struct {
	AccountID string
	TenantID  string
	Name      string
	Prefix    string
	TokenHash []byte
	Scopes    []string
	ExpiresAt time.Time
}

// PlatformTenantAccessStore manages opaque tenant-bound tokens. Authenticate
// returns only an active, unexpired token and the account it belongs to.
type PlatformTenantAccessStore interface {
	CreatePlatformTenantAccessToken(context.Context, PlatformTenantAccessTokenInput) (PlatformTenantAccessToken, error)
	ListPlatformTenantAccessTokens(context.Context, string, string) ([]PlatformTenantAccessToken, error)
	RevokePlatformTenantAccessToken(context.Context, string, string, string) (PlatformTenantAccessToken, bool, error)
	AuthenticatePlatformTenantAccessToken(context.Context, []byte) (Account, PlatformTenantAccessToken, error)
}

func validatePlatformTenantAccessTokenInput(in PlatformTenantAccessTokenInput) error {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ErrInvalidArgument
	}
	prefixSuffix := strings.TrimPrefix(in.Prefix, api.PlatformTenantAccessTokenPrefix)
	if in.Name == "" || len(in.Name) > 64 ||
		strings.TrimSpace(in.Name) != in.Name || len(in.TokenHash) != 32 ||
		len(prefixSuffix) != 8 || in.Prefix != api.PlatformTenantAccessTokenPrefix+prefixSuffix ||
		len(in.Scopes) == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now().UTC()) {
		return ErrInvalidArgument
	}
	if _, err := hex.DecodeString(prefixSuffix); err != nil {
		return ErrInvalidArgument
	}
	for _, r := range in.Name {
		if unicode.IsControl(r) {
			return ErrInvalidArgument
		}
	}
	seen := map[string]bool{}
	for _, scope := range in.Scopes {
		if (scope != api.ScopePlatformTenantUsageRead && scope != api.ScopePlatformTenantStatementsRead) || seen[scope] {
			return ErrInvalidArgument
		}
		seen[scope] = true
	}
	return nil
}

func clonePlatformTenantAccessToken(in PlatformTenantAccessToken) PlatformTenantAccessToken {
	in.TokenHash = append([]byte(nil), in.TokenHash...)
	in.Scopes = append([]string(nil), in.Scopes...)
	if in.LastUsedAt != nil {
		value := *in.LastUsedAt
		in.LastUsedAt = &value
	}
	if in.RevokedAt != nil {
		value := *in.RevokedAt
		in.RevokedAt = &value
	}
	return in
}

var _ PlatformTenantAccessStore = (*PgStore)(nil)
var _ PlatformTenantAccessStore = (*MemStore)(nil)
