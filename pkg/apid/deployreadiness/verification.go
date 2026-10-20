package deployreadiness

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// VerificationPolicy is shared with atomic release admission. It only reads
// route policy; a passing result never asserts application runtime health.
func VerificationPolicy(ctx context.Context, store Reader, accountID string, release state.ProjectReleaseSet) (*api.ProjectReleaseVerification, *api.Problem) {
	if time.Duration(release.TTLSeconds)*time.Second < api.ProjectReleaseVerificationMinRetention {
		return nil, api.ErrValidation("verified releases require at least 15 minutes of baseline retention")
	}
	health, ok := store.(state.RouteHealthStore)
	if !ok {
		return nil, api.ErrCapacity("route probe policy is unavailable")
	}
	verification := &api.ProjectReleaseVerification{}
	for _, member := range release.Members {
		app, err := store.AppByID(ctx, member.AppID)
		if err != nil || app.AccountID != accountID {
			return nil, api.ErrValidation("verification workload changed")
		}
		if app.Manifest.ExecutionMode == api.ExecutionModeWorker {
			continue
		}
		gate, err := health.GetRouteHealthGate(ctx, accountID, app.ID)
		if err != nil {
			return nil, api.ErrCapacity("verification route policy could not be read")
		}
		for _, route := range gate.Routes {
			if route.Probe != nil {
				verification.Probes = append(verification.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: route.Method, Path: route.Probe.Path})
			}
		}
	}
	if _, ok := store.(state.ProjectReleaseVerificationStore); !ok {
		return nil, api.ErrCapacity("project verification orchestration is unavailable")
	}
	if len(verification.Probes) > api.ProjectReleaseVerificationMaxProbes {
		return nil, api.ErrValidation("verified project releases support at most five probe routes")
	}
	if len(verification.Probes) == 0 {
		return nil, api.ErrValidation("--verify requires at least one configured GET/HEAD route-health probe")
	}
	sort.Slice(verification.Probes, func(i, j int) bool {
		a, b := verification.Probes[i], verification.Probes[j]
		return a.AppID+a.Method+a.Path < b.AppID+b.Method+b.Path
	})
	return verification, nil
}
