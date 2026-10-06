package api

import (
	"encoding/json"
	"errors"
)

var ErrWorkflowInputLimit = errors.New("resolved workflow input exceeds the size limit")

// Count the encoded expansion without allocating the expanded JSON document.
// References share decoded values; traversal stops as soon as the budget ends.
func boundedWorkflowJSONSize(value any, remaining int64) error {
	return consumeWorkflowJSONSize(value, &remaining)
}

func consumeWorkflowJSONSize(value any, remaining *int64) error {
	var visit func(any) error
	consume := func(n int64) error {
		*remaining -= n
		if *remaining < 0 {
			return ErrWorkflowInputLimit
		}
		return nil
	}
	visit = func(value any) error {
		switch item := value.(type) {
		case []any:
			if err := consume(2 + int64(max(0, len(item)-1))); err != nil {
				return err
			}
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		case map[string]any:
			if err := consume(2 + int64(max(0, len(item)-1))); err != nil {
				return err
			}
			for key, child := range item {
				if err := visit(key); err != nil {
					return err
				}
				if err := consume(1); err != nil {
					return err
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			return consume(int64(len(encoded)))
		}
		return nil
	}
	return visit(value)
}
