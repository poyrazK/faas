package state

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type HostingPreviewStore interface {
	CreateHostingPreview(context.Context, api.HostingPreview) (api.HostingPreview, error)
	ListHostingPreviews(context.Context, string, string) ([]api.HostingPreview, error)
	RevokeHostingPreview(context.Context, string, string, string, int64) (api.HostingPreview, error)
	DueHostingPreviews(context.Context, time.Time) ([]string, error)
	PrepareHostingPreview(context.Context, string, time.Time) (api.HostingPreview, error)
	FinishHostingPreview(context.Context, api.HostingPreview, time.Time, string) error
	PurgeHostingPreview(context.Context, api.HostingPreview, time.Time) error
}

func validHostingPreview(p api.HostingPreview) bool {
	return uuid.Validate(p.ID) == nil && uuid.Validate(p.AccountID) == nil && uuid.Validate(p.SiteID) == nil && uuid.Validate(p.ReleaseID) == nil && hosting.ValidDigest(p.OriginID) && strings.HasPrefix(p.Hostname, "p-"+p.ID+".") && len(p.Hostname) <= 253 && !p.CreatedAt.IsZero() && p.ExpiresAt.Sub(p.CreatedAt) >= api.HostingPreviewTTLMin && p.ExpiresAt.Sub(p.CreatedAt) <= api.HostingPreviewTTLMax
}
func cloneHostingPreview(p api.HostingPreview) api.HostingPreview {
	if p.PublishedAt != nil {
		at := *p.PublishedAt
		p.PublishedAt = &at
	}
	return p
}
func hostingPreviewNext(p api.HostingPreview, now time.Time, code string) time.Time {
	if code != "" {
		return now.Add(api.HostingPreviewRetry)
	}
	if p.Revoked {
		return now.Add(api.HostingPreviewRemovalGrace)
	}
	next := now.Add(api.HostingPreviewRepairInterval)
	if p.ExpiresAt.Before(next) {
		next = p.ExpiresAt
	}
	return next
}
func validHostingPreviewCode(code string) bool {
	return code == "" || code == api.HostingRouteErrorOrigin || code == api.HostingRouteErrorProvider
}
