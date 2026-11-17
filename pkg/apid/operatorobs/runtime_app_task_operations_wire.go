package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeAppTaskOperations) UnmarshalJSON(body []byte) error {
	type wire RuntimeAppTaskOperations
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "operations", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("incomplete operation evidence")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected"); !ok {
		return errors.New("incomplete operation counts")
	}
	var operations, items []json.RawMessage
	if json.Unmarshal(root["operations"], &operations) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid operation arrays")
	}
	for _, raw := range operations {
		if _, ok := runtimeRequiredJSONFields(raw, "operation", "increased", "unchanged", "baseline", "unknown", "error_increases", "cancellations", "lease_losses", "capacity_refusals"); !ok {
			return errors.New("incomplete operation summary")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "process", "signals")
		if !ok {
			return errors.New("incomplete operation item")
		}
		var signals []json.RawMessage
		if json.Unmarshal(item["signals"], &signals) != nil {
			return errors.New("invalid operation signals")
		}
		for _, raw := range signals {
			s, ok := runtimeRequiredJSONFields(raw, "operation", "outcome", "state")
			if !ok {
				return errors.New("incomplete operation signal")
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
	*h = RuntimeAppTaskOperations(decoded)
	return nil
}
