package main

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsProfileShape is the reporting-only view of an app's resolved resource
// shape. The label is intentionally closed: named profiles retain their name,
// while arbitrary RAM/CPU pairs collapse into the bounded "custom" bucket.
type obsProfileShape struct {
	label         string
	memoryMB      int
	cpuMillicores int
}

func obsProfileShapeForApp(app state.App, found bool) obsProfileShape {
	if !found {
		return obsProfileShape{label: "unknown"}
	}
	profile := api.ResourceProfileForResources(app.RAMMB, app.CPUMillicores)
	if profile == "" {
		return obsProfileShape{label: "custom"}
	}
	return obsProfileShape{
		label:         string(profile),
		memoryMB:      app.RAMMB,
		cpuMillicores: app.CPUMillicores,
	}
}

func obsProfileSortKey(label string) int {
	for i, profile := range api.ResourceProfiles {
		if string(profile.Name) == label {
			return i
		}
	}
	if label == "custom" {
		return len(api.ResourceProfiles)
	}
	return len(api.ResourceProfiles) + 1
}

func sortObsProfileLabels(labels []string) {
	sort.Slice(labels, func(i, j int) bool {
		ki, kj := obsProfileSortKey(labels[i]), obsProfileSortKey(labels[j])
		if ki != kj {
			return ki < kj
		}
		return labels[i] < labels[j]
	})
}

func obsInstanceIsLive(instance state.Instance) bool {
	switch state.State(instance.State) {
	case state.StateRunning, state.StateWaking, state.StateColdBooting:
		return true
	default:
		return false
	}
}

func buildObsCapacityProfiles(apps []state.App, instances []state.Instance) []api.ObsCapacityProfile {
	profiles := make(map[string]*api.ObsCapacityProfile, len(api.ResourceProfiles)+1)
	appShapes := make(map[string]obsProfileShape, len(apps))
	for _, app := range apps {
		if app.Status == state.AppDeleted {
			continue
		}
		shape := obsProfileShapeForApp(app, true)
		appShapes[app.ID] = shape
		row := profiles[shape.label]
		if row == nil {
			row = &api.ObsCapacityProfile{
				ResourceProfile: shape.label,
				MemoryMB:        shape.memoryMB,
				CPUMillicores:   shape.cpuMillicores,
			}
			profiles[shape.label] = row
		}
		row.Apps++
		row.ReservedMemoryMB += int64(app.RAMMB)
		row.ReservedCPUMillicores += int64(app.CPUMillicores)
	}
	for _, instance := range instances {
		if !obsInstanceIsLive(instance) {
			continue
		}
		shape, ok := appShapes[instance.AppID]
		if !ok {
			continue
		}
		row := profiles[shape.label]
		if row != nil {
			row.LiveInstances++
		}
	}
	labels := make([]string, 0, len(profiles))
	for label := range profiles {
		labels = append(labels, label)
	}
	sortObsProfileLabels(labels)
	out := make([]api.ObsCapacityProfile, 0, len(labels))
	for _, label := range labels {
		out = append(out, *profiles[label])
	}
	return out
}

type obsUsageProfileAccumulator struct {
	profile   api.ObsTenantUsageProfile
	mbSeconds int64
	cpuUsec   int64
	appIDs    map[string]struct{}
}

func buildObsTenantUsageProfiles(rows []state.Usage, apps map[string]state.App) []api.ObsTenantUsageProfile {
	profiles := make(map[string]*obsUsageProfileAccumulator, len(api.ResourceProfiles)+2)
	for _, row := range rows {
		app, found := apps[row.AppID]
		shape := obsProfileShapeForApp(app, found)
		acc := profiles[shape.label]
		if acc == nil {
			acc = &obsUsageProfileAccumulator{
				profile: api.ObsTenantUsageProfile{
					ResourceProfile: shape.label,
					MemoryMB:        shape.memoryMB,
					CPUMillicores:   shape.cpuMillicores,
				},
				appIDs: make(map[string]struct{}),
			}
			profiles[shape.label] = acc
		}
		if _, seen := acc.appIDs[row.AppID]; !seen {
			acc.appIDs[row.AppID] = struct{}{}
			acc.profile.Apps++
		}
		acc.mbSeconds += row.MBSeconds
		acc.cpuUsec += row.CPUUsec
		acc.profile.Requests += row.Requests
		acc.profile.ColdBoots += row.ColdBootCount
	}
	labels := make([]string, 0, len(profiles))
	for label := range profiles {
		labels = append(labels, label)
	}
	sortObsProfileLabels(labels)
	out := make([]api.ObsTenantUsageProfile, 0, len(labels))
	for _, label := range labels {
		acc := profiles[label]
		acc.profile.UsedGBHours = meter.GBHours(acc.mbSeconds)
		acc.profile.UsedCPUHours = float64(acc.cpuUsec) / 3_600_000_000.0
		out = append(out, acc.profile)
	}
	return out
}
