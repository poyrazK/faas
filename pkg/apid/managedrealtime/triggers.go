package managedrealtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const realtimeTriggerDocs = "https://gregale.dev/docs/realtime#function-triggers"

func realtimeTriggerProblem(status int, code, title, detail string) *api.Problem {
	return api.NewProblem(status, code, title, detail).WithDocs(realtimeTriggerDocs)
}

// PrepareRealtimeTrigger validates a kind=realtime trigger before it is
// created (ADR-1322): the retained preview, the delivery settings the group
// owns, endpoint ownership, and the group's lease and binding. Binding runs
// again atomically after creation.
func (s *Service) PrepareRealtimeTrigger(ctx context.Context, acct state.Account, req *api.CreateTriggerRequest) (api.RealtimeTriggerConfig, *api.Problem) {
	var cfg api.RealtimeTriggerConfig
	if !s.PreviewVisible(acct.ID) {
		// Outside the preview rollout the kind does not exist.
		return cfg, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request",
			"unknown kind: must be one of cron|kafka|nats|redis_streams|sqs_compat|queue")
	}
	if !s.HistoryPreviewEnabled {
		return cfg, realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Realtime triggers unavailable",
			"realtime triggers require the retained-realtime preview")
	}
	if p := rejectGroupOwnedTriggerFields(req); p != nil {
		return cfg, p
	}
	decoder := json.NewDecoder(bytes.NewReader(req.Config))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", "realtime config: "+err.Error())
	}
	if err := cfg.Validate(); err != nil {
		return cfg, realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", err.Error())
	}
	return cfg, s.checkRealtimeTriggerGroup(ctx, acct, cfg)
}

func rejectGroupOwnedTriggerFields(req *api.CreateTriggerRequest) *api.Problem {
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"batch_window_ms", req.BatchWindowMs != nil},
		{"max_attempts", req.MaxAttempts != nil},
		{"retry_policy", req.RetryPolicy != nil},
		{"broker_poison_strategy", req.BrokerPoisonStrategy != nil},
		{"filter_criteria", req.FilterCriteria != nil},
	} {
		if f.set {
			return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Setting owned by the consumer group",
				f.name+" is not supported for kind=realtime; configure it on the consumer group")
		}
	}
	return checkRealtimeTriggerBatchSize(req.Delivery, req.BatchSizeMax)
}

// checkRealtimeTriggerBatchSize allows several messages per invocation only
// with batch delivery (ADR-1321), up to the most one group claim returns.
func checkRealtimeTriggerBatchSize(delivery *string, batchSizeMax *int) *api.Problem {
	if batchSizeMax == nil || *batchSizeMax <= 1 {
		return nil
	}
	if delivery == nil || *delivery != api.TriggerDeliveryBatch {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "One message per invocation",
			"kind=realtime delivers one message per invocation unless delivery is batch; set delivery: batch or batch_size_max: 1")
	}
	if *batchSizeMax > api.RealtimeConsumerGroupBatchMax {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Batch too large",
			fmt.Sprintf("kind=realtime with delivery: batch receives at most %d messages per invocation", api.RealtimeConsumerGroupBatchMax)).
			WithLimit(api.RealtimeConsumerGroupBatchMax, int64(*batchSizeMax))
	}
	return nil
}

// RealtimeTriggerBatchSize is the claim size for a realtime trigger: one
// message with record delivery, otherwise the requested size or 10.
func RealtimeTriggerBatchSize(delivery string, requested *int) int32 {
	if delivery != api.TriggerDeliveryBatch {
		return 1
	}
	if requested != nil && *requested > 0 {
		return int32(*requested)
	}
	return api.RealtimeTriggerDefaultBatchSize
}

