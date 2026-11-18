package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimePostCaptureHealth) UnmarshalJSON(body []byte) error {
	type wire RuntimePostCaptureHealth
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "classes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("invalid post_capture evidence shape")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected", "nonempty_views", "empty_views", "unknown_age_views", "unsuccessful_collections", "enabled_views", "disabled_views"); !ok {
		return errors.New("incomplete post_capture counts")
	}
	var classes, items []json.RawMessage
	if json.Unmarshal(root["classes"], &classes) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid post_capture arrays")
	}
	for _, raw := range classes {
		if _, ok := runtimeRequiredJSONFields(raw, "class", "nonempty_views", "empty_views", "unknown_age_views"); !ok {
			return errors.New("incomplete class counts")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "state", "collection", "observation", "classes")
		if !ok {
			return errors.New("incomplete post_capture item")
		}
		for _, c := range []struct {
			name   string
			fields []string
		}{{"collection", []string{"success"}}, {"observation", []string{"enabled"}}} {
			s, ok := runtimeRequiredJSONFields(item[c.name], "state")
			if !ok {
				return errors.New("invalid post_capture component")
			}
			if value, exists := s["values"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(value, c.fields...); !ok {
					return errors.New("incomplete post_capture values")
				}
			}
		}
		var values []json.RawMessage
		if json.Unmarshal(item["classes"], &values) != nil {
			return errors.New("invalid post_capture classes")
		}
		for _, raw := range values {
			p, ok := runtimeRequiredJSONFields(raw, "class", "state")
			if !ok {
				return errors.New("incomplete post_capture class")
			}
			if v, exists := p["values"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				if _, ok := runtimeRequiredJSONFields(v, "pending", "unknown_age"); !ok {
					return errors.New("incomplete class values")
				}
			}
		}
	}
	*h = RuntimePostCaptureHealth(decoded)
	return nil
}
