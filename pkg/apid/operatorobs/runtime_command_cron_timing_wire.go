package operatorobs

import (
	"bytes"
	"encoding/json"
	"errors"
)

func (h *RuntimeCommandCronTiming) UnmarshalJSON(body []byte) error {
	type wire RuntimeCommandCronTiming
	var decoded wire
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	root, ok := runtimeRequiredJSONFields(body, "scope", "state", "counts", "signals", "incomplete", "items", "omitted_items")
	if !ok {
		return errors.New("incomplete cron timing evidence")
	}
	if _, ok := runtimeRequiredJSONFields(root["counts"], "expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected"); !ok {
		return errors.New("incomplete cron timing counts")
	}
	var operations, items []json.RawMessage
	if json.Unmarshal(root["signals"], &operations) != nil || json.Unmarshal(root["items"], &items) != nil {
		return errors.New("invalid cron timing arrays")
	}
	for _, raw := range operations {
		if _, ok := runtimeRequiredJSONFields(raw, "signal", "increased", "unchanged", "baseline", "unknown"); !ok {
			return errors.New("incomplete cron timing summary")
		}
	}
	for _, raw := range items {
		item, ok := runtimeRequiredJSONFields(raw, "job", "process", "signals")
		if !ok {
			return errors.New("incomplete cron timing item")
		}
		var signals []json.RawMessage
		if json.Unmarshal(item["signals"], &signals) != nil {
			return errors.New("invalid cron timing signals")
		}
		for _, raw := range signals {
			s, ok := runtimeRequiredJSONFields(raw, "signal", "state")
			if !ok {
				return errors.New("incomplete cron timing signal")
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
	*h = RuntimeCommandCronTiming(decoded)
	return nil
}
