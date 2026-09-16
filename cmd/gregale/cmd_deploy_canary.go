// cmd/gregale/cmd_deploy_canary.go — closed-set validation +
// flag-string parsing for the SAFE-RELEASES production-leveling
// Stream F canary preset / stages surface (issue #976 / ADR-122).
// Lives in its own file so cmdDeployTarball stays focused on its
// main path and so the helpers can be unit-tested without
// spinning up the flag machinery.
//
// Two pieces:
//
//  1. buildCanarySpec — turns the (preset, stages) flag pair
//     into an *api.CanaryPresetSpec. Validates the preset name
//     against pkg/api/canary.AllowedCanaryPresets (the closed
//     set) and, for preset="custom", parses the comma-separated
//     "percent@duration" list into []canary.CustomStage. A bad
//     preset name or a malformed stage is an exit-2 PrintFail
//     so the customer sees the error pre-flight (no network
//     round-trip). Empty preset → nil spec → fast-default
//     zero-value on the server (no canary).
//
//  2. parseCanaryStages — internal helper that splits
//     "1@30s,10@2m,100@0s" into the per-stage pairs the DTO
//     expects. Exposed for unit tests so the parsing contract
//     is pinned without spinning up the flag machinery.

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
)

// buildCanarySpec validates the (--canary-preset, --canary-stages)
// flag pair and returns an *api.CanaryPresetSpec ready for the
// CreateDeploymentRequest wire. Empty preset → nil (no canary,
// fast-default zero-value on the server). A bad preset name or a
// malformed stage returns an error so the command can render one structured
// problem and still run its deferred cleanup.
func buildCanarySpec(preset, stages string) (*api.CanaryPresetSpec, error) {
	if preset == "" {
		if strings.TrimSpace(stages) != "" {
			return nil, fmt.Errorf("--canary-stages requires --canary-preset=custom")
		}
		// Fast path: no flag → no canary. Mirrors the pre-PR
		// behaviour (nil Canary on the request → server stamps
		// canary_preset='none', canary_total_steps=0).
		return nil, nil
	}
	if !canary.AllowedCanaryPreset(preset) {
		return nil, fmt.Errorf("--canary-preset=%q is not in the closed set %v", preset, canary.AllowedCanaryPresets)
	}
	if preset != "custom" && strings.TrimSpace(stages) != "" {
		return nil, fmt.Errorf("--canary-stages is only valid with --canary-preset=custom")
	}
	spec := &api.CanaryPresetSpec{Preset: preset}
	if preset == "custom" {
		parsed, err := parseCanaryStages(stages)
		if err != nil {
			return nil, fmt.Errorf("--canary-stages=%q: %w", stages, err)
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("--canary-preset=custom requires a non-empty --canary-stages list")
		}
		if _, err := canary.LookupCustomPreset(parsed); err != nil {
			return nil, fmt.Errorf("--canary-stages=%q: %w", stages, err)
		}
		spec.Stages = parsed
	}
	return spec, nil
}

// parseCanaryStages splits a comma-separated "percent@duration"
// list into []canary.CustomStage. Empty input returns an empty
// slice (caller's responsibility to require at least one stage for
// preset=custom). Whitespace around commas is trimmed so
// "1@30s, 10@2m, 100@0s" parses identically to the tight form.
//
// time.ParseDuration runs inside LookupCustomPreset at the apid
// handler; here we only enforce the percent-parses-as-int +
// percent-in-range + duration-non-empty invariants so a CLI typo
// fails fast.
func parseCanaryStages(s string) ([]canary.CustomStage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]canary.CustomStage, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("stage %d: empty", i)
		}
		at := strings.IndexByte(p, '@')
		if at < 0 {
			return nil, fmt.Errorf("stage %d: missing '@' (expected percent@duration, got %q)", i, p)
		}
		percentStr := strings.TrimSpace(p[:at])
		durationStr := strings.TrimSpace(p[at+1:])
		if percentStr == "" {
			return nil, fmt.Errorf("stage %d: missing percent before '@'", i)
		}
		if durationStr == "" {
			return nil, fmt.Errorf("stage %d: missing duration after '@'", i)
		}
		percent, err := strconv.Atoi(percentStr)
		if err != nil {
			// %w per CLAUDE.md "Errors: wrap with %w + operation
			// context" + golangci-lint errorlint rule (the pre-rebase
			// lint job ran v8 and caught this as the only errorlint
			// violation in the Stream F diff).
			return nil, fmt.Errorf("stage %d: percent %q: %w", i, percentStr, err)
		}
		if percent < 0 || percent > 100 {
			return nil, fmt.Errorf("stage %d: percent %d out of [0,100]", i, percent)
		}
		out = append(out, canary.CustomStage{Percent: percent, Duration: durationStr})
	}
	return out, nil
}
