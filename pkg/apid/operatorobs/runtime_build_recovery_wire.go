package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeBuildRecoveryHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimeBuildRecoveryHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid build recovery shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "nonempty_views", "empty_views", "running_views", "cleanup_views", "retried_cleanup_views", "unsuccessful_collections"); !ok {
		return errors.New("incomplete build recovery counts")
	}
	var items []json.RawMessage
	if json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid build recovery items")
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "state", "collection", "running", "cleanup")
		if !ok {
			return errors.New("incomplete build recovery item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"running", []string{"count"}}, {"cleanup", []string{"pending", "retried"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid build recovery component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete build recovery values")
				}
			}
		}
	}
	*h = RuntimeBuildRecoveryHealth(decoded)
	return nil
}
