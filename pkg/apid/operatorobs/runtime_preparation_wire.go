package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimePreparationHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimePreparationHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "state", "counts", "phases", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid preparation evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "waiting_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes"); !ok {
		return errors.New("incomplete preparation counts")
	}
	var phases, items []json.RawMessage
	if json.Unmarshal(root["phases"], &phases) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid preparation arrays")
	}
	for _, raw := range phases {
		if _, ok := runtimeRequiredJSONFields(raw, "phase", "waiting_scopes", "empty_scopes", "unknown_age_scopes"); !ok {
			return errors.New("incomplete phase counts")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "state", "collection", "observation", "phases")
		if !ok {
			return errors.New("incomplete preparation item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"observation", []string{"enabled"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid preparation component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete preparation values")
				}
			}
		}
		var values []json.RawMessage
		if json.Unmarshal(item["phases"], &values) != nil {
			return errors.New("invalid preparation phases")
		}
		for _, raw := range values {
			p, ok := runtimeRequiredJSONFields(raw, "phase", "state")
			if !ok {
				return errors.New("incomplete preparation phase")
			}
			if v, exists := p["values"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(v, "pending", "unknown_age"); !ok {
					return errors.New("incomplete phase values")
				}
			}
		}
	}
	*h = RuntimePreparationHealth(decoded)
	return nil
}
