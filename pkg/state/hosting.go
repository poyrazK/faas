package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

var ErrHostingQuota = errors.New("internal hosting quota exhausted")

type HostingQuotaError struct {
	Limit, Observed int64
	Resource        string
}

func (e *HostingQuotaError) Error() string { return ErrHostingQuota.Error() }
func (e *HostingQuotaError) Unwrap() error { return ErrHostingQuota }
func hostingQuota(limit int) error {
	return &HostingQuotaError{Limit: int64(limit), Observed: int64(limit + 1)}
}

// HostingStore is deliberately separate from VM deployment intent. Static
// releases never enter instances, schedd, imaged, or snapshot accounting.
type HostingStore interface {
	GetHostingUsage(context.Context, string) (api.HostingUsage, error)
	CreateHostingSite(context.Context, api.HostingSite) (api.HostingSite, error)
	GetHostingSite(context.Context, string, string) (api.HostingSite, error)
	ListHostingSites(context.Context, string) ([]api.HostingSite, error)
	ListHostingRoutes(context.Context, string, int) ([]api.HostingSite, error)
	CreateHostingRelease(context.Context, string, string, api.HostingManifest) (api.HostingRelease, error)
	GetHostingRelease(context.Context, string, string, string) (api.HostingRelease, error)
	ListHostingReleases(context.Context, string, string) ([]api.HostingRelease, error)
	ReadyHostingRelease(context.Context, string, string, string) (api.HostingRelease, error)
	PromoteHostingRelease(context.Context, string, string, string, int64) (api.HostingSite, error)
	RetireHostingRelease(context.Context, string, string, string) (api.HostingRelease, error)
	DeleteHostingReleaseRecord(context.Context, string, string, string) error
	DeleteHostingSite(context.Context, string, string) (api.HostingSite, error)
	PurgeHostingSite(context.Context, string, string) error
	SetHostingSiteSuspended(context.Context, string, string, int64, bool) (api.HostingSite, error)
	ListDueHostingRoutePublications(context.Context, time.Time, int) ([]string, error)
	HostingRouteSnapshot(context.Context, string) (HostingRouteSnapshot, error)
	AcknowledgeHostingRoute(context.Context, string, int64, time.Time) error
	RetryHostingRoute(context.Context, string, int64, time.Time, string) error
	HostingRouteBacklog(context.Context) (HostingRouteBacklog, error)
}

// A site and its ready-release set must be read from the same transaction as
// their generation. The per-site row is a coalescing transactional outbox.
type HostingRouteSnapshot struct {
	Site                                       api.HostingSite
	Releases                                   []api.HostingRelease
	Generation                                 int64
	PublishedGeneration                        int64
	PublishedAt                                time.Time
	PendingSince, NextAttemptAt, LastAttemptAt time.Time
	RetryCount                                 int64
	LastErrorCode                              string
}

type HostingRouteBacklog struct {
	Pending         int64
	OldestPendingAt time.Time
}

type hostingPublication struct {
	Generation, PublishedGeneration int64
	PublishedAt, NextAttemptAt      time.Time
	PendingSince, LastAttemptAt     time.Time
	RetryCount                      int64
	LastErrorCode                   string
}

func validHostingRouteFailure(code string) bool {
	return code == api.HostingRouteErrorOrigin || code == api.HostingRouteErrorProvider || code == api.HostingRouteErrorStale
}

func hostingPublicationSnapshot(site api.HostingSite, p hostingPublication) HostingRouteSnapshot {
	return HostingRouteSnapshot{Site: site, Generation: p.Generation, PublishedGeneration: p.PublishedGeneration,
		PublishedAt: p.PublishedAt, PendingSince: p.PendingSince, NextAttemptAt: p.NextAttemptAt,
		LastAttemptAt: p.LastAttemptAt, RetryCount: p.RetryCount, LastErrorCode: p.LastErrorCode,
		Releases: []api.HostingRelease{}}
}

func validHostingSite(s api.HostingSite) bool {
	digest, err := hex.DecodeString(s.OriginID)
	return uuid.Validate(s.ID) == nil && uuid.Validate(s.AccountID) == nil && uuid.Validate(s.ProjectID) == nil && hosting.ValidName(s.Name) && hosting.ValidName(s.Environment) && len(s.Environment) <= 63 && strings.HasPrefix(s.Hostname, s.ID+".") && len(s.Hostname) <= 253 && err == nil && len(digest) == 32 && strings.ToLower(s.OriginID) == s.OriginID
}

func cloneHostingRelease(r api.HostingRelease) api.HostingRelease {
	r.Provenance = cloneHostingProvenance(r.Provenance)
	r.Manifest.Files = append([]api.HostingFile(nil), r.Manifest.Files...)
	if r.Manifest.Config != nil {
		cfg := *r.Manifest.Config
		cfg.Redirects = append([]api.HostingRedirect(nil), cfg.Redirects...)
		cfg.Headers = append([]api.HostingHeaderRule(nil), cfg.Headers...)
		for i, rule := range cfg.Headers {
			cfg.Headers[i].Values = make(map[string]string, len(rule.Values))
			for k, v := range rule.Values {
				cfg.Headers[i].Values[k] = v
			}
		}
		r.Manifest.Config = &cfg
	}
	if r.FirstPromotedAt != nil {
		at := *r.FirstPromotedAt
		r.FirstPromotedAt = &at
	}
	if r.RetiredAt != nil {
		t := *r.RetiredAt
		r.RetiredAt = &t
	}
	if r.RetirementPublishedAt != nil {
		t := *r.RetirementPublishedAt
		r.RetirementPublishedAt = &t
	}
	return r
}

func validateHostingManifest(m api.HostingManifest) error {
	data, err := json.Marshal(m)
	if err != nil || int64(len(data)) > api.HostingManifestMaxBytes || hosting.ValidateManifest(m) != nil {
		return ErrInvalidArgument
	}
	return nil
}
