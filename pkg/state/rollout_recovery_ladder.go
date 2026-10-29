package state

import "fmt"

// recoverAdvanceStage is where a stuck-rollout "advance" moves a canary: the
// next stage of the deployment's persisted ladder, numbered as AdvanceCanary
// numbers it. canary_step is the zero-based stage index, and moving onto the
// last stage completes the rollout at 100% with step = total.
//
// Hunt 20 H20-1: recovery used a fixed table of 1-based percents. Traffic
// lagged one stage behind, other presets and custom ladders got guessed or
// zero percents, and a move onto the last stage stopped short of complete,
// where the stuck detector later aborted the release. A row stranded on its
// last stage that way completes on the next advance.
func recoverAdvanceStage(d Deployment) (step, percent int, complete bool, err error) {
	if d.CanaryTotalSteps <= 0 || d.CanaryStep < 0 || d.CanaryStep >= d.CanaryTotalSteps {
		return 0, 0, false, ErrRolloutStateInvalid
	}
	name := d.CanaryPreset
	if name == "" {
		name = "none"
	}
	preset, err := deploymentCanaryPreset(d, name)
	if err != nil {
		return 0, 0, false, fmt.Errorf("%w: %w", ErrRolloutStateInvalid, err)
	}
	total := preset.TotalSteps()
	if total != d.CanaryTotalSteps {
		return 0, 0, false, fmt.Errorf("%w: canary preset %q has %d stages, deployment records %d", ErrRolloutStateInvalid, name, total, d.CanaryTotalSteps)
	}
	next := d.CanaryStep + 1
	if next >= total-1 {
		return total, 100, true, nil
	}
	stage, _ := preset.StageAt(next)
	return next, stage.Percent, false, nil
}
