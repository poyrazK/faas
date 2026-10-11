package api

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

const RealtimeActivityScopePrefix = "__activity."

func validActivityScopePart(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00/?#\r\n")
}

// RealtimeActivityScopeChannel returns the canonical private subscription channel.
func RealtimeActivityScopeChannel(parent, scope string) (string, error) {
	if !validActivityScopePart(parent) || strings.HasPrefix(parent, RealtimeActivityScopePrefix) || !validActivityScopePart(scope) || len(scope) > 64 {
		return "", fmt.Errorf("invalid activity parent or scope")
	}
	channel := RealtimeActivityScopePrefix + base64.RawURLEncoding.EncodeToString([]byte(parent)) + "." + base64.RawURLEncoding.EncodeToString([]byte(scope))
	if len(channel) > 256 {
		return "", fmt.Errorf("encoded activity channel exceeds 256 bytes")
	}
	return channel, nil
}

// ParseRealtimeActivityScope accepts ordinary channels and strictly decodes reserved ones.
func ParseRealtimeActivityScope(channel string) (parent, scope string, err error) {
	if !strings.HasPrefix(channel, RealtimeActivityScopePrefix) {
		return "", "", nil
	}
	parts := strings.Split(strings.TrimPrefix(channel, RealtimeActivityScopePrefix), ".")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid activity scope channel")
	}
	p, e := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if e != nil {
		return "", "", e
	}
	s, e := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if e != nil {
		return "", "", e
	}
	canonical, e := RealtimeActivityScopeChannel(string(p), string(s))
	if e != nil || canonical != channel {
		return "", "", fmt.Errorf("noncanonical activity scope channel")
	}
	return string(p), string(s), nil
}
