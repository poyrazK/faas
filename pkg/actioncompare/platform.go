// adr: 20261021150000
package actioncompare

import (
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

var platformTargetPattern = regexp.MustCompile(`^(workload|app|run|endpoint):`)

// addPlatformGroups compares the workflow and realtime calls an enrolled app
// requested as a multiset per workload, kind, target and name, by canonical
// message. Platform IDs never compare: a captured workflow run is addressed by
// its starter, target, workflow and start ordinal.
func addPlatformGroups(snapshot *api.ScenarioActionSnapshot, groups map[string]Change, ids map[string]bool) error {
	if snapshot.PlatformCaptureVersion == 0 {
		if len(snapshot.PlatformCalls) != 0 || snapshot.IncompletePlatformCalls != 0 {
			return errors.New("platform call evidence without a platform capture version")
		}
		return nil
	}
	if snapshot.PlatformCaptureVersion != api.ScenarioPlatformCaptureVersion || snapshot.IncompletePlatformCalls != 0 ||
		len(snapshot.PlatformCalls) > api.MaxScenarioCapturedPlatformCalls {
		return errors.New("platform call evidence is incomplete or exceeds format bounds")
	}
	workloads := map[string]bool{}
	for _, w := range snapshot.Workloads {
		workloads[w] = true
	}
	total := 0
	for _, call := range snapshot.PlatformCalls {
		id, err := uuid.Parse(call.ID)
		switch call.Kind {
		case api.ScenarioPlatformKindWorkflowRun, api.ScenarioPlatformKindWorkflowEvent, api.ScenarioPlatformKindRealtimePublish, api.ScenarioPlatformKindRealtimeSignal:
		default:
			err = errors.New("unknown kind")
		}
		if err != nil || ids[id.String()] || !workloads[call.Workload] || !platformTargetPattern.MatchString(call.Target) || len(call.Target) > 512 ||
			call.Name == "" || len(call.Name) > 256 || len(call.Message) > api.MaxScenarioCapturedPlatformCallBytes {
			return errors.New("invalid captured platform call identity")
		}
		ids[id.String()] = true
		content, err := behaviorcompare.CanonicalJSON(call.Message)
		if err != nil {
			return errors.New("captured platform call is ambiguous or invalid")
		}
		total += len(content)
		if total > api.MaxScenarioActionPayloadBytes {
			return errors.New("captured platform calls exceed format bounds")
		}
		key := eventGroupKey(call.Workload, call.Kind, call.Target, call.Name)
		group := groups[key]
		if group.Key == "" {
			group = Change{Key: key, Workload: call.Workload, Source: call.Kind, Name: call.Target + " " + call.Name}
		}
		group.Before = append(group.Before, Value{Type: call.Kind, Payload: json.RawMessage(content)})
		groups[key] = group
	}
	return nil
}
