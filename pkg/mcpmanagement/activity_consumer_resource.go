package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const activityConsumerResourceTemplate = "gregale://projects/{project}/environments/{environment}/apps/{app}/activity-consumers/{consumer}"
const activityConsumerResourceView = "activity-consumer"

// Binding is internal read-validation context, never serialized resource data.
type activityConsumerResourceResult struct {
	activityConsumerResult
	binding activityConsumerBinding
}

func resourceConsumerArgs(args contextResourceArgs) activityConsumerArgs {
	return activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
}

func (m *management) selectedContextResourceValue(ctx context.Context, args contextResourceArgs, view string) (any, error) {
	if view != activityConsumerResourceView {
		return m.contextResourceValue(ctx, args.appArgs, view)
	}
	base := resourceConsumerArgs(args)
	b, _, err := m.activityConsumerTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	value, err := m.inspectConsumerAtTarget(ctx, base, b)
	if err != nil {
		return nil, err
	}
	state := value.(activityConsumerResult)
	state.Cursor, state.ActivityRecovery, state.Hint = "", nil, ""
	return activityConsumerResourceResult{activityConsumerResult: state, binding: b}, nil
}

// Call after the final native consent/identity reads, so changes during those
// reads cannot release stale consumer state or metadata after revocation.
func (m *management) checkConsumerResource(ctx context.Context, args contextResourceArgs, value any) error {
	state, ok := value.(activityConsumerResourceResult)
	if !ok {
		return nil
	}
	return m.checkConsumerObservation(ctx, resourceConsumerArgs(args), state.binding, activityConsumerRecord{Revision: state.Revision, Generation: state.Generation})
}

func contextResourceDigest(data json.RawMessage, view string) ([32]byte, error) {
	if view == activityConsumerResourceView {
		var state activityConsumerResourceResult
		if err := json.Unmarshal(data, &state); err != nil {
			return [32]byte{}, err
		}
		if state.Lag != nil {
			state.Lag.ObservedAt = time.Time{}
			state.Lag.SecondsSinceAck = nil
			state.Lag.AckTimeInFuture = false
		}
		var err error
		data, err = json.Marshal(state)
		if err != nil {
			return [32]byte{}, err
		}
	}
	return resourceSemanticDigest(data, time.Now())
}

func retryResourceSnapshot(err error) bool {
	if requestBusy(err) {
		return true
	}
	var conflict *inputError
	return errors.As(err, &conflict) && (conflict.code == "checkpoint_conflict" || conflict.code == "consumer_generation_conflict")
}
