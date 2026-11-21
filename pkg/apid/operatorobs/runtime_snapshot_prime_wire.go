package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeSnapshotPrimeHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeSnapshotPrimeHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "state", "counts", "classes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid snapshot_prime evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "nonempty_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes"); !ok {
		return errors.New("incomplete snapshot_prime counts")
	}
	var classes, items []json.RawMessage
	if json.Unmarshal(root["classes"], &classes) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid snapshot_prime arrays")
	}
	for _, raw := range classes {
		if _, ok := runtimeRequiredJSONFields(raw, "class", "nonempty_scopes", "empty_scopes", "unknown_age_scopes"); !ok {
			return errors.New("incomplete class counts")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "state", "collection", "observation", "classes")
		if !ok {
			return errors.New("incomplete snapshot_prime item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"observation", []string{"enabled"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid snapshot_prime component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete snapshot_prime values")
				}
			}
		}
		var values []json.RawMessage
		if json.Unmarshal(item["classes"], &values) != nil {
			return errors.New("invalid snapshot_prime classes")
		}
		for _, raw := range values {
			p, ok := runtimeRequiredJSONFields(raw, "class", "state")
			if !ok {
				return errors.New("incomplete snapshot_prime class")
			}
			if v, exists := p["values"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(v, "pending", "unknown_age"); !ok {
					return errors.New("incomplete class values")
				}
			}
		}
	}
	*h = RuntimeSnapshotPrimeHealth(decoded)
	return nil
}
