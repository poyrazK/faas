// adr: 570
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublicRoutingViewKey names one app's ordinary public routing view
// (ADR-20261027085556): the owner a request verifies and the scope it routes
// in. ProjectID is "" for an app outside a project.
type PublicRoutingViewKey struct{ AppID, AccountID, ProjectID, Scope string }

// PublicRoutingView is what a public request needs from the store, as
// PublicRoutingPolicyReader would read it in one snapshot: the roster and
// release graph of an ordinary request, and what host, revision and release
// pins check (ADR-20261028095914). A view exists only for a verified owner.
type PublicRoutingView struct {
	Weights []ServiceDeploymentWeight
	// Lane is the scope a production-lane roster came from, as
	// PublicProductionDeploymentWeights reports it; "" for a scoped roster.
	Lane string
	// Release is the project's release graph; nil outside a project.
	Release *PublicRoutingReleaseView
	// Live is every live deployment of the app, in every scope. LiveComplete
	// is false when api.TrafficPublicRoutingMaxLiveDeployments cut the list.
	Live         []PublicRoutingLiveDeployment
	LiveComplete bool
	// RevisionPins is the app's revision_pin_ttl_seconds > 0.
	// RevisionPinsKnown is false when the stored value is not an integer.
	RevisionPins, RevisionPinsKnown bool
}

// PublicRoutingLiveDeployment is one live deployment as host and revision
// pins check it. PinExpiresAt is zero without a revision pin.
type PublicRoutingLiveDeployment struct {
	ID, Scope      string
	TrafficPercent int
	PinExpiresAt   time.Time
}

func (v PublicRoutingView) live(deployment string) (PublicRoutingLiveDeployment, bool) {
	for _, d := range v.Live {
		if d.ID == deployment {
			return d, true
		}
	}
	return PublicRoutingLiveDeployment{}, false
}

// HostPinAllowed answers PublicHostPinAllowed for a deployment-preview or
// named-environment host. ok is false when the view cannot answer.
func (v PublicRoutingView) HostPinAllowed(scope, deployment string) (allowed, ok bool) {
	d, found := v.live(deployment)
	if !found {
		return false, v.LiveComplete
	}
	return d.Scope == normalizedDeploymentScope(scope), true
}

// RevisionAllowed answers PublicRevisionAllowed at now. ok is false when the
// view cannot answer.
func (v PublicRoutingView) RevisionAllowed(scope, deployment string, now time.Time) (allowed, ok bool) {
	if !v.RevisionPinsKnown {
		return false, false
	}
	d, found := v.live(deployment)
	if !found {
		return false, v.LiveComplete
	}
	return v.RevisionPins && d.Scope == normalizedDeploymentScope(scope) && (d.TrafficPercent > 0 || d.PinExpiresAt.After(now)), true
}

// PublicRoutingReleaseView holds ResolvePublicProjectCanary's inputs for an
// unpinned request, so the per-request canary split stays per request.
type PublicRoutingReleaseView struct {
	// Active is ResolvePublicProjectRelease with no requested graph.
	Active PublicRoutingReleaseTarget
	// Canary is the project's canary when the scope is production.
	Canary *PublicRoutingCanaryView
	// Graphs are the scope's graphs a release pin can name. GraphsComplete
	// is false when api.TrafficPublicRoutingMaxReleaseGraphs cut the list.
	Graphs         []PublicRoutingReleaseGraph
	GraphsComplete bool
}

// PublicRoutingReleaseGraph is one graph a release pin can name, with what
// ResolvePublicProjectRelease answers for it while it is usable.
type PublicRoutingReleaseGraph struct {
	ID        string
	Active    bool
	ExpiresAt time.Time
	Target    PublicRoutingReleaseTarget
}

// Requested answers ResolvePublicProjectRelease for a release pin at now.
// ok is false when the view cannot answer.
func (v *PublicRoutingReleaseView) Requested(requested string, now time.Time) (target PublicRoutingReleaseTarget, ok bool) {
	if v == nil {
		return PublicRoutingReleaseTarget{Err: ErrNotFound}, true
	}
	for _, graph := range v.Graphs {
		if graph.ID == requested {
			if graph.Active || graph.ExpiresAt.After(now) {
				return graph.Target, true
			}
			return PublicRoutingReleaseTarget{Err: ErrNotFound}, true
		}
	}
	return PublicRoutingReleaseTarget{Err: ErrNotFound}, v.GraphsComplete
}

