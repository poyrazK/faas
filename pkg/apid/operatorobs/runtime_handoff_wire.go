package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeHandoffHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeHandoffHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "state", "counts", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid handoff evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "waiting_scopes", "empty_scopes", "unsuccessful_collections", "working_recovery_scopes", "waiting_recovery_scopes", "stopping_recovery_scopes", "not_started_recovery_scopes"); !ok {
		return errors.New("incomplete handoff counts")
	}
	var items []json.RawMessage
	if json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid handoff items")
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "state", "collection", "backlog", "recovery")
		if !ok {
			return errors.New("incomplete handoff item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"backlog", []string{"pending", "oldest_wait_seconds"}}, {"recovery", []string{"enabled", "inflight"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid handoff component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete handoff values")
				}
			}
		}
	}
	*h = RuntimeHandoffHealth(decoded)
	return nil
}
