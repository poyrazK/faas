package openapidiff

// The contract gate is deliberately kept in this package rather than in a
// daemon. It gives apid (read-only preview and rollback), imaged (the actual
// live transition), and tests one deterministic comparison path.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	// ErrSnapshotStoreUnavailable means the configured state store cannot
	// read contract snapshots. Once the flag is enabled, callers should fail
	// closed rather than silently claiming that a promotion was checked.
	ErrSnapshotStoreUnavailable = errors.New("openapidiff: contract snapshot store unavailable")
	// ErrSnapshotBaselineMissing is informational: the first deployment in a
	// scope has no baseline and is allowed to establish one.
	ErrSnapshotBaselineMissing = errors.New("openapidiff: contract snapshot baseline missing")
)

// SnapshotDiff is the stable, daemon-neutral result of comparing two
// canonical snapshots. Breaks are blocking; additions are explanatory.
type SnapshotDiff struct {
	BaselineSHA256 string
	ProposedSHA256 string
	Breaks         []SchemaBreak
	Additions      []AdditiveChange
}

// PromotionCheck carries both the comparison and the snapshot metadata used
// by the HTTP preview. HasBaseline is false for the first deployment in a
// scope; that case is allowed but still returns the proposed hash.
type PromotionCheck struct {
	Diff        SnapshotDiff
	Baseline    state.OpenAPISnapshot
	Proposed    state.OpenAPISnapshot
	HasBaseline bool
}

// GateError is returned when the proposed contract contains one or more
// structural breaks. The message is deterministic so deployment errors and
// audit records are useful without exposing the entire snapshot document.
type GateError struct {
	Diff SnapshotDiff
}

func (e *GateError) Error() string {
	if e == nil || len(e.Diff.Breaks) == 0 {
		return "openapi contract gate: breaking change"
	}
	parts := make([]string, 0, len(e.Diff.Breaks))
	for _, b := range e.Diff.Breaks {
		anchor := strings.TrimSpace(strings.Join([]string{b.Method, b.Path, b.Status, b.PathInSchema}, " "))
		anchor = strings.Join(strings.Fields(anchor), " ")
		if anchor == "" {
			anchor = b.Path
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", anchor, b.Kind))
	}
	sort.Strings(parts)
	return fmt.Sprintf("openapi contract gate: %d breaking change(s): %s", len(parts), strings.Join(parts, "; "))
}

// CompareSnapshots validates and compares two canonical snapshot envelopes.
func CompareSnapshots(baseline, proposed json.RawMessage) (SnapshotDiff, error) {
	base, err := UnmarshalSnapshot(baseline)
	if err != nil {
		return SnapshotDiff{}, fmt.Errorf("openapidiff: decode baseline snapshot: %w", err)
	}
	prop, err := UnmarshalSnapshot(proposed)
	if err != nil {
		return SnapshotDiff{}, fmt.Errorf("openapidiff: decode proposed snapshot: %w", err)
	}
	_, baseSHA, err := MarshalSnapshot(base)
	if err != nil {
		return SnapshotDiff{}, fmt.Errorf("openapidiff: hash baseline snapshot: %w", err)
	}
	_, propSHA, err := MarshalSnapshot(prop)
	if err != nil {
		return SnapshotDiff{}, fmt.Errorf("openapidiff: hash proposed snapshot: %w", err)
	}
	return SnapshotDiff{
		BaselineSHA256: baseSHA,
		ProposedSHA256: propSHA,
		Breaks:         Compare(base, prop),
		Additions:      CompareAdditive(base, prop),
	}, nil
}

// SnapshotFromEdgeRules projects the enabled app edge rules into the same
// canonical snapshot written by MarkDeploymentLive. Keeping this helper
// public lets the pre-live gate use exactly the producer's projection rather
// than maintaining a second route-to-schema implementation.
func SnapshotFromEdgeRules(deploymentID, appID, scope string, rules []state.EdgeRule) (state.OpenAPISnapshot, error) {
	pending := make([]api.CreateEdgeRuleRequest, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		action, err := json.Marshal(rule.Action)
		if err != nil {
			return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: encode edge rule %s: %w", rule.ID, err)
		}
		priority := rule.Priority
		enabled := rule.Enabled
		pending = append(pending, api.CreateEdgeRuleRequest{
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
			MatchMethods: append([]string(nil), rule.MatchMethods...),
			Priority:     &priority, Enabled: &enabled, Kind: string(rule.Kind),
			ValidateMode: rule.ValidateMode, Action: action,
		})
	}
	spec, err := GenerateFromEdgeRules(nil, nil, pending)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: project snapshot: %w", err)
	}
	raw, sha, err := MarshalSnapshot(spec)
	if err != nil {
		return state.OpenAPISnapshot{}, fmt.Errorf("openapidiff: marshal snapshot: %w", err)
	}
	return state.OpenAPISnapshot{
		DeploymentID: deploymentID, AppID: appID, Scope: scope,
		Snapshot: raw, SHA256: sha, SchemaVersion: SnapshotSchemaVersion,
	}, nil
}

