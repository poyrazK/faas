package appstandards

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"slices"

	"github.com/google/uuid"
)

// Parse rejects unknown fields, duplicate keys, unknown rule properties and
// trailing documents before deriving a stable immutable version hash.
func Parse(raw []byte, limits Limits) (Definition, string, error) {
	if limits.DefinitionBytes <= 0 || len(raw) > limits.DefinitionBytes {
		return nil, "", fmt.Errorf("standard definition exceeds configured byte limit")
	}
	var definition Definition
	if err := DecodeStrict(raw, &definition); err != nil {
		return nil, "", err
	}
	return Normalize(definition, limits)
}

// DecodeStrict rejects ambiguous and unsupported properties on standards API
// documents, including duplicate keys which ordinary encoding/json ignores.
func DecodeStrict(raw []byte, target any) error {
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode standard: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("standard must contain one JSON object")
	}
	return nil
}

func Normalize(definition Definition, limits Limits) (Definition, string, error) {
	if len(definition) == 0 {
		return nil, "", fmt.Errorf("standard must contain at least one requirement")
	}
	normal := make(Definition, len(definition))
	for field, rule := range definition {
		if err := validateRule(field, rule); err != nil {
			return nil, "", err
		}
		value, err := normalizeValue(field, rule.Value, limits.SetEntries)
		if err != nil {
			return nil, "", err
		}
		if rule.Override == "" {
			rule.Override = NoOverride
			if rule.Mode == Restricted {
				rule.Override = Narrow
			}
		}
		if rule.Mode != Default && (field == LogDestinations || field == EgressCIDRs || field == TrustedPublishers) && bytes.Equal(value, []byte("[]")) {
			return nil, "", fmt.Errorf("%s cannot have an empty enforced set", field)
		}
		rule.Value = value
		normal[field] = rule
	}
	raw, err := json.Marshal(normal)
	if err != nil {
		return nil, "", fmt.Errorf("encode standard: %w", err)
	}
	if len(raw) > limits.DefinitionBytes {
		return nil, "", fmt.Errorf("standard definition exceeds configured byte limit")
	}
	sum := sha256.Sum256(raw)
	return normal, hex.EncodeToString(sum[:]), nil
}

func validateRule(field Field, rule Rule) error {
	if rule.Mode != Default && rule.Mode != Mandatory && rule.Mode != Restricted {
		return fmt.Errorf("%s: unsupported mode", field)
	}
	if rule.Override != "" && rule.Override != NoOverride && rule.Override != Narrow && rule.Override != Extend {
		return fmt.Errorf("%s: unsupported override", field)
	}
	if rule.Mode == Default && rule.Override != "" && rule.Override != NoOverride {
		return fmt.Errorf("%s: defaults do not declare override constraints", field)
	}
	if rule.Mode == Restricted && rule.Override != "" && rule.Override != Narrow {
		return fmt.Errorf("%s: restricted requirements permit narrowing only", field)
	}
	if rule.Override == Extend && (field != LogDestinations || rule.Mode != Mandatory) {
		return fmt.Errorf("%s: only mandatory logging destinations permit extension", field)
	}
	if rule.Mode == Restricted && field == LogDestinations {
		return fmt.Errorf("logging destinations must be default or mandatory")
	}
	if rule.Override == Narrow && field == LogDestinations {
		return fmt.Errorf("required logging destinations cannot be narrowed")
	}
	return nil
}

func normalizeValue(field Field, raw json.RawMessage, maxEntries int) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%s requires a non-null value", field)
	}
	switch field {
	case RequireSigned:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s requires a boolean", field)
		}
		return json.Marshal(value)
	case SecurityPolicy:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || policyRank(value) < 0 {
			return nil, fmt.Errorf("security_policy must be off, warn, or enforce")
		}
		return json.Marshal(value)
	case LogDestinations, TrustedPublishers, EgressCIDRs:
		return normalizeStrings(field, raw, maxEntries)
	case EgressExtraPorts:
		var values []int
		if err := json.Unmarshal(raw, &values); err != nil || values == nil || len(values) > maxEntries {
			return nil, fmt.Errorf("%s requires a bounded integer array", field)
		}
		for _, value := range values {
			if value < 1 || value > 65535 {
				return nil, fmt.Errorf("%s contains an invalid port", field)
			}
		}
		slices.Sort(values)
		return json.Marshal(slices.Compact(values))
	default:
		return nil, fmt.Errorf("unsupported standard field %q", field)
	}
}

func normalizeStrings(field Field, raw json.RawMessage, maxEntries int) (json.RawMessage, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || values == nil || len(values) > maxEntries {
		return nil, fmt.Errorf("%s requires a bounded string array", field)
	}
	for i, value := range values {
		if field == EgressCIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Addr().Is4In6() {
				return nil, fmt.Errorf("egress_cidrs contains an invalid CIDR")
			}
			values[i] = prefix.Masked().String()
		} else {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				return nil, fmt.Errorf("%s requires nonzero resource UUIDs", field)
			}
			values[i] = id.String()
		}
	}
	slices.Sort(values)
	return json.Marshal(slices.Compact(values))
}

func policyRank(value string) int {
	switch value {
	case "off":
		return 0
	case "warn":
		return 1
	case "enforce":
		return 2
	default:
		return -1
	}
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSON(dec); err != nil {
		return fmt.Errorf("invalid standard JSON: %w", err)
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate or invalid object key")
			}
			seen[key] = true
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}
