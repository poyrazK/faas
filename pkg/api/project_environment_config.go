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

var (
	projectEnvironmentConfigKeyRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	projectEnvironmentConfigHashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// ValidProjectEnvironmentConfigHash reports whether hash is a canonical
// SHA-256 digest produced by NormalizeProjectEnvironmentConfig.
func ValidProjectEnvironmentConfigHash(hash string) bool {
	return projectEnvironmentConfigHashRE.MatchString(hash)
}

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

// ProjectEnvironmentConfigEnv returns the configuration entries delivered to a
// project environment's services as environment variables (ADR-1136):
// top-level keys that are valid variable names whose value is a JSON string,
// number or boolean. Strings are delivered as-is, numbers as their JSON text
// and booleans as "true" or "false"; every other entry stays document-only.
// The database trigger that invalidates snapshots applies the same rule.
func ProjectEnvironmentConfigEnv(values json.RawMessage) (map[string]string, error) {
	env := map[string]string{}
	if len(bytes.TrimSpace(values)) == 0 {
		return env, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(values, &object); err != nil {
		return nil, fmt.Errorf("configuration must be a JSON object: %w", err)
	}
	for key, raw := range object {
		if ValidateEnvKey(key) != nil {
			continue
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		switch raw[0] {
		case '"':
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return nil, fmt.Errorf("configuration value for %q: %w", key, err)
			}
			env[key] = text
		case 't', 'f':
			env[key] = string(raw)
		case '{', '[', 'n':
			// Objects, arrays and null stay document-only.
		default:
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			var number json.Number
			if err := dec.Decode(&number); err != nil {
				return nil, fmt.Errorf("configuration value for %q: %w", key, err)
			}
			env[key] = number.String()
		}
	}
	return env, nil
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
