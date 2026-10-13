package api

import "context"

// A proposed acknowledgement removal is delivered to OnProgress before it becomes
// the watch's in-memory state. Callback failure preserves the previous queue.
func pollDeliveryHealthAcknowledgements(ctx context.Context, o EventDeliveryHealthWatchOptions, out *EventDeliveryHealthWatchResult) error {
	if o.PollAcknowledgements == nil {
		return nil
	}
	snapshot := *out.Checkpoint
	snapshot.Conditions = append([]EventDeliveryHealthCondition{}, snapshot.Conditions...)
	snapshot.Pending = append([]EventDeliveryHealthAlert{}, snapshot.Pending...)
	snapshot.AcknowledgedAlerts = append([]EventDeliveryHealthAlert{}, snapshot.AcknowledgedAlerts...)
	acks, err := o.PollAcknowledgements(ctx, snapshot)
	if err == nil {
		var updated EventDeliveryHealthCheckpoint
		updated, err = AcknowledgeEventDeliveryHealthAlerts(*out.Checkpoint, acks)
		if err == nil && len(updated.Pending) != len(out.Checkpoint.Pending) {
			staged := *out
			staged.Checkpoint = &updated
			staged.Alerts = []EventDeliveryHealthAlert{}
			if err = deliveryHealthProgress(o, staged); err != nil {
				out.StopReason = "callback_failure"
				return err
			}
			out.Checkpoint = &updated
		}
	}
	if err != nil {
		out.StopReason = "acknowledgement_failure"
		if ctx.Err() != nil {
			out.StopReason = "cancelled"
			if ctx.Err() == context.DeadlineExceeded {
				out.StopReason = "timeout"
			}
		}
	}
	return err
}