func (s *Service) checkRealtimeTriggerGroup(ctx context.Context, acct state.Account, cfg api.RealtimeTriggerConfig) *api.Problem {
	endpoints, ok := s.Host.Store().(state.ManagedRealtimeEndpointStore)
	groups, groupsOK := s.Host.Store().(state.RealtimeConsumerGroupStore)
	if !ok || !groupsOK {
		return api.ErrCapacity("realtime consumer groups unavailable")
	}
	endpoint, err := endpoints.ManagedRealtimeEndpointByID(ctx, cfg.EndpointID)
	if err != nil || endpoint.AccountID != acct.ID {
		return realtimeTriggerProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "realtime endpoint not found")
	}
	list, err := groups.ListRealtimeConsumerGroups(ctx, cfg.EndpointID, cfg.Channel)
	if err != nil {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", "invalid realtime channel")
	}
	for _, g := range list {
		if g.Name != cfg.Group {
			continue
		}
		if g.LeaseSeconds < api.RealtimeTriggerMinLeaseSeconds {
			return realtimeTriggerProblem(http.StatusUnprocessableEntity, "realtime_group_lease_too_short", "Consumer group lease too short",
				"create the group with lease_seconds of at least the trigger minimum so a wake and invocation fit in one lease").
				WithLimit(api.RealtimeTriggerMinLeaseSeconds, int64(g.LeaseSeconds))
		}
		if g.TriggerID != "" {
			return realtimeTriggerProblem(http.StatusConflict, "realtime_group_bound_to_trigger", "Consumer group already bound",
				"another trigger consumes this group; a group binds to at most one trigger")
		}
		return nil
	}
	return realtimeTriggerProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "realtime consumer group not found")
}

// BindRealtimeTrigger binds the created trigger to its group. A nil Problem
// means the trigger now consumes the group; otherwise the caller deletes the
// trigger it just created.
func (s *Service) BindRealtimeTrigger(ctx context.Context, acct state.Account, triggerID string, cfg api.RealtimeTriggerConfig) *api.Problem {
	binder, ok := s.Host.Store().(state.RealtimeTriggerBindingStore)
	if !ok {
		return api.ErrCapacity("realtime consumer groups unavailable")
	}
	err := binder.BindRealtimeConsumerGroupTrigger(ctx, acct.ID, triggerID, cfg)
	var lease *state.RealtimeTriggerLeaseError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &lease):
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "realtime_group_lease_too_short", "Consumer group lease too short",
			"create the group with lease_seconds of at least the trigger minimum").WithLimit(int64(lease.Limit), int64(lease.Observed))
	case errors.Is(err, state.ErrConflict):
		return realtimeTriggerProblem(http.StatusConflict, "realtime_group_bound_to_trigger", "Consumer group already bound",
			"another trigger consumes this group; a group binds to at most one trigger")
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrRealtimeConsumerGroupInvalid):
		return realtimeTriggerProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "realtime consumer group not found")
	default:
		s.Host.Logger().WarnContext(ctx, "realtime trigger binding failed", "err", err)
		return api.ErrCapacity("could not bind realtime consumer group")
	}
}

// CheckRealtimeTriggerUpdate validates a PATCH of a kind=realtime trigger.
// The binding is immutable: max_concurrency, delivery and batch_size_max may
// change (ADR-1321), and the delivery settings the group owns stay rejected.
func CheckRealtimeTriggerUpdate(current json.RawMessage, currentDelivery string, currentBatchSize int32, req *api.UpdateTriggerRequest) *api.Problem {
	delivery := currentDelivery
	if req.Delivery != nil {
		delivery = *req.Delivery
	}
	batchSize := int(currentBatchSize)
	if req.BatchSizeMax != nil {
		batchSize = *req.BatchSizeMax
	}
	if p := rejectGroupOwnedTriggerFields(&api.CreateTriggerRequest{
		BatchSizeMax: &batchSize, BatchWindowMs: req.BatchWindowMs, MaxAttempts: req.MaxAttempts,
		RetryPolicy: req.RetryPolicy, BrokerPoisonStrategy: req.BrokerPoisonStrategy, FilterCriteria: req.FilterCriteria,
		Delivery: &delivery,
	}); p != nil {
		return p
	}
	if req.Config == nil {
		return nil
	}
	var before api.RealtimeTriggerConfig
	if err := json.Unmarshal(current, &before); err != nil {
		return api.ErrCapacity("realtime trigger config unreadable")
	}
	after := before
	decoder := json.NewDecoder(bytes.NewReader(req.Config))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&after); err != nil {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", "realtime config: "+err.Error())
	}
	if after.EndpointID != before.EndpointID || after.Channel != before.Channel || after.Group != before.Group {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_immutable", "Binding is immutable",
			"endpoint_id, channel and group cannot change; delete the trigger and create a new one")
	}
	if err := after.Validate(); err != nil {
		return realtimeTriggerProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", err.Error())
	}
	return nil
}