// CheckPromotion builds the proposed contract for an app and compares it to
// the latest captured snapshot in the target scope. A missing baseline is
// explicitly allowed and returned as ErrSnapshotBaselineMissing; all other
// errors are actionable failures when the gate is enabled.
func CheckPromotion(ctx context.Context, store state.Store, appID, deploymentID, scope string) (PromotionCheck, error) {
	snapshots, ok := store.(state.OpenAPISnapshotStore)
	if !ok {
		return PromotionCheck{}, ErrSnapshotStoreUnavailable
	}
	rules, err := store.ListEdgeRulesForApp(ctx, appID)
	if err != nil {
		return PromotionCheck{}, fmt.Errorf("openapidiff: read edge rules: %w", err)
	}
	proposed, err := SnapshotFromEdgeRules(deploymentID, appID, scope, rules)
	if err != nil {
		return PromotionCheck{}, err
	}
	return checkSnapshotPromotion(ctx, snapshots, appID, scope, proposed)
}

// CheckDeploymentPromotion compares an already-captured deployment snapshot
// with the current live baseline. Rollbacks use this path: rebuilding the
// proposal from the app's current edge rules would compare the app to itself
// and could never detect a rollback that removes a currently-live field.
// Deployments created before snapshot capture are allowed to fall back to the
// projection path; the caller still gets the same first-baseline semantics.
func CheckDeploymentPromotion(ctx context.Context, store state.Store, appID, deploymentID, scope string) (PromotionCheck, error) {
	snapshots, ok := store.(state.OpenAPISnapshotStore)
	if !ok {
		return PromotionCheck{}, ErrSnapshotStoreUnavailable
	}
	proposed, err := snapshots.OpenAPISnapshotByDeployment(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return PromotionCheck{}, fmt.Errorf("openapidiff: read proposed snapshot: %w", err)
		}
		return CheckPromotion(ctx, store, appID, deploymentID, scope)
	}
	return checkSnapshotPromotion(ctx, snapshots, appID, scope, proposed)
}

func checkSnapshotPromotion(ctx context.Context, snapshots state.OpenAPISnapshotStore, appID, scope string, proposed state.OpenAPISnapshot) (PromotionCheck, error) {
	baseline, err := snapshots.LatestOpenAPISnapshotForScope(ctx, appID, scope)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return PromotionCheck{
				Diff: SnapshotDiff{ProposedSHA256: proposed.SHA256}, Proposed: proposed,
			}, ErrSnapshotBaselineMissing
		}
		return PromotionCheck{}, fmt.Errorf("openapidiff: read baseline snapshot: %w", err)
	}
	diff, err := CompareSnapshots(baseline.Snapshot, proposed.Snapshot)
	if err != nil {
		return PromotionCheck{}, err
	}
	return PromotionCheck{Diff: diff, Baseline: baseline, Proposed: proposed, HasBaseline: true}, nil
}
