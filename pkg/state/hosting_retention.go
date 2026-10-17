package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

// Separate from the broad Store interface: older/fake stores fail closed.
type HostingRetentionStore interface {
	GetHostingCleanupPlan(context.Context, string, string, *api.HostingRetentionRules, time.Time) (api.HostingCleanupPlan, error)
	SetHostingRetentionPolicy(context.Context, string, string, api.SetHostingRetentionRequest) (api.HostingRetentionPolicy, error)
	ApplyHostingRetention(context.Context, string, string, string, time.Time) ([]string, error)
	ListHostingRetentionSites(context.Context, string, int) ([]api.HostingSite, error)
	TouchHostingRelease(context.Context, string, string, string) (api.HostingRelease, error)
}

func defaultHostingRetentionPolicy(site string) api.HostingRetentionPolicy {
	return api.HostingRetentionPolicy{SiteID: site, HostingRetentionRules: hosting.DefaultRetentionRules()}
}
func cloneHostingRetentionPolicy(p api.HostingRetentionPolicy) api.HostingRetentionPolicy {
	if p.UpdatedAt != nil {
		at := *p.UpdatedAt
		p.UpdatedAt = &at
	}
	return p
}
func validHostingRetentionRequest(req api.SetHostingRetentionRequest) bool {
	return req.ExpectedRevision >= 0 && hosting.ValidRetentionRules(req.HostingRetentionRules)
}