// PublicRoutingReleaseTarget is one ResolvePublicProjectRelease answer.
// Err is nil, ErrNotFound (the graph is gone) or ErrConflict.
type PublicRoutingReleaseTarget struct {
	ID, DeploymentID string
	Err              error
}

type PublicRoutingCanaryView struct {
	Policy ProjectCanaryPolicyState
	// ActiveReleaseID is the project's active production graph.
	ActiveReleaseID string
	// Targets resolves the canary's baseline and candidate graphs.
	Targets map[string]PublicRoutingReleaseTarget
}

// Resolve answers ResolvePublicProjectCanary for one request: key splits a
// running canary and now bounds its deadline, as in the per-request read.
func (v *PublicRoutingReleaseView) Resolve(key string, now time.Time) (string, string, error) {
	if v == nil {
		return "", "", errors.New("public routing view has no release graph")
	}
	target := v.Active
	if v.Canary != nil {
		requested, err := selectOwnedCanaryAt(v.Canary.Policy, v.Canary.ActiveReleaseID, key, now)
		if err != nil {
			return "", "", err
		}
		if requested != "" {
			var ok bool
			if target, ok = v.Canary.Targets[requested]; !ok {
				return "", "", errors.New("public routing view lacks the selected canary graph")
			}
		}
	}
	return target.ID, target.DeploymentID, target.Err
}

// PublicRoutingViewResult is one key's answer. Err is ErrNotFound when the
// owner is not verified, or an answer the per-request read would refuse.
type PublicRoutingViewResult struct {
	View PublicRoutingView
	Err  error
}

type PublicRoutingViewStore interface {
	ReadPublicRoutingViews(context.Context, []PublicRoutingViewKey) (map[PublicRoutingViewKey]PublicRoutingViewResult, error)
}

var _ PublicRoutingViewStore = (*PgStore)(nil)

