// adr: 20261104141122
package imaged

import (
	"maps"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// stampPostgresPool tells a released account's app how many database
// connections one instance can use (ADR-20261104141122). A value the image
// already sets is kept, and the app's own environment overrides the stamp in
// the guest. Fixed at build time: a changed plan or scaling policy needs a
// deploy, like FAAS_FUNCTION_CONCURRENCY.
func (h *Handler) stampPostgresPool(manifest api.AppManifest, app state.App, acct state.Account) api.AppManifest {
	if h.postgresPoolSizingReleased == nil || !h.postgresPoolSizingReleased(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID}) {
		return manifest
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if _, set := manifest.Env[api.PostgresPoolMaxEnv]; !ok || set {
		return manifest
	}
	requested := 0
	if app.ScalingPolicy != nil {
		requested = app.ScalingPolicy.PerInstanceConcurrency
	}
	size := limits.PostgresPoolMax(requested)
	if size < 1 {
		return manifest
	}
	env := maps.Clone(manifest.Env)
	if env == nil {
		env = map[string]string{}
	}
	env[api.PostgresPoolMaxEnv] = strconv.Itoa(size)
	manifest.Env = env
	return manifest
}
