package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"reflect"
)

var ErrManagedRealtimeScheduleSkipped = errors.New("state: scheduled occurrence skipped")

type ManagedRealtimeScheduleConditionFailure struct {
	Index  int
	Reason string
}

func (e *ManagedRealtimeScheduleConditionFailure) Error() string {
	return fmt.Sprintf("schedule condition %d failed: %s", e.Index, e.Reason)
}

type realtimeScheduleCondition struct {
	Key    string          `json:"key"`
	Field  string          `json:"field,omitempty"`
	Exists json.RawMessage `json:"exists,omitempty"`
	Equals json.RawMessage `json:"equals,omitempty"`
	LT     json.RawMessage `json:"lt,omitempty"`
	LTE    json.RawMessage `json:"lte,omitempty"`
	GT     json.RawMessage `json:"gt,omitempty"`
	GTE    json.RawMessage `json:"gte,omitempty"`
}

func decodeScheduleConditions(raw json.RawMessage) ([]realtimeScheduleCondition, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) || len(raw) > 4096 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	var conditions []realtimeScheduleCondition
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&conditions) != nil || len(conditions) < 1 || len(conditions) > 16 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	for _, condition := range conditions {
		if !reducerKeyValid(condition.Key) {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
		count := 0
		for _, value := range []json.RawMessage{condition.Exists, condition.Equals, condition.LT, condition.LTE, condition.GT, condition.GTE} {
			if len(value) > 0 {
				count++
			}
		}
		if count != 1 {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
		if len(condition.Exists) > 0 {
			if condition.Field != "" || (string(condition.Exists) != "true" && string(condition.Exists) != "false") {
				return nil, ErrManagedRealtimeHistoryInvalid
			}
			continue
		}
		if !reducerKeyValid(condition.Field) {
			return nil, ErrManagedRealtimeHistoryInvalid
		}
		for _, value := range []json.RawMessage{condition.LT, condition.LTE, condition.GT, condition.GTE} {
			if len(value) > 0 {
				if _, ok := reducerCounterInteger(value); !ok {
					return nil, ErrManagedRealtimeHistoryInvalid
				}
			}
		}
	}
	return conditions, nil
}
func checkScheduleConditions(schedule ManagedRealtimeSchedule, reducer *ManagedRealtimeReducerState) error {
	conditions, err := decodeScheduleConditions(schedule.Conditions)
	if err != nil {
		return err
	}
	if len(conditions) == 0 {
		return nil
	}
	fail := func(index int, reason string) error { return &ManagedRealtimeScheduleConditionFailure{index, reason} }
	if reducer == nil {
		return fail(0, "reducer_missing")
	}
	entities, err := decodeReducerEntities(reducer.Entities)
	if err != nil {
		return err
	}
	for index, condition := range conditions {
		entity, exists := entities[condition.Key]
		if len(condition.Exists) > 0 {
			if exists != (string(condition.Exists) == "true") {
				return fail(index, "entity_existence_mismatch")
			}
			continue
		}
		if !exists {
			return fail(index, "entity_missing")
		}
		actual, exists := entity[condition.Field]
		if !exists {
			return fail(index, "field_missing")
		}
		if len(condition.Equals) > 0 {
			values, ok := reducerArrayValues([]json.RawMessage{actual, condition.Equals})
			if !ok {
				return ErrManagedRealtimeHistoryInvalid
			}
			if !reflect.DeepEqual(values[0], values[1]) {
				return fail(index, "value_mismatch")
			}
			continue
		}
		number, ok := reducerCounterInteger(actual)
		if !ok {
			return fail(index, "field_not_integer")
		}
		match := true
		if len(condition.LT) > 0 {
			threshold, _ := reducerCounterInteger(condition.LT)
			match = number < threshold
		}
		if len(condition.LTE) > 0 {
			threshold, _ := reducerCounterInteger(condition.LTE)
			match = number <= threshold
		}
		if len(condition.GT) > 0 {
			threshold, _ := reducerCounterInteger(condition.GT)
			match = number > threshold
		}
		if len(condition.GTE) > 0 {
			threshold, _ := reducerCounterInteger(condition.GTE)
			match = number >= threshold
		}
		if !match {
			return fail(index, "threshold_not_met")
		}
	}
	return nil
}

func nullableScheduleConditions(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func saveSkippedRealtimeSchedulePG(ctx context.Context, tx pgx.Tx, row ManagedRealtimeSchedule) error {
	_, err := tx.Exec(ctx, `update managed_realtime_schedules set status=$4,attempts=$5,cycle_attempts=$6,next_attempt_at=null,last_attempt_at=$7,version=$8,updated_at=$7,deliver_at=$9,occurrence=$10,skipped_occurrences=$11,skip_reason=$12,last_error=$13 where endpoint_id=$1 and channel=$2 and schedule_id=$3`, row.EndpointID, row.Channel, row.ID, row.Status, row.Attempts, row.CycleAttempts, row.LastAttemptAt, row.Version, row.DeliverAt, row.Occurrence, row.SkippedOccurrences, row.SkipReason, row.LastError)
	return err
}
