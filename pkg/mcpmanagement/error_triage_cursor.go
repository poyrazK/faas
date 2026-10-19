package mcpmanagement

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

// This bounded context envelope prevents accidental selector changes. It is
// data, not an authorization token; every page rechecks live app consent.
// Native is opaque and is forwarded to the native API unchanged.
type errorPageCursor struct {
	Version     int    `json:"v"`
	Kind        string `json:"kind"`
	App         string `json:"app"`
	AppID       string `json:"app_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	WindowStart string `json:"window_start,omitempty"`
	WindowEnd   string `json:"window_end,omitempty"`
	Limit       int    `json:"limit"`
	Native      string `json:"native"`
}

func (m *management) safeErrorCursor(s string) bool {
	return requestFilterText(s, api.MCPErrorCursorMaxBytes) && !strings.ContainsFunc(s, unicode.IsSpace) && m.clean(s) == s
}

func (m *management) readErrorCursor(raw string, want errorPageCursor) (errorPageCursor, error) {
	if raw == "" {
		return want, nil
	}
	fail := invalid("invalid_argument", "invalid continuation or changed selectors; copy next_arguments unchanged")
	encoded, ok := strings.CutPrefix(raw, "e1.")
	if !ok || !m.safeErrorCursor(raw) {
		return want, fail
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return want, fail
	}
	var c errorPageCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.Version != want.Version || c.Kind != want.Kind || c.App != want.App || c.Fingerprint != want.Fingerprint || c.Since != want.Since || c.Until != want.Until || c.Limit != want.Limit || validateSegment(c.AppID, "app_id") != nil || c.Native == "" || !m.safeErrorCursor(c.Native) {
		return want, fail
	}
	if c.Kind == "groups" {
		start, se := errorTime(c.WindowStart)
		end, ee := errorTime(c.WindowEnd)
		if !se || !ee || !start.Before(end) {
			return want, fail
		}
	} else if c.WindowStart != "" || c.WindowEnd != "" {
		return want, fail
	}
	return c, nil
}

func (m *management) writeErrorCursor(c errorPageCursor, next string) (string, error) {
	if next == "" {
		return "", nil
	}
	if !m.safeErrorCursor(next) || next == c.Native {
		return "", invalid("invalid_response", "native error cursor is unsafe or made no progress")
	}
	c.Native = next
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	raw := "e1." + base64.RawURLEncoding.EncodeToString(data)
	if !m.safeErrorCursor(raw) {
		return "", invalid("invalid_response", "error continuation exceeds its output bound")
	}
	return raw, nil
}
