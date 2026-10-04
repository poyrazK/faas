package state

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type RegionalReleaseGrant struct {
	ID, CellID, AppID, AccountID, OrgID, GlobalAppID string
	Hash                                             []byte
	CreatedAt, ExpiresAt                             time.Time
	Status                                           string
	RevokedAt                                        *time.Time
}

type RegionalReleaseGrantStore interface {
	PutRegionalReleaseGrant(context.Context, RegionalReleaseGrant) (RegionalReleaseGrant, error)
	GetRegionalReleaseGrant(context.Context, string, string, string, string) (RegionalReleaseGrant, error)
	RevokeRegionalReleaseGrant(context.Context, string, string, string, string) (RegionalReleaseGrant, error)
	AuthenticateRegionalReleaseGrant(context.Context, []byte) (Account, APIKey, error)
}

type RegionalReleaseGrantLimitError struct{ Limit, Observed int64 }

func (e *RegionalReleaseGrantLimitError) Error() string { return "regional active grant bound reached" }

func (g RegionalReleaseGrant) valid() bool {
	return validRegionalReleaseIDs(g.CellID, g.AppID, g.ID) && validRegionalReleaseIDs(g.GlobalAppID, g.AppID, g.ID) && g.AccountID != "" && len(g.Hash) == 32 && !g.ExpiresAt.IsZero()
}
func (g RegionalReleaseGrant) matches(other RegionalReleaseGrant) bool {
	return g.ID == other.ID && g.CellID == other.CellID && g.AppID == other.AppID && g.AccountID == other.AccountID && g.OrgID == other.OrgID && g.GlobalAppID == other.GlobalAppID && bytes.Equal(g.Hash, other.Hash) && g.ExpiresAt.Equal(other.ExpiresAt)
}
func cloneRegionalReleaseGrant(g RegionalReleaseGrant) RegionalReleaseGrant {
	g.Hash = append([]byte(nil), g.Hash...)
	if g.RevokedAt != nil {
		stamp := *g.RevokedAt
		g.RevokedAt = &stamp
	}
	return g
}
func (g RegionalReleaseGrant) principal() APIKey {
	expiry := g.ExpiresAt
	return APIKey{ID: g.ID, AccountID: g.AccountID, OrgID: g.OrgID, AppID: g.AppID, RegionalReleaseGrantID: g.ID, RegionalReleaseCellID: g.CellID, RegionalReleaseGlobalAppID: g.GlobalAppID, Scopes: []string{api.ScopeRegionalImageRelease}, Status: g.Status, ExpiresAt: &expiry}
}
func (g RegionalReleaseGrant) active(now time.Time) error {
	if g.Status != "active" {
		return ErrAPIKeyRevoked
	}
	if !g.ExpiresAt.After(now) {
		return ErrAPIKeyExpired
	}
	return nil
}
func validRegionalGrantExpiry(expiry, now time.Time) error {
	if !expiry.After(now) {
		return fmt.Errorf("state: regional grant expiry: %w", ErrInvalidArgument)
	}
	if expiry.After(now.Add(api.RegionalReleaseGrantMaxLifetime)) {
		return fmt.Errorf("state: regional grant lifetime: %w", ErrInvalidArgument)
	}
	return nil
}
