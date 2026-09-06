package main

import (
	"encoding/json"
	"strings"
)

// appChangedID accepts APID's documented envelope and the legacy bare ID
// emitted by the maintenance-mode trigger. Empty signals a full cache flush.
func appChangedID(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	if json.Valid([]byte(payload)) || strings.HasPrefix(payload, "{") {
		var event struct {
			AppID string `json:"app_id"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			return ""
		}
		return strings.TrimSpace(event.AppID)
	}
	// Malformed JSON must not masquerade as a legacy ID.
	if strings.ContainsAny(payload, "{}[]\" ,:\t\r\n") {
		return ""
	}
	return payload
}
