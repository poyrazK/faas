package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformTenantCredentialStore reconciles client-generated key hashes and
// revocations in one account-scoped transaction. Plaintext never enters state.
type PlatformTenantCredentialStore interface {
	ApplyPlatformTenantCredentials(context.Context, ApplyPlatformTenantCredentialsParams) (ApplyPlatformTenantCredentialsResult, error)
	ListPlatformTenantCredentials(context.Context, string, string, int, int) ([]ConsumerKey, error)
}

type ApplyPlatformTenantCredentialsParams struct {
	AccountID    string
	TenantID     string
	DryRun       bool
	AppLimit     int
	AccountLimit int
	Keys         []PlatformTenantCredentialIntent
	RevokeKeyIDs []string
}

type PlatformTenantCredentialIntent struct {
	ConsumerID string
	Name       string
	Prefix     string
	Hash       []byte
	Scopes     []string
	ExpiresAt  *time.Time
}

type PlatformTenantCredentialResult struct {
	Key    ConsumerKey
	Action string // create, unchanged, revoke
}

type ApplyPlatformTenantCredentialsResult struct {
	TenantID string
	DryRun   bool
	Keys     []PlatformTenantCredentialResult
}

type PlatformTenantCredentialQuotaError struct {
	Scope    string // app, account
	Limit    int
	Observed int
}

func (e *PlatformTenantCredentialQuotaError) Error() string {
	return "platform tenant credential quota exceeded"
}

var (
	_ PlatformTenantCredentialStore = (*PgStore)(nil)
	_ PlatformTenantCredentialStore = (*MemStore)(nil)
)

func validatePlatformTenantCredentials(in ApplyPlatformTenantCredentialsParams) error {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ErrInvalidArgument
	}
	if in.AppLimit < 1 || in.AccountLimit < 1 || len(in.Keys)+len(in.RevokeKeyIDs) == 0 {
		return ErrInvalidArgument
	}
	seenKeys := make(map[string]bool, len(in.Keys))
	seenPrefixes := make(map[string]bool, len(in.Keys))
	for _, key := range in.Keys {
		if _, err := uuid.Parse(key.ConsumerID); err != nil {
			return ErrInvalidArgument
		}
		if strings.TrimSpace(key.Name) != key.Name || key.Name == "" || len(key.Name) > 64 ||
			len(key.Prefix) != 8 || len(key.Hash) != 32 || len(key.Scopes) == 0 {
			return ErrInvalidArgument
		}
		if _, err := hex.DecodeString(key.Prefix); err != nil || strings.ToLower(key.Prefix) != key.Prefix {
			return ErrInvalidArgument
		}
		identity := key.ConsumerID + "\x00" + key.Name
		if seenKeys[identity] {
			return ErrInvalidArgument
		}
		seenKeys[identity] = true
		// A prefix is unique per app. Checking the whole bundle is stricter
		// but avoids an unusable pair even before consumer ownership reads.
		if seenPrefixes[key.Prefix] {
			return ErrInvalidArgument
		}
		seenPrefixes[key.Prefix] = true
		seenScopes := make(map[string]bool, len(key.Scopes))
		for _, scope := range key.Scopes {
			if scope != "read" && scope != "write" && scope != "admin" || seenScopes[scope] {
				return ErrInvalidArgument
			}
			seenScopes[scope] = true
		}
	}
	seenRevoke := make(map[string]bool, len(in.RevokeKeyIDs))
	for _, id := range in.RevokeKeyIDs {
		if _, err := uuid.Parse(id); err != nil || seenRevoke[id] {
			return ErrInvalidArgument
		}
		seenRevoke[id] = true
	}
	return nil
}

func sameCredentialIntent(current ConsumerKey, wanted PlatformTenantCredentialIntent) bool {
	if current.ConsumerID != wanted.ConsumerID || current.Name != wanted.Name || current.Prefix != wanted.Prefix ||
		!bytes.Equal(current.Hash, wanted.Hash) || current.RevokedAt != nil || len(current.Scopes) != len(wanted.Scopes) {
		return false
	}
	for i := range current.Scopes {
		if current.Scopes[i] != wanted.Scopes[i] {
			return false
		}
	}
	if (current.ExpiresAt == nil) != (wanted.ExpiresAt == nil) {
		return false
	}
	if current.ExpiresAt != nil && !current.ExpiresAt.Truncate(time.Microsecond).Equal(wanted.ExpiresAt.Truncate(time.Microsecond)) {
		return false
	}
	return true
}
