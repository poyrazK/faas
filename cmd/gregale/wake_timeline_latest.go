package main

import "github.com/onebox-faas/faas/pkg/api"

// latestWakeID returns the wake ID of the most recently started instance
// that has one, or "". `app wake-timeline APP` shows that wake.
func latestWakeID(instances []api.InstanceResponse) string {
	best, bestAt := "", ""
	for _, instance := range instances {
		if instance.WakeID == "" {
			continue
		}
		if best == "" || instance.StartedAt > bestAt {
			best, bestAt = instance.WakeID, instance.StartedAt
		}
	}
	return best
}
