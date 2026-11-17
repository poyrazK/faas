package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// QueueOrderedWorkPolicy is the built-in work policy a group_key selects
// (workpolicy.Ordered; ADR-20261112193306).
const QueueOrderedWorkPolicy = "gregale-ordered"

// GroupKeyWork returns the work selector a queue message's group_key stands
// for, or nil without one. A message carries a group_key or work, not both.
func GroupKeyWork(groupKey string, work *InvokeWork) (*InvokeWork, error) {
	if groupKey == "" {
		return work, nil
	}
	if work != nil {
		return nil, errors.New("group_key and work cannot be combined")
	}
	if len(groupKey) > QueueGroupKeyMaxBytes || !utf8.ValidString(groupKey) || strings.IndexFunc(groupKey, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("group_key must be 1-%d bytes of printable UTF-8", QueueGroupKeyMaxBytes)
	}
	key, err := json.Marshal(groupKey)
	if err != nil {
		return nil, err
	}
	return &InvokeWork{Policy: QueueOrderedWorkPolicy, Key: key}, nil
}
