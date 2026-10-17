package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// rolloutFlagConflicts are the flags --rollout replaces or contradicts.
var rolloutFlagConflicts = []string{"safe", "canary-preset", "canary-stages", "traffic-percent", "rollback-on-5xx"}

// applyRolloutFlag maps `gregale deploy --rollout` onto the rollout flags the
// rest of deploy already understands (ADR-1450): safe is --safe, fast and
// immediate choose a canary preset. explicit records the mapped flag so the
// existing validation treats it as given.
func applyRolloutFlag(rollout string, explicit map[string]bool, safe *bool, canaryPreset *string) error {
	for _, other := range rolloutFlagConflicts {
		if explicit[other] {
			return fmt.Errorf("--rollout cannot be combined with --%s", other)
		}
	}
	switch rollout {
	case "safe":
		*safe = true
		explicit["safe"] = true
	case "fast":
		*canaryPreset = api.FastReleaseCanaryPreset
		explicit["canary-preset"] = true
	case "immediate":
		*canaryPreset = "none"
		explicit["canary-preset"] = true
	default:
		return fmt.Errorf("--rollout must be safe, fast or immediate; got %q", rollout)
	}
	return nil
}

// followRolloutByDefault reports whether deploy follows a canary rollout to
// its end (ADR-1450): only for a person at a terminal, never in CI or JSON.
func followRolloutByDefault() bool {
	return !jsonOutput && !nonInteractive && stdoutIsTTY()
}

// printRolloutDetached explains a Ctrl-C while following a rollout: the
// rollout runs on Gregale, not in the CLI.
func printRolloutDetached(d api.DeploymentResponse) {
	PrintProgress(osStdout, "Detached at %d%% traffic; the rollout continues.", d.TrafficPercent)
	PrintProgress(osStdout, "follow again: gregale deployment wait %s --rollout", d.ID)
}