// ReadPublicRoutingViews reads every key in one read-only repeatable-read
// transaction with five queries, whatever the number of keys. An error is a
// store failure; per-key refusals are in the results.
func (s *PgStore) ReadPublicRoutingViews(ctx context.Context, keys []PublicRoutingViewKey) (map[PublicRoutingViewKey]PublicRoutingViewResult, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("public routing view store is unavailable")
	}
	results := make(map[PublicRoutingViewKey]PublicRoutingViewResult, len(keys))
	if len(keys) == 0 {
		return results, nil
	}
	ids := make([]pgtype.UUID, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if !seen[key.AppID] {
			seen[key.AppID] = true
			ids = append(ids, uuidToPgtype(key.AppID))
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin public routing view: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := readPublicRoutingViewRows(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit public routing view: %w", err)
	}
	for _, key := range keys {
		results[key] = rows.result(key)
	}
	return results, nil
}

type publicRoutingViewRows struct {
	owners   map[string]sqlc.ReadPublicRoutingViewOwnersRow
	weights  map[string][]sqlc.ReadPublicRoutingViewWeightsRow
	canaries map[string]sqlc.ReadPublicRoutingViewCanariesRow
	releases map[string][]sqlc.ReadPublicRoutingViewReleasesRow
	live     map[string][]sqlc.ReadPublicRoutingViewLiveRow
}

func readPublicRoutingViewRows(ctx context.Context, tx pgx.Tx, ids []pgtype.UUID) (publicRoutingViewRows, error) {
	q := sqlc.New()
	view := publicRoutingViewRows{owners: map[string]sqlc.ReadPublicRoutingViewOwnersRow{},
		weights: map[string][]sqlc.ReadPublicRoutingViewWeightsRow{}, canaries: map[string]sqlc.ReadPublicRoutingViewCanariesRow{},
		releases: map[string][]sqlc.ReadPublicRoutingViewReleasesRow{}, live: map[string][]sqlc.ReadPublicRoutingViewLiveRow{}}
	owners, err := q.ReadPublicRoutingViewOwners(ctx, tx, ids)
	if err != nil {
		return view, fmt.Errorf("read public routing view owners: %w", err)
	}
	for _, row := range owners {
		view.owners[pgUUIDString(row.ID)] = row
	}
	weights, err := q.ReadPublicRoutingViewWeights(ctx, tx, sqlc.ReadPublicRoutingViewWeightsParams{AppIds: ids,
		RowLimit: int32(api.TrafficPolicyMaxDeployments + 1)})
	if err != nil {
		return view, fmt.Errorf("read public routing view weights: %w", err)
	}
	for _, row := range weights {
		app := pgUUIDString(row.AppID)
		view.weights[app] = append(view.weights[app], row)
	}
	canaries, err := q.ReadPublicRoutingViewCanaries(ctx, tx, ids)
	if err != nil {
		return view, fmt.Errorf("read public routing view canaries: %w", err)
	}
	var named []pgtype.UUID
	for _, row := range canaries {
		view.canaries[pgUUIDString(row.AppID)] = row
		var policy ProjectCanaryPolicyState
		if json.Unmarshal(row.Policy, &policy) == nil {
			named = append(named, uuidToPgtype(policy.BaselineReleaseID), uuidToPgtype(policy.CandidateReleaseID))
		}
	}
	releases, err := q.ReadPublicRoutingViewReleases(ctx, tx, sqlc.ReadPublicRoutingViewReleasesParams{AppIds: ids, ReleaseIds: named,
		RowLimit: int32(api.TrafficPublicRoutingMaxReleaseGraphs + 1)})
	if err != nil {
		return view, fmt.Errorf("read public routing view releases: %w", err)
	}
	for _, row := range releases {
		app := pgUUIDString(row.AppID)
		view.releases[app] = append(view.releases[app], row)
	}
	live, err := q.ReadPublicRoutingViewLive(ctx, tx, sqlc.ReadPublicRoutingViewLiveParams{AppIds: ids,
		RowLimit: int32(api.TrafficPublicRoutingMaxLiveDeployments + 1)})
	if err != nil {
		return view, fmt.Errorf("read public routing view live deployments: %w", err)
	}
	for _, row := range live {
		app := pgUUIDString(row.AppID)
		view.live[app] = append(view.live[app], row)
	}
	return view, nil
}

func (v publicRoutingViewRows) result(key PublicRoutingViewKey) PublicRoutingViewResult {
	owner, ok := v.owners[key.AppID]
	if !ok || pgUUIDString(owner.AccountID) != key.AccountID || pgUUIDString(owner.ProjectID) != key.ProjectID {
		return PublicRoutingViewResult{Err: ErrNotFound}
	}
	view := PublicRoutingView{RevisionPins: owner.RevisionPins, RevisionPinsKnown: owner.RevisionPinTtlValid}
	var err error
	if view.Weights, view.Lane, err = v.roster(key); err != nil {
		return PublicRoutingViewResult{Err: err}
	}
	view.Live, view.LiveComplete = v.liveDeployments(key.AppID)
	if key.ProjectID != "" {
		if view.Release, err = v.release(key); err != nil {
			return PublicRoutingViewResult{Err: err}
		}
	}
	return PublicRoutingViewResult{View: view}
}

// roster follows PublicProductionDeploymentWeights for a production scope and
// PublicDeploymentWeights otherwise, including their bound.
func (v publicRoutingViewRows) roster(key PublicRoutingViewKey) ([]ServiceDeploymentWeight, string, error) {
	scope := normalizedDeploymentScope(key.Scope)
	rows := v.weights[key.AppID]
	if IsProductionRoutingScope(key.Scope) {
		scope = DefaultEnvScope
		for _, row := range rows {
			if row.Scope == "production" {
				scope = "production"
				break
			}
		}
	}
	weights := make([]ServiceDeploymentWeight, 0, len(rows))
	for _, row := range rows {
		if row.Scope == scope {
			weights = append(weights, ServiceDeploymentWeight{ID: pgUUIDString(row.ID), TrafficPercent: int(row.TrafficPercent)})
		}
	}
	if len(weights) > api.TrafficPolicyMaxDeployments {
		return nil, "", errors.New("public routing deployment limit exceeded")
	}
	if !IsProductionRoutingScope(key.Scope) || len(weights) == 0 {
		return weights, "", nil
	}
	return weights, scope, nil
}

// release resolves the graphs ResolvePublicProjectCanary can select for an
// unpinned request: the scope's active graph and, in production, the
// project canary's baseline and candidate.
func (v publicRoutingViewRows) release(key PublicRoutingViewKey) (*PublicRoutingReleaseView, error) {
	scope := normalizedDeploymentScope(key.Scope)
	view := &PublicRoutingReleaseView{Active: v.releaseTarget(key.AppID, scope, ""),
		GraphsComplete: len(v.releases[key.AppID]) <= api.TrafficPublicRoutingMaxReleaseGraphs}
	seen := map[string]bool{}
	for _, row := range v.releases[key.AppID] {
		id := pgUUIDString(row.ID)
		if row.EnvironmentSlug != scope || seen[id] {
			continue
		}
		seen[id] = true
		view.Graphs = append(view.Graphs, PublicRoutingReleaseGraph{ID: id, Active: row.Active,
			ExpiresAt: row.ExpiresAt.Time, Target: v.releaseTarget(key.AppID, scope, id)})
	}
	canary, ok := v.canaries[key.AppID]
	if !ok || scope != "production" {
		return view, nil
	}
	var policy ProjectCanaryPolicyState
	if err := json.Unmarshal(canary.Policy, &policy); err != nil {
		return nil, fmt.Errorf("decode public routing canary: %w", err)
	}
	view.Canary = &PublicRoutingCanaryView{Policy: policy, ActiveReleaseID: canary.ActiveReleaseID, Targets: map[string]PublicRoutingReleaseTarget{}}
	for _, id := range []string{policy.BaselineReleaseID, policy.CandidateReleaseID} {
		if id != "" {
			view.Canary.Targets[id] = v.releaseTarget(key.AppID, scope, id)
		}
	}
	return view, nil
}

// liveDeployments keeps at most api.TrafficPublicRoutingMaxLiveDeployments
// and reports whether that was all of them.
func (v publicRoutingViewRows) liveDeployments(app string) ([]PublicRoutingLiveDeployment, bool) {
	rows := v.live[app]
	complete := len(rows) <= api.TrafficPublicRoutingMaxLiveDeployments
	if !complete {
		rows = rows[:api.TrafficPublicRoutingMaxLiveDeployments]
	}
	out := make([]PublicRoutingLiveDeployment, 0, len(rows))
	for _, row := range rows {
		d := PublicRoutingLiveDeployment{ID: pgUUIDString(row.ID), Scope: row.Scope, TrafficPercent: int(row.TrafficPercent)}
		if row.PinExpiresAt.Valid {
			d.PinExpiresAt = row.PinExpiresAt.Time
		}
		out = append(out, d)
	}
	return out, complete
}

// releaseTarget mirrors ResolvePublicProjectRelease over the view's rows.
func (v publicRoutingViewRows) releaseTarget(app, scope, requested string) PublicRoutingReleaseTarget {
	var rows []sqlc.ReadPublicRoutingViewReleasesRow
	for _, row := range v.releases[app] {
		if row.EnvironmentSlug == scope && (requested == "" && row.Active || requested != "" && pgUUIDString(row.ID) == requested) {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		if requested != "" {
			return PublicRoutingReleaseTarget{Err: ErrNotFound}
		}
		return PublicRoutingReleaseTarget{}
	}
	if len(rows) != 1 || !rows[0].DeploymentID.Valid || !rows[0].TargetLive {
		return PublicRoutingReleaseTarget{Err: ErrConflict}
	}
	return PublicRoutingReleaseTarget{ID: pgUUIDString(rows[0].ID), DeploymentID: pgUUIDString(rows[0].DeploymentID)}
}
