package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cancellation is terminal intent; reads and identical CI admission retries retain it.
type HostingCancellationStore interface {
	CancelHostingCIRun(context.Context, string, string, string) (api.HostingCIRun, error)
}

var ErrHostingCancellationProtected = fmt.Errorf("production releases and other preview references protect CI runs: %w", ErrConflict)

func hostingCancellationProtected(site api.HostingSite, run api.HostingCIRun, release *api.HostingRelease, previews []api.HostingPreview) bool {
	if site.ActiveReleaseID == run.ReleaseID || release != nil && release.FirstPromotedAt != nil {
		return true
	}
	for _, p := range previews {
		if p.ID == run.PreviewID && p.ReleaseID != run.ReleaseID {
			return true
		}
		if p.ReleaseID == run.ReleaseID && p.ID != run.PreviewID {
			return true
		}
	}
	return false
}
