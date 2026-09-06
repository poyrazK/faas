// Package canary (issue #976 / ADR-122 / SAFE-RELEASES-A) — catalog of
// canary deployment presets. The catalog lives in pkg/api (not pkg/state)
// for the same reason pkg/api/alert_presets.go does: pkg/api must stay
// free of the pkg/api ↔ pkg/state import cycle, and the closed sets
// belong alongside the DTOs that the handler validates.
//
// The canary_progression meterd tick (pkg/canary) consumes a
// pkg/canary/preset.go runtime wrapper that calls LookupPreset; the
// CLI default for `--canary-preset 1-10-50-100` resolves to the
// `balanced` preset (4 stages) — the catalog ships both names so the
// CLI ergonomic alias and the canonical name are first-class.
//
// Each preset is a list of Stages. Stage N is reached when
// canary_step_started_at + Stage[N].Duration has elapsed. The progression
// worker asks APID's atomic canary endpoint to apply Stage[N].Percent
// (meterd never writes the deployment row directly per CLAUDE.md ownership
// rules). The final stage always has Duration 0 — it is entered as the
// terminal 100% state.
package canary

import (
	"fmt"
	"time"
)

// Stage is one rung of the canary ladder. Percent is the slice of
// traffic the new deployment should serve at this step (0..100;
// validated against pkg/api/limits.go::TrafficSplitAllowed by the
// apid handler before INSERT). Duration is how long the orchestrator
// waits before advancing to the next stage. A Duration of 0 marks
// the terminal stage (no further advance; rollout_state flips to
// `complete` on entry).
type Stage struct {
	Percent     int
	Duration    time.Duration
	MirrorClean *MirrorCleanCondition
}

// Preset is a named ordered list of Stages. The catalog names map
// 1:1 to deployments.canary_preset (CHECK constraint at migration
// 00480) and to the CLI's --canary-preset flag.
type Preset struct {
	Name   string
	Stages []Stage
}

// TotalSteps returns len(Stages). Mirrors
// deployments.canary_total_steps (the column the orchestrator walks).
func (p Preset) TotalSteps() int { return len(p.Stages) }

// AllowedCanaryPresets is the closed set for the canary_preset field
// on deployments AND the --canary-preset CLI flag. Mirrors the
// deployments_canary_preset_chk DB constraint at migration 00487
// (widen from migration 00480 to admit "custom"). Order matches the
// catalog above so the dashboard renders the ladder selector in a
// stable order.
var AllowedCanaryPresets = []string{
	"none",
	"slow",
	"balanced",
	"aggressive",
	"1-10-50-100",
	// "custom" — Stages come from the deployment row's
	// canary_stages jsonb column rather than the catalog. The
	// catalog entry has nil Stages so a stray LookupPreset
	// call returns ok=true with empty stages (the orchestrator
	// then reads the row's canary_stages and synthesises a
	// Preset via LookupCustomPreset). The migration's
	// canary_stages_shape CHECK refuses preset='custom' with a
	// NULL canary_stages, so the row-level invariant is
	// enforced at write time.
	"custom",
}

// catalog is the registry of every shipped preset. Lookup by name
// returns a copy of the preset (callers can mutate freely without
// affecting other lookups). Unregistered names return ok=false.
//
// The catalog is intentionally hard-coded — operator-facing
// canary rollout cadence is a platform concern, not a per-account
// one. Customers who want custom ladders land in a follow-up
// (per-account canary_progression spec at meterd runtime, gated
// on Enterprise plan).
var catalog = map[string]Preset{
	// "none" — no canary ladder; the deployment is stamped with
	// canary_total_steps=0 and traffic goes straight to 100% on the
	// existing rollout path. Equivalent to NOT specifying
	// --canary-preset at all. Required entry so the catalog has a
	// disabling name (the migration's NOT NULL DEFAULT 'none' is
	// how pre-PR rows self-identify).
	"none": {Name: "none", Stages: nil},

	// "slow" — 3 stages. 2 min observation at each rung, ~6 min
	// minimum time-to-complete. Recommended for production traffic
	// on latency-sensitive apps where 1% can soak real users but a
	// regression at 10% is recoverable.
	"slow": {
		Name: "slow",
		Stages: []Stage{
			{Percent: 1, Duration: 5 * time.Minute},
			{Percent: 10, Duration: 5 * time.Minute},
			{Percent: 100, Duration: 0},
		},
	},

	// "balanced" — 4 stages. 2 min observation at each rung, ~6 min
	// minimum time-to-complete. Recommended default for most apps;
	// the additional 50% rung between 10% and 100% is the
	// regression-catching middle ground.
	"balanced": {
		Name: "balanced",
		Stages: []Stage{
			{Percent: 1, Duration: 2 * time.Minute},
			{Percent: 10, Duration: 2 * time.Minute},
			{Percent: 50, Duration: 2 * time.Minute},
			{Percent: 100, Duration: 0},
		},
	},

	// "aggressive" — 3 stages. 1 min observation, ~2 min
	// time-to-complete. Reserved for development environments or
	// pre-merge canaries where the operator has high confidence.
	"aggressive": {
		Name: "aggressive",
		Stages: []Stage{
			{Percent: 5, Duration: 1 * time.Minute},
			{Percent: 50, Duration: 1 * time.Minute},
			{Percent: 100, Duration: 0},
		},
	},

	// "1-10-50-100" — CLI alias of `balanced`. Same Stage layout,
	// different catalog name. Lets `gregale deploy
	// --canary-preset 1-10-50-100` self-document (the customer sees
	// the percentages in the flag) while the underlying preset
	// stays a single canonical entry.
	"1-10-50-100": {
		Name: "1-10-50-100",
		Stages: []Stage{
			{Percent: 1, Duration: 2 * time.Minute},
			{Percent: 10, Duration: 2 * time.Minute},
			{Percent: 50, Duration: 2 * time.Minute},
			{Percent: 100, Duration: 0},
		},
	},

	// "custom" — Stages come from the deployment row's
	// canary_stages jsonb column. The catalog entry has nil
	// Stages because the layout is per-deployment; the apid
	// handler's LookupCustomPreset synthesises a Preset by
	// running Validate() on the row's stages. Catalog
	// membership is what makes `AllowedCanaryPreset("custom")`
	// return true (so the apid handler's pre-INSERT check
	// doesn't 400), but the orchestrator's per-row resolve
	// goes through LookupCustomPreset, not LookupPreset.
	"custom": {Name: "custom", Stages: nil},
}

