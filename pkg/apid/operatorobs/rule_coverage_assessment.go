package operatorobs

import (
	"context"
	"path"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

type coverageCandidate struct {
	rule        promql.AlertRule
	groupID     string
	groupLabels bool
}

type coverageSource struct {
	candidates                                        map[string][]coverageCandidate
	groups                                            map[string]int
	observed, additional, invalidRules, invalidGroups int
}

func coverageSourceIndex(groups []promql.AlertRuleGroup, expected obsrulecatalog.Set) coverageSource {
	out := coverageSource{candidates: map[string][]coverageCandidate{}, groups: map[string]int{}}
	ids := map[string]bool{}
	for _, r := range expected.Rules {
		ids[r.ID] = true
	}
	for _, g := range groups {
		file := path.Base(g.File)
		groupValid := g.File != "" && obsrulecatalog.SafeIdentity(file) && obsrulecatalog.SafeIdentity(g.Name)
		groupID := ""
		if !groupValid {
			out.invalidGroups++
		} else {
			groupID = ruleHealthHash([]string{file, g.Name})
			out.groups[groupID]++
		}
		for _, r := range g.Rules {
			out.observed++
			id, valid := obsrulecatalog.Identity(file, g.Name, r.Name)
			if !groupValid || !valid {
				out.invalidRules++
				continue
			}
			if !ids[id] {
				out.additional++
				continue
			}
			out.candidates[id] = append(out.candidates[id], coverageCandidate{rule: r, groupID: groupID, groupLabels: len(g.Labels) > 0})
		}
	}
	return out
}

func assessRuleCoverage(ctx context.Context, groups []promql.AlertRuleGroup, expected obsrulecatalog.Set, out api.ObsRuleCoverage) (api.ObsRuleCoverage, error) {
	return assessDefinitionCoverage(ctx, groups, expected, out, "alerting")
}

func assessDefinitionCoverage(ctx context.Context, groups []promql.AlertRuleGroup, expected obsrulecatalog.Set, out api.ObsRuleCoverage, kind string) (api.ObsRuleCoverage, error) {
	source := coverageSourceIndex(groups, expected)
	t := &api.ObsRuleCoverageTotals{ExpectedRules: len(expected.Rules), ObservedRules: source.observed, AdditionalRules: source.additional, InvalidObservedRules: source.invalidRules, InvalidObservedGroups: source.invalidGroups}
	for _, r := range expected.Rules {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		item := coverageRuleKind(r, source, kind)
		switch item.State {
		case "matching":
			t.Matching++
		case "missing":
			t.Missing++
		case "changed":
			t.Changed++
		case "ambiguous":
			t.Ambiguous++
		default:
			t.Unknown++
		}
		out.Items = append(out.Items, item)
	}
	out.Totals = t
	out.State, out.Reason = "covered", ""
	if t.Missing+t.Changed+t.Ambiguous > 0 {
		out.State, out.Reason = "degraded", "expected_rules_differ"
	} else if t.Unknown+t.InvalidObservedRules+t.InvalidObservedGroups > 0 {
		out.State, out.Reason = "partial", "comparison_incomplete"
		if t.Matching == 0 {
			out.State = "unknown"
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if coveragePriority(a.State) != coveragePriority(b.State) {
			return coveragePriority(a.State) < coveragePriority(b.State)
		}
		return a.ID < b.ID
	})
	out.TotalItems = len(out.Items)
	out.Truncated = out.TotalItems > out.Limit
	if out.Truncated {
		out.Items = out.Items[:out.Limit]
	}
	return out, ctx.Err()
}

func coverageRuleKind(expected obsrulecatalog.Rule, source coverageSource, kind string) api.ObsRuleCoverageItem {
	candidates := source.candidates[expected.ID]
	out := api.ObsRuleCoverageItem{ID: expected.ID, FileName: expected.FileName, GroupName: expected.GroupName, RuleName: expected.RuleName, State: "missing", Reason: "rule_missing", ExpectedFingerprint: expected.Definition.Fingerprint, ObservedCandidates: len(candidates), ChangedFields: []string{}}
	if len(candidates) == 0 {
		if source.invalidRules+source.invalidGroups > 0 {
			out.State, out.Reason = "unknown", "source_identity_unavailable"
		}
		return out
	}
	candidate := candidates[0]
	if len(candidates) > 1 || source.groups[candidate.groupID] > 1 {
		out.State, out.Reason = "ambiguous", "ambiguous_identity"
		return out
	}
	r := candidate.rule
	if r.Type != kind || candidate.groupLabels || (kind == "alerting" && (!r.DurationReported || r.KeepFiringFor == nil || r.Labels == nil)) || (kind == "recording" && r.LabelsNull) {
		out.State, out.Reason = "unknown", "definition_unavailable"
		return out
	}
	hold, keep := 0.0, 0.0
	if kind == "alerting" {
		hold, keep = r.Duration, *r.KeepFiringFor
	}
	definition, err := obsrulecatalog.Normalize(r.Query, hold, keep, r.Labels)
	if err != nil {
		out.State, out.Reason = "unknown", "definition_unavailable"
		return out
	}
	out.ObservedFingerprint = definition.Fingerprint
	out.ChangedFields = expected.Definition.ChangedFields(definition)
	out.State, out.Reason = "matching", ""
	if len(out.ChangedFields) > 0 {
		out.State, out.Reason = "changed", "definition_changed"
	}
	return out
}

func coveragePriority(state string) int {
	switch state {
	case "ambiguous":
		return 0
	case "changed":
		return 1
	case "missing":
		return 2
	case "unknown":
		return 3
	default:
		return 4
	}
}
