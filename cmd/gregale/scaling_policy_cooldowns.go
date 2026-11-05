package main

import "github.com/onebox-faas/faas/pkg/api"

// fillScalingPolicyCooldowns fills only absent cooldowns with the defaults.
// Older/default rows round-trip as a `{}` policy, and an app deployed without
// one has none at all; apid validates cooldowns whenever a replacement policy
// is supplied, so the zero values made an unrelated update fail with
// invalid_cooldown. Valid existing values stay untouched.
func fillScalingPolicyCooldowns(policy *api.ScalingPolicy) {
	if policy.ScaleOutCooldownS == 0 {
		policy.ScaleOutCooldownS = 5
	}
	if policy.ScaleInCooldownS == 0 {
		policy.ScaleInCooldownS = 60
	}
}
