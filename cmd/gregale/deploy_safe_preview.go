package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

// addSafeReleasePreview enriches a read-only deploy diff with the facts an
// operator needs before starting a health-gated rollout: the ladder, the
// next step, actionable alert rules, and the previous deployment target.
// Alert reads are intentionally limited to --safe previews so ordinary diff
// output keeps its existing request cost and wire shape.
func addSafeReleasePreview(ctx context.Context, client *api.Client, opts diffCLIOptions, baseline deploydiff.Baseline, d *deploydiff.Diff) {
	if !opts.Safe || d == nil {
		return
	}

	preview, err := newSafeReleasePreview(opts.Canary, baseline.LatestDeployment)
	if err != nil {
		d.Breaks = append(d.Breaks, deploydiff.Break{
			Code:     "safe_release_rollout_unavailable",
			Severity: deploydiff.SeverityWarn,
			Reason:   fmt.Sprintf("could not resolve the safe rollout plan: %v", err),
			Field:    "safe_release.rollout",
		})
		return
	}
	if preview == nil {
		return
	}

	if client == nil {
		preview.HealthGate.Status = deploydiff.SafeReleaseHealthUnavailable
		addSafeReleaseHealthWarning(d, "safe_release_health_gate_unavailable", "could not read alert rules; safe rollout health-gate status is unavailable")
		d.SafeRelease = preview
		return
	}
	rules, err := client.ListAlertRules(ctx, opts.Slug)
	if err != nil {
		preview.HealthGate.Status = deploydiff.SafeReleaseHealthUnavailable
		addSafeReleaseHealthWarning(d, "safe_release_health_gate_unavailable", "could not read alert rules; safe rollout health-gate status is unavailable")
		d.SafeRelease = preview
		return
	}
	preview.HealthGate = safeReleaseHealthGate(rules)
	if preview.HealthGate.Status == deploydiff.SafeReleaseHealthNotConfigured {
		addSafeReleaseHealthWarning(d, "safe_release_health_gate_missing", "no enabled rollback/demote alert rule is configured; the safe rollout can advance without an actionable health gate")
	}
	d.SafeRelease = preview
}

func newSafeReleasePreview(spec *api.CanaryPresetSpec, latest *api.DeploymentResponse) (*deploydiff.SafeReleasePreview, error) {
	if spec == nil || spec.Preset == "" || spec.Preset == "none" {
		return nil, nil
	}
	preset, ok := canarycatalog.LookupPreset(spec.Preset)
	if !ok {
		return nil, fmt.Errorf("unknown preset %q", spec.Preset)
	}
	if spec.Preset == "custom" {
		var err error
		preset, err = canarycatalog.LookupCustomPreset(spec.Stages)
		if err != nil {
			return nil, err
		}
	}
	if len(preset.Stages) == 0 {
		return nil, fmt.Errorf("preset %q has no rollout stages", spec.Preset)
	}
	stages := make([]deploydiff.SafeReleaseStage, 0, len(preset.Stages))
	for _, stage := range preset.Stages {
		stages = append(stages, deploydiff.SafeReleaseStage{
			TrafficPercent: stage.Percent,
			Duration:       stage.Duration.String(),
		})
	}
	preview := &deploydiff.SafeReleasePreview{
		Preset:        spec.Preset,
		RollbackOn5xx: true,
		Rollout: deploydiff.SafeReleaseRollout{
			Step:           1,
			TotalSteps:     len(stages),
			TrafficPercent: stages[0].TrafficPercent,
			Stages:         stages,
		},
		HealthGate: deploydiff.SafeReleaseHealthGate{
			Status: deploydiff.SafeReleaseHealthReady,
		},
	}
	if latest != nil {
		preview.RollbackTarget = latest.ID
	}
	return preview, nil
}

func safeReleaseHealthGate(rules []api.AlertRuleResponse) deploydiff.SafeReleaseHealthGate {
	gate := deploydiff.SafeReleaseHealthGate{Status: deploydiff.SafeReleaseHealthNotConfigured}
	for _, rule := range rules {
		if !rule.Enabled || (rule.Action != "rollback" && rule.Action != "demote") {
			continue
		}
		gate.Configured++
		if rule.State == "firing" {
			gate.Firing++
		}
		gate.Rules = append(gate.Rules, deploydiff.SafeReleaseHealthRule{
			Name: rule.Name, Metric: rule.Metric, Action: rule.Action, State: rule.State,
		})
	}
	sort.Slice(gate.Rules, func(i, j int) bool {
		if gate.Rules[i].Name != gate.Rules[j].Name {
			return gate.Rules[i].Name < gate.Rules[j].Name
		}
		return gate.Rules[i].Metric < gate.Rules[j].Metric
	})
	if gate.Firing > 0 {
		gate.Status = deploydiff.SafeReleaseHealthBlocked
	} else if gate.Configured > 0 {
		gate.Status = deploydiff.SafeReleaseHealthReady
	}
	return gate
}

func addSafeReleaseHealthWarning(d *deploydiff.Diff, code, reason string) {
	if d == nil {
		return
	}
	d.Breaks = append(d.Breaks, deploydiff.Break{
		Code:     code,
		Severity: deploydiff.SeverityWarn,
		Reason:   strings.TrimSpace(reason),
		Field:    "safe_release.health_gate",
	})
}