// LookupPreset returns the preset for name, or ok=false if name is
// not in the catalog. Caller-side check; the apid handler validates
// membership before INSERT and the meterd tick validates before
// walking the ladder.
func LookupPreset(name string) (Preset, bool) {
	p, ok := catalog[name]
	if !ok {
		return Preset{}, false
	}
	// Return a copy so callers can mutate Stages without
	// contaminating the package-level registry.
	stages := make([]Stage, len(p.Stages))
	copy(stages, p.Stages)
	return Preset{Name: p.Name, Stages: stages}, true
}

// AllowedCanaryPreset returns true iff name is in
// AllowedCanaryPresets. Mirrors the closed-set membership pattern at
// pkg/api/alerts.go: AllowedAlertRuleMetric / Comparison /
// WindowSpec / FailureSource.
func AllowedCanaryPreset(name string) bool {
	for _, n := range AllowedCanaryPresets {
		if n == name {
			return true
		}
	}
	return false
}

// StageAt returns the Stage at index step. Mirrors pkg/state
// transitions: canary_step is the zero-indexed position in
// Preset.Stages. Boolean false signals an out-of-range index —
// caller is expected to check before advancing.
func (p Preset) StageAt(step int) (Stage, bool) {
	if step < 0 || step >= len(p.Stages) {
		return Stage{}, false
	}
	return p.Stages[step], true
}

// Validate inspects every Stage and returns an error on the first
// invalid rung. Used by tests and by the apid handler's body
// validator to catch a corrupt catalog (e.g. percent out of range,
// non-monotonic). The orchestrator trusts the catalog on disk;
// failures here surface as meterd panic-once-at-startup, not at
// runtime.
func (p Preset) Validate() error {
	if len(p.Stages) == 0 {
		// "none" preset has no stages — that's the disable case.
		return nil
	}
	for i, s := range p.Stages {
		if s.Percent < 0 || s.Percent > 100 {
			return fmt.Errorf("canary preset %q stage %d: percent %d out of [0,100]", p.Name, i, s.Percent)
		}
		if s.Duration < 0 {
			return fmt.Errorf("canary preset %q stage %d: duration %v is negative", p.Name, i, s.Duration)
		}
		if s.MirrorClean != nil {
			if s.MirrorClean.MinInvocations <= 0 {
				return fmt.Errorf("canary preset %q stage %d: mirror_clean.min_invocations must be positive", p.Name, i)
			}
			if s.MirrorClean.WindowSeconds <= 0 {
				return fmt.Errorf("canary preset %q stage %d: mirror_clean.window_s must be positive", p.Name, i)
			}
		}
	}
	// The terminal stage must reach 100% (rollback safety: if the
	// ladder caps below 100 the deployment is stuck at less than
	// full traffic forever).
	last := p.Stages[len(p.Stages)-1]
	if last.Percent != 100 || last.Duration != 0 {
		return fmt.Errorf("canary preset %q terminal stage must be {100, 0}; got {%d, %v}", p.Name, last.Percent, last.Duration)
	}
	return nil
}

// LookupCustomPreset synthesises a Preset from a customer-
// supplied stage list, validates it via Validate(), and returns
// the synthesised Preset. The returned Preset carries Name=
// "custom" + the parsed stages so the orchestrator can walk the
// ladder the same way it walks a catalog preset (StageAt,
// TotalSteps, …).
//
// Validation runs Validate() before returning — a bad stage
// (negative percent, terminal stage not at 100%) surfaces as an
// error here so the apid handler can 422 with the field-level
// reason instead of letting the row reach Postgres.
//
// An empty stages list is rejected (the "no ladder" case is
// preset="none", not preset="custom" with empty stages).
func LookupCustomPreset(stages []CustomStage) (Preset, error) {
	if len(stages) == 0 {
		return Preset{}, fmt.Errorf("canary: custom preset requires at least one stage (use preset=none for no ladder)")
	}
	parsed := make([]Stage, 0, len(stages))
	for i, s := range stages {
		d, err := time.ParseDuration(s.Duration)
		if err != nil {
			return Preset{}, fmt.Errorf("canary: custom stage %d: duration %q: %w", i, s.Duration, err)
		}
		var mirrorClean *MirrorCleanCondition
		if s.MirrorClean != nil {
			condition := *s.MirrorClean
			mirrorClean = &condition
		}
		parsed = append(parsed, Stage{Percent: s.Percent, Duration: d, MirrorClean: mirrorClean})
	}
	p := Preset{Name: "custom", Stages: parsed}
	if err := p.Validate(); err != nil {
		return Preset{}, err
	}
	return p, nil
}
