package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

const ManagedRealtimeReducerMaxConditions = 16

type ManagedRealtimeConditionConflict struct {
	Key, Field                string
	Item, Condition           int
	CurrentVersion            int64
	EntityExists, FieldExists bool
}

func (e *ManagedRealtimeConditionConflict) Error() string {
	return fmt.Sprintf("entity %q: condition %d on field %q failed", e.Key, e.Condition, e.Field)
}
func checkReducerConditions(raw json.RawMessage, key string, current map[string]json.RawMessage, version int64, item int) error {
	if len(raw) == 0 {
		return nil
	}
	var conditions []struct {
		Field  string          `json:"field"`
		Equals json.RawMessage `json:"equals"`
		Absent json.RawMessage `json:"absent"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	invalid := func() error {
		return &ManagedRealtimeReducerError{Item: item, Reason: "conditions must contain 1..16 objects with field and exactly one of equals or absent:true"}
	}
	if decoder.Decode(&conditions) != nil || len(conditions) < 1 || len(conditions) > ManagedRealtimeReducerMaxConditions {
		return invalid()
	}
	// Validate all predicates before evaluating, so malformed requests are always 400.
	for _, condition := range conditions {
		if !reducerKeyValid(condition.Field) || (len(condition.Equals) == 0) == (len(condition.Absent) == 0) || (len(condition.Absent) > 0 && string(condition.Absent) != "true") {
			return invalid()
		}
	}
	for index, condition := range conditions {
		actual, exists := current[condition.Field]
		matches := !exists
		if len(condition.Equals) > 0 {
			matches = false
			if exists {
				values, ok := reducerArrayValues([]json.RawMessage{actual, condition.Equals})
				if !ok {
					return invalid()
				}
				matches = reflect.DeepEqual(values[0], values[1])
			}
		}
		if !matches {
			return &ManagedRealtimeConditionConflict{Key: key, Field: condition.Field, Item: item, Condition: index, CurrentVersion: version, EntityExists: current != nil, FieldExists: exists}
		}
	}
	return nil
}
