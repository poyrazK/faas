package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

var ErrHostingQuota = errors.New("internal hosting quota exhausted")

type HostingQuotaError struct{ Limit, Observed int64 }

func (e *HostingQuotaError) Error() string { return ErrHostingQuota.Error() }
func (e *HostingQuotaError) Unwrap() error { return ErrHostingQuota }
func hostingQuota(limit int) error {
	return &HostingQuotaError{Limit: int64(limit), Observed: int64(limit + 1)}
}

// HostingStore is deliberately separate from VM deployment intent. Static
// releases never enter instances, schedd, imaged, or snapshot accounting.
type HostingStore interface {
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
}

func validHostingSite(s api.HostingSite) bool {
	digest, err := hex.DecodeString(s.OriginID)
	return uuid.Validate(s.ID) == nil && uuid.Validate(s.AccountID) == nil && uuid.Validate(s.ProjectID) == nil && hosting.ValidName(s.Name) && hosting.ValidName(s.Environment) && len(s.Environment) <= 63 && strings.HasPrefix(s.Hostname, s.ID+".") && len(s.Hostname) <= 253 && err == nil && len(digest) == 32 && strings.ToLower(s.OriginID) == s.OriginID
}

func cloneHostingRelease(r api.HostingRelease) api.HostingRelease {
	r.Manifest.Files = append([]api.HostingFile(nil), r.Manifest.Files...)
	if r.RetiredAt != nil {
		t := *r.RetiredAt
		r.RetiredAt = &t
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
