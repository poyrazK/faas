// Bounded admission for the gateway_request_duration_by_deployment_seconds
// histogram's deployment_id label (ADR-127 §Decision 4, Debugger
// UX v1). The dashboard's per-deployment latency drill-down
// (issue #273 / ADR-042) needs the deployment id as a label, but
// a deployment_id is a per-app runtime value — unbounded
// admission would explode the per-app series set past the
// gateway's TSDB budget.
//
// Behavioural contract (mirrors pkg/gateway/account_label_set.go):
//
//   - reserved labels ("", "__other__") are pre-admitted at
//     construction without consuming capacity (per app, since
//     the cap is per-app);
//   - empty input normalises to "" (pre-PR-B legacy
//     single-targetSet behaviour, per PR-B's Target.DeploymentID
//     documentation at handler.go:404-411);
//   - real ids are admitted up to `cap(app.Plan) -
//     reservedDeploymentCnt` (per-app cap varies by plan:
//     Free=0, Hobby=10, Pro=50, Scale=200 — see
//     pkg/api/limits.go:1249-1260);
//   - overflow collapses to "__other__" without ever inserting
//     into the admitted map (so the map never resizes past cap);
//   - the map is non-evicting (deliberately a plain map+mutex,
//     not an LRU) — daemon restart is the only path that resets
//     it. An evicting LRU would let evicted deployments
//     re-admit later and grow the per-app series set unbounded
//     over the daemon's lifetime.
//
// Per-app cap, not global cap: a Hobby customer's 10-dep cap
// should not consume budget from a Scale customer's 200-dep
// cap. The capByApp map caches the lookup so subsequent admits
// on the same app don't re-touch api.MustLimitsFor.
//
// The Prometheus observe happens at the call site AFTER
// admit() returns so it is outside the critical section.
package gateway

import (
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// Reserved label values. emptyDeploymentLabel handles the
// legacy single-targetSet behaviour (Target.DeploymentID == "");
// otherDeploymentLabel is the overflow placeholder (literal
// "__other__") that the §12 dashboard panel recognises.
const (
	emptyDeploymentLabel  = ""
	otherDeploymentLabel  = "__other__"
	reservedDeploymentCnt = 2
)

// deploymentLabelSet is the bounded admission set behind the
// deployment_id-labelled histogram in this package's Metrics
// bundle. Reserved values are pre-admitted at construction;
// real ids consume capacity once and are never evicted in
// process. capByApp caches the per-plan limit on first sight
// so subsequent admits on the same app don't re-touch the
// limit table.
//
// Pointer-receiver methods because the type contains a
// sync.Mutex — copying the value would duplicate the lock
// (govet copylocks). Constructed once per *Metrics in NewMetrics
// and held as a pointer field.
type deploymentLabelSet struct {
	mu       sync.Mutex
	capByApp map[string]int                 // appID → plan cap (cached on first sight)
	admitted map[string]map[string]struct{} // appID → deploymentID → {}
}

// newDeploymentLabelSet constructs a fresh admission set with
// empty capByApp + admitted maps. The first admit() on a given
// app id triggers the api.MustLimitsFor lookup and seeds the
// per-app sub-map; subsequent admits are O(1) hits.
func newDeploymentLabelSet() *deploymentLabelSet {
	return &deploymentLabelSet{
		capByApp: make(map[string]int),
		admitted: make(map[string]map[string]struct{}),
	}
}

// admit resolves a (app id, plan, deployment id) triple to a
// label value. Empty deployment input normalises to
// emptyDeploymentLabel (does NOT consume capacity — the
// legacy "" sentinel can be reused indefinitely). Reserved
// values (emptyDeploymentLabel, otherDeploymentLabel) are
// always admitted without consuming capacity. Real ids are
// admitted up to the plan-specific cap; further ids collapse
// to otherDeploymentLabel without ever consuming capacity, and
// the underlying map is never resized past cap.
//
// plan is the customer's plan string at observation time; the
// cap is cached on first sight so the limit table is touched
// once per app id per daemon lifetime.
//
// Concurrency: holds mu across the lookup+insert. Hot path is
// the "already admitted" lookup, which is O(1) and never
// inserts. The Prometheus observe at the call site happens
// AFTER admit returns, so it is outside the critical section.
func (s *deploymentLabelSet) admit(appID string, plan api.Plan, deploymentID string) string {
	switch deploymentID {
	case "":
		return emptyDeploymentLabel
	case otherDeploymentLabel:
		return otherDeploymentLabel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cap, ok := s.capByApp[appID]
	if !ok {
		// First sight of this app id — seed both the cap and
		// the admitted map. Free plan's DebugTelemetryDeploymentsPerApp
		// is 0 (limits.go:1659), so any real deployment id
		// collapses to __other__ for free customers.
		cap = api.MustLimitsFor(plan).DebugTelemetryDeploymentsPerApp
		s.capByApp[appID] = cap
		appMap := make(map[string]struct{}, cap+reservedDeploymentCnt)
		appMap[emptyDeploymentLabel] = struct{}{}
		appMap[otherDeploymentLabel] = struct{}{}
		s.admitted[appID] = appMap
	}
	appMap := s.admitted[appID]
	if _, ok := appMap[deploymentID]; ok {
		return deploymentID
	}
	// Real-id budget is exactly `cap - reservedCount`, not
	// `cap - reservedCount - 2`. Without the subtraction the
	// reserved labels steal two slots from the real-id budget.
	if len(appMap)-reservedDeploymentCnt >= cap {
		return otherDeploymentLabel
	}
	appMap[deploymentID] = struct{}{}
	return deploymentID
}
