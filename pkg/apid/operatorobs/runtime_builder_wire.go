package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Required zero-valued fields must remain distinguishable from absent/null
// JSON. Optional malformed diagnostics are discarded by the owning reader.
func (h *RuntimeBuilderHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeBuilderHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "state", "counts", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid builder evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "overdue_workers", "waiting_workers", "working_workers", "draining_workers", "waiting_queues", "saturated_slots"); !ok {
		return errors.New("incomplete builder counts")
	}
	var items []json.RawMessage
	if json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid builder items")
	}
	for _, body := range items {
		item, ok := runtimeRequiredJSONFields(body, "state", "worker", "slots", "queues")
		if !ok || !runtimeBuilderWireComponent(item["worker"], []string{"enabled", "inflight", "started_at", "interval_seconds", "busy_allowance_seconds", "progress_age_seconds", "progress_allowance_seconds"}) || !runtimeBuilderWireComponent(item["slots"], []string{"used", "limit"}) {
			return errors.New("incomplete builder component")
		}
		var queues []json.RawMessage
		if json.Unmarshal(item["queues"], &queues) != nil {
			return errors.New("invalid builder queues")
		}
		for _, queue := range queues {
			if _, ok := runtimeRequiredJSONFields(queue, "queue"); !ok || !runtimeBuilderWireComponent(queue, []string{"enabled", "waiting", "active", "capacity", "workers", "oldest_wait_seconds"}) {
				return errors.New("incomplete builder queue")
			}
		}
	}
	*h = RuntimeBuilderHealth(decoded)
	return nil
}

func runtimeBuilderWireComponent(body []byte, fields []string) bool {
	component, ok := runtimeRequiredJSONFields(body, "state")
	if !ok {
		return false
	}
	if value, exists := component["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		_, ok = runtimeRequiredJSONFields(value, fields...)
		return ok
	}
	return true
}

func runtimeRequiredJSONFields(body []byte, fields ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		return nil, false
	}
	for _, key := range fields {
		value, exists := object[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return object, true
}
