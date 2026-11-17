package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeAppTaskResults) UnmarshalJSON(body []byte) error {
	type wire RuntimeAppTaskResults
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "outcomes", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("incomplete result evidence")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected"); !ok {
		return errors.New("incomplete result counts")
	}
	var operations, items []json.RawMessage
	if json.Unmarshal(root["outcomes"], &operations) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid result arrays")
	}
	for _, raw := range operations {
		if !runtimeAppTaskResultDimensionJSONValid(raw) {
			return errors.New("invalid result dimension")
		}
		if _, ok := runtimeRequiredJSONFields(raw, "source", "outcome", "increased", "unchanged", "baseline", "unknown"); !ok {
			return errors.New("incomplete result summary")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "process", "signals")
		if !ok {
			return errors.New("incomplete result item")
		}
		var signals []json.RawMessage
		if json.Unmarshal(item["signals"], &signals) != nil {
			return errors.New("invalid result signals")
		}
		for _, raw := range signals {
			if !runtimeAppTaskResultDimensionJSONValid(raw) {
				return errors.New("invalid result dimension")
			}
			s, ok := runtimeRequiredJSONFields(raw, "source", "outcome", "state")
			if !ok {
				return errors.New("incomplete result signal")
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
	*h = RuntimeAppTaskResults(decoded)
	return nil
}

func runtimeAppTaskResultDimensionJSONValid(body []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return false
	}
	var source string
	if json.Unmarshal(raw["source"], &source) != nil {
		return false
	}
	if source == "completion" {
		_, ok := runtimeRequiredJSONFields(body, "kind")
		return ok
	}
	_, exists := raw["kind"]
	return source == "recovery" && !exists
}
