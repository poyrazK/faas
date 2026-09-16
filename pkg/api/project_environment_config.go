package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// MaxProjectEnvironmentConfigBytes bounds the non-secret configuration
// document accepted for one environment. The document is intentionally small
// because it is carried in plan previews and compared before apply.
const MaxProjectEnvironmentConfigBytes = 64 << 10

var projectEnvironmentConfigKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

// NormalizeProjectEnvironmentConfig validates and canonicalizes a project
// environment's non-secret configuration. Canonical bytes are stable across
// whitespace and object-key ordering, so the returned hash can safely bind a
// plan to the exact configuration that was previewed.
func NormalizeProjectEnvironmentConfig(raw []byte) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > MaxProjectEnvironmentConfigBytes {
		return nil, "", fmt.Errorf("configuration exceeds %d bytes", MaxProjectEnvironmentConfigBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, "", fmt.Errorf("configuration must be valid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, "", fmt.Errorf("configuration must contain one JSON value")
		}
		return nil, "", fmt.Errorf("configuration must contain one JSON value: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("configuration must be a JSON object")
	}
	for key := range object {
		if !projectEnvironmentConfigKeyRE.MatchString(key) {
			return nil, "", fmt.Errorf("invalid configuration key %q", key)
		}
		if projectEnvironmentConfigKeyIsSensitive(key) {
			return nil, "", fmt.Errorf("configuration key %q is reserved for secrets", key)
		}
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize configuration: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return json.RawMessage(canonical), hex.EncodeToString(sum[:]), nil
}

// EmptyProjectEnvironmentConfigHash is the hash used for an environment
// which has not received an explicit configuration version yet.
func EmptyProjectEnvironmentConfigHash() string {
	_, hash, _ := NormalizeProjectEnvironmentConfig(nil)
	return hash
}

func projectEnvironmentConfigKeyIsSensitive(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '.'
	})
	for i, part := range parts {
		switch part {
		case "secret", "secrets", "password", "token", "credential", "credentials":
			return true
		}
		if part == "key" && i > 0 && (parts[i-1] == "api" || parts[i-1] == "access" || parts[i-1] == "private") {
			return true
		}
	}
	return false
}
