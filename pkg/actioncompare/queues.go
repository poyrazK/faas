// adr: 20261021090000
package actioncompare

import (
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

var (
	queueTargetPattern   = regexp.MustCompile(`^(workload|app):[a-z0-9][a-z0-9-]{0,62}$`)
	queueSchedulePattern = regexp.MustCompile(`^(absolute|delay:[0-9]{1,8})$`)
)

// addQueueGroups compares the queue sends and delayed tasks an enrolled app
// requested as a multiset per workload, kind, target and queue: the schedule
// and canonical request content must match, while the platform-generated ID
// never does.
func addQueueGroups(snapshot *api.ScenarioActionSnapshot, groups map[string]Change, ids map[string]bool) error {
	if snapshot.QueueCaptureVersion == 0 {
		if len(snapshot.QueueMessages) != 0 || snapshot.IncompleteQueueMessages != 0 {
			return errors.New("queue evidence without a queue capture version")
		}
		return nil
	}
	if snapshot.QueueCaptureVersion != api.ScenarioQueueCaptureVersion || snapshot.IncompleteQueueMessages != 0 ||
		len(snapshot.QueueMessages) > api.MaxScenarioCapturedQueueMessages {
		return errors.New("queue capture evidence is incomplete or exceeds format bounds")
	}
	workloads := map[string]bool{}
	for _, w := range snapshot.Workloads {
		workloads[w] = true
	}
	total := 0
	for _, message := range snapshot.QueueMessages {
		id, err := uuid.Parse(message.ID)
		delayed := message.Kind == api.ScenarioQueueKindDelayedTask
		if err != nil || ids[id.String()] || !workloads[message.Workload] || message.Kind != api.ScenarioQueueKindQueue && message.Kind != api.ScenarioQueueKindAsyncInvoke && !delayed ||
			!queueTargetPattern.MatchString(message.Target) || len(message.QueueName) > 256 || len(message.Message) > api.MaxScenarioCapturedQueueMessageBytes ||
			delayed != queueSchedulePattern.MatchString(message.Schedule) || !delayed && message.Schedule != "" {
			return errors.New("invalid captured queue message identity")
		}
		ids[id.String()] = true
		content, err := behaviorcompare.CanonicalJSON(message.Message)
		if err != nil {
			return errors.New("captured queue message is ambiguous or invalid")
		}
		total += len(content)
		if total > api.MaxScenarioActionPayloadBytes {
			return errors.New("captured queue messages exceed format bounds")
		}
		payload, _ := json.Marshal(struct {
			Schedule string          `json:"schedule,omitempty"`
			Message  json.RawMessage `json:"message"`
		}{message.Schedule, content})
		name := message.Target
		if message.QueueName != "" {
			name += "/" + message.QueueName
		}
		key := eventGroupKey(message.Workload, message.Kind, message.Target, message.QueueName)
		group := groups[key]
		if group.Key == "" {
			group = Change{Key: key, Workload: message.Workload, Source: message.Kind, Name: name}
		}
		group.Before = append(group.Before, Value{Type: message.Kind, Payload: payload})
		groups[key] = group
	}
	return nil
}
