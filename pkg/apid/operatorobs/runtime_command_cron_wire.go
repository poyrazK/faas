package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeCommandCronHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeCommandCronHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "classes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid command_cron evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "nonempty_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes", "due_scopes", "inactive_scopes", "sweep_running_scopes", "sweep_idle_scopes", "sweep_never_started_scopes", "sweep_never_succeeded_scopes"); !ok {
		return errors.New("incomplete command_cron counts")
	}
	var classes, items []json.RawMessage
	if json.Unmarshal(root["classes"], &classes) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid command_cron arrays")
	}
	for _, raw := range classes {
		if _, ok := runtimeRequiredJSONFields(raw, "class", "nonempty_scopes", "empty_scopes", "unknown_age_scopes"); !ok {
			return errors.New("incomplete class counts")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "state", "collection", "observation", "sweep", "classes")
		if !ok {
			return errors.New("incomplete command_cron item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"observation", []string{"enabled"}}, {"sweep", []string{"running"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid command_cron component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete command_cron values")
				}
			}
		}
		var values []json.RawMessage
		if json.Unmarshal(item["classes"], &values) != nil {
			return errors.New("invalid command_cron classes")
		}
		for _, raw := range values {
			p, ok := runtimeRequiredJSONFields(raw, "class", "state")
			if !ok {
				return errors.New("incomplete command_cron class")
			}
			if v, exists := p["values"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(v, "schedules", "unknown_age"); !ok {
					return errors.New("incomplete class values")
				}
			}
		}
	}
	*h = RuntimeCommandCronHealth(decoded)
	return nil
}
