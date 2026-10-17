package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type HostingAutomationStore interface {
	CreateHostingAutomationCredential(context.Context, api.HostingAutomationCredential) (api.HostingAutomationCredential, error)
	ListHostingAutomationCredentials(context.Context, string, string) ([]api.HostingAutomationCredential, error)
	RevokeHostingAutomationCredential(context.Context, string, string, string) error
	AuthenticateHostingAutomationToken(context.Context, []byte) (Account, APIKey, error)
	CreateHostingCIRun(context.Context, string, string, string, api.HostingCIRunRequest) (api.HostingCIRun, error)
	GetHostingCIRun(context.Context, string, string, string) (api.HostingCIRun, error)
	CreateHostingCIPreview(context.Context, string, string, string) (api.HostingPreview, error)
}

func validHostingCredential(c api.HostingAutomationCredential) bool {
	minTTL, maxTTL := api.HostingAutomationTTLMin, api.HostingAutomationTTLMax
	if c.OIDCTrustID != "" {
		if uuid.Validate(c.OIDCTrustID) != nil {
			return false
		}
		minTTL, maxTTL = time.Second, api.HostingOIDCBearerTTL
	}
	return uuid.Validate(c.ID) == nil && uuid.Validate(c.AccountID) == nil && uuid.Validate(c.SiteID) == nil && len(c.Hash) == 32 && hosting.ValidHostingAutomationLabel(c.Label) && hosting.ValidHostingAutomationOperations(c.Operations) && !c.CreatedAt.IsZero() && c.ExpiresAt.Sub(c.CreatedAt) >= minTTL && c.ExpiresAt.Sub(c.CreatedAt) <= maxTTL
}
func cloneHostingCredential(c api.HostingAutomationCredential) api.HostingAutomationCredential {
	c.Hash = append([]byte(nil), c.Hash...)
	c.Operations = append([]string(nil), c.Operations...)
	return c
}
func hostingCredentialKey(c api.HostingAutomationCredential) APIKey {
	return APIKey{ID: c.ID, AccountID: c.AccountID, HostingSiteID: c.SiteID, HostingOIDCTrustID: c.OIDCTrustID, HostingOperations: append([]string(nil), c.Operations...), Scopes: []string{api.ScopeAppsRead, api.ScopeDeployWrite}, Status: string(APIKeyStatusActive)}
}
func ciPreview(run api.HostingCIRun, site api.HostingSite) api.HostingPreview {
	return api.HostingPreview{ID: run.PreviewID, AccountID: site.AccountID, SiteID: site.ID, ReleaseID: run.ReleaseID, OriginID: site.OriginID, Hostname: "p-" + run.PreviewID + site.Hostname[len(site.ID):], CreatedAt: run.CreatedAt, ExpiresAt: run.PreviewExpiresAt}
}
func newCIRun(id, site, release, fingerprint string, ttl int64) api.HostingCIRun {
	now := time.Now().UTC()
	return api.HostingCIRun{ID: id, SiteID: site, ReleaseID: release, PreviewID: uuid.NewString(), Fingerprint: fingerprint, CreatedAt: now, PreviewExpiresAt: now.Add(time.Duration(ttl) * time.Second)}
}
