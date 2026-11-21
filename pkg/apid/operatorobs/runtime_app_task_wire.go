package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeAppTaskHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeAppTaskHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "classes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid app_task evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "nonempty_views", "empty_views", "unknown_age_views", "unsuccessful_collections", "enabled_views", "disabled_views", "dispatch_enabled_views", "dispatch_disabled_views", "runner_running_views", "runner_stopped_views", "occupied_views", "saturated_views"); !ok {
		return errors.New("incomplete app_task counts")
	}
	var classes, items []json.RawMessage
	if json.Unmarshal(root["classes"], &classes) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid app_task arrays")
	}
	for _, raw := range classes {
		if _, ok := runtimeRequiredJSONFields(raw, "kind", "class", "nonempty_views", "empty_views", "unknown_age_views"); !ok {
			return errors.New("incomplete class counts")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "state", "collection", "observation", "dispatch", "classes")
		if !ok {
			return errors.New("incomplete app_task item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"observation", []string{"enabled"}}, {"dispatch", []string{"enabled", "running", "slots_configured", "slots_in_use"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid app_task component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete app_task values")
				}
			}
		}
		var values []json.RawMessage
		if json.Unmarshal(item["classes"], &values) != nil {
			return errors.New("invalid app_task classes")
		}
		for _, raw := range values {
			p, ok := runtimeRequiredJSONFields(raw, "kind", "class", "state")
			if !ok {
				return errors.New("incomplete app_task class")
			}
			if v, exists := p["values"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(v, "pending", "unknown_age"); !ok {
					return errors.New("incomplete class values")
				}
			}
		}
	}
	*h = RuntimeAppTaskHealth(decoded)
	return nil
}
