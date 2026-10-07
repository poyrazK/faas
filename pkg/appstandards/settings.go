package appstandards

import (
	"bytes"
	"fmt"
	"time"
)

// ParseSettings accepts a complete local-intent object. An empty object means
// inherit every field; null values, duplicates and unknown fields are refused.
func ParseSettings(raw []byte, limits Limits) (Settings, error) {
	if limits.DefinitionBytes <= 0 || len(raw) > limits.DefinitionBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("invalid application standard local settings")
	}
	var settings Settings
	if err := DecodeStrict(raw, &settings); err != nil {
		return nil, err
	}
	resolved, err := Resolve(nil, nil, settings, nil, time.Time{}, limits)
	return resolved.Values, err
}
