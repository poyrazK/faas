package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// These are internal apid inputs, never caller-provided approval evidence.
// A gateway generation is allocated before prepare and retained across crash
// recovery. ExpectedNodes is a frozen minimum: recovery can add, never drop,
// required gateways merely because one stopped responding.
type EnvironmentGitOpsEffectSpec struct {
	AppID             string
	Kind              string
	GatewayGeneration int64
	MatchHosts        []string
	ExpectedNodes     []string
}

type EnvironmentGitOpsEffect struct {
	EnvironmentGitOpsEffectSpec
	ID                string
	SourceID          string
	RevisionID        string
	Generation        int64
	IntentVersion     int64
	PlanHash          string
	AcknowledgedNodes []string
	CompletedAt       *time.Time
}

type EnvironmentGitOpsEffectStore interface {
	ApplyEnvironmentGitOpsWithEffects(context.Context, EnvironmentGitOpsLease, environmentsync.Plan, []EnvironmentGitOpsEffectSpec) ([]EnvironmentGitOpsStep, error)
	PendingEnvironmentGitOpsEffects(context.Context, EnvironmentGitOpsLease) ([]EnvironmentGitOpsEffect, error)
	ExtendEnvironmentGitOpsEffectTargets(context.Context, EnvironmentGitOpsLease, string, []string) (EnvironmentGitOpsEffect, error)
	AcknowledgeEnvironmentGitOpsEffect(context.Context, EnvironmentGitOpsLease, string, int64, string) error
	CompleteEnvironmentGitOpsEffect(context.Context, EnvironmentGitOpsLease, string) error
}

func canonicalGitOpsEffectNames(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, ErrInvalidArgument
		}
		out = append(out, value)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func validateGitOpsEffects(plan environmentsync.Plan, observed EnvironmentGitOpsObservation, effects []EnvironmentGitOpsEffectSpec) ([]EnvironmentGitOpsEffectSpec, error) {
	required := map[string]bool{}
	for _, change := range plan.Changes {
		if change.Path == "policies" && (change.Action == "create" || change.Action == "update" || change.Action == "remove") {
			required[observed.State.ResourceIDs[change.Resource]] = true
		}
	}
	seen := map[string]bool{}
	generations := map[int64]bool{}
	out := make([]EnvironmentGitOpsEffectSpec, 0, len(effects))
	for _, effect := range effects {
		if effect.AppID == "" || effect.Kind != "edge_policy" || effect.GatewayGeneration <= 0 || !required[effect.AppID] || seen[effect.AppID] || generations[effect.GatewayGeneration] {
			return nil, ErrInvalidArgument
		}
		var err error
		effect.MatchHosts, err = canonicalGitOpsEffectNames(effect.MatchHosts)
		if err != nil || len(effect.MatchHosts) == 0 {
			return nil, ErrInvalidArgument
		}
		effect.ExpectedNodes, err = canonicalGitOpsEffectNames(effect.ExpectedNodes)
		if err != nil {
			return nil, err
		}
		seen[effect.AppID], generations[effect.GatewayGeneration] = true, true
		out = append(out, effect)
	}
	if len(seen) != len(required) {
		return nil, ErrInvalidArgument
	}
	return out, nil
}

func cloneEnvironmentGitOpsEffect(effect EnvironmentGitOpsEffect) EnvironmentGitOpsEffect {
	effect.MatchHosts = slices.Clone(effect.MatchHosts)
	effect.ExpectedNodes = slices.Clone(effect.ExpectedNodes)
	effect.AcknowledgedNodes = slices.Clone(effect.AcknowledgedNodes)
	if effect.CompletedAt != nil {
		completed := *effect.CompletedAt
		effect.CompletedAt = &completed
	}
	return effect
}
