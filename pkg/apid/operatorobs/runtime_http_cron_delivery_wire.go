package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeHTTPCronDelivery) UnmarshalJSON(body []byte) error {
	type wire RuntimeHTTPCronDelivery
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "outcomes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("incomplete HTTP cron delivery evidence")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected"); !ok {
		return errors.New("incomplete HTTP cron delivery counts")
	}
	var operations, items []json.RawMessage
	if json.Unmarshal(root["outcomes"], &operations) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid HTTP cron delivery arrays")
	}
	for _, raw := range operations {
		if _, ok := runtimeRequiredJSONFields(raw, "outcome", "increased", "unchanged", "baseline", "unknown"); !ok {
			return errors.New("incomplete HTTP cron delivery summary")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "process", "signals")
		if !ok {
			return errors.New("incomplete HTTP cron delivery item")
		}
		var signals []json.RawMessage
		if json.Unmarshal(item["signals"], &signals) != nil {
			return errors.New("invalid HTTP cron delivery signals")
		}
		for _, raw := range signals {
			s, ok := runtimeRequiredJSONFields(raw, "outcome", "state")
			if !ok {
				return errors.New("incomplete HTTP cron delivery signal")
			}
			for _, name := range []string{"current", "previous"} {
				if p, exists := s[name]; exists && !bytes.Equal(bytes.TrimSpace(p), []byte("null")) {
					if _, ok := runtimeRequiredJSONFields(p, "value", "observed_at"); !ok {
						return errors.New("incomplete counter point")
					}
				}
			}
		}
	}
	*h = RuntimeHTTPCronDelivery(decoded)
	return nil
}
