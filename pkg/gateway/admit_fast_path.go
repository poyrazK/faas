package gateway

import "github.com/onebox-faas/faas/pkg/sched"

// skipsAdmitFastPath reports whether an admission must reach schedd even when
// the gateway's cached targets already fill the app's concurrency budget.
//
// A deployment's post-readiness smoke must: the cached targets can belong to
// an older release at 0% traffic, and only schedd can park that idle sibling
// to free the slot (parkZeroTrafficSibling). Answering at capacity locally
// meant schedd never saw the smoke; it was refused until the old instance
// idled out, past the verification window, and the deploy failed as
// deployment_verification_unavailable (prod 2026-11-23: 1 in 4 back-to-back
// redeploys of a max_concurrency=1 MCP app; ADR-20261123120000).
func skipsAdmitFastPath(trigger string) bool {
	return trigger == sched.TriggerDeploymentSmoke
}
