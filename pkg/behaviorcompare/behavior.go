// Package behaviorcompare compares explicitly selected application observations.
// Evidence is application-reported; it does not attest to unobserved side effects.
package behaviorcompare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// These bounds define the version 1 evidence format, not platform entitlements.
const (
	Version          = 1
	MaxDocumentBytes = 1 << 20
	MaxObservations  = 100
	Provenance       = "application_reported"
)

var (
	keyPattern    = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Spec declares one required observation. Keys identify logical outcomes across runs.
type Spec struct {
	Key  string `json:"key" yaml:"key"`
	Kind string `json:"kind" yaml:"kind"`
}

type Observation struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Document is written by a scenario command to the runner's private output path.
type Document struct {
	Version      int           `json:"version"`
	Observations []Observation `json:"observations"`
}

// Evidence binds the selected values to the declared observer and fixture files.
type Evidence struct {
	Version        int           `json:"version"`
	Status         string        `json:"status"`
	Provenance     string        `json:"provenance"`
	ContractSHA256 string        `json:"contract_sha256"`
	FixtureSHA256  string        `json:"fixture_sha256"`
	Specs          []Spec        `json:"specs"`
	Observations   []Observation `json:"observations,omitempty"`
	Error          string        `json:"error,omitempty"`
}

type Change struct {
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Status string          `json:"status"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type Comparison struct {
	Status         string   `json:"status"`
	Reason         string   `json:"reason,omitempty"`
	Provenance     string   `json:"provenance"`
	ContractSHA256 string   `json:"contract_sha256,omitempty"`
	FixtureSHA256  string   `json:"fixture_sha256,omitempty"`
	Changes        []Change `json:"changes,omitempty"`
}

func ValidateSpecs(specs []Spec) error {
	if len(specs) == 0 || len(specs) > MaxObservations {
		return fmt.Errorf("behavior requires 1 to %d observations", MaxObservations)
	}
	seen := make(map[string]bool, len(specs))
	for i, spec := range specs {
		if !keyPattern.MatchString(spec.Key) || seen[spec.Key] {
			return fmt.Errorf("behavior observation %d has an invalid or duplicate key", i+1)
		}
		seen[spec.Key] = true
		switch spec.Kind {
		case "http", "delivery", "object", "database", "application":
		default:
			return fmt.Errorf("behavior observation %d has an unsupported kind", i+1)
		}
	}
	return nil
}

// Decode rejects undeclared/missing observations, ambiguous JSON, and unknown fields.
// Validation errors never include submitted values.
func Decode(body []byte, specs []Spec) ([]Observation, error) {
	canonical, err := canonicalJSON(body)
	if err != nil {
		return nil, errors.New("behavior output must be one bounded, unambiguous JSON document")
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.Version != Version {
		return nil, errors.New("behavior output must use the version 1 observation format")
	}
	return validateObservations(specs, document.Observations)
}

func validateObservations(specs []Spec, observations []Observation) ([]Observation, error) {
	if err := ValidateSpecs(specs); err != nil {
		return nil, err
	}
	if len(observations) != len(specs) {
		return nil, errors.New("behavior output must include every declared observation exactly once")
	}
	declared := make(map[string]bool, len(specs))
	for _, spec := range specs {
		declared[spec.Key] = true
	}
	result := make([]Observation, 0, len(observations))
	total := 0
	for _, observation := range observations {
		if !declared[observation.Key] {
			return nil, errors.New("behavior output contains a duplicate or undeclared observation")
		}
		delete(declared, observation.Key)
		total += len(observation.Value)
		if total > MaxDocumentBytes {
			return nil, errors.New("behavior observations exceed the format size bound")
		}
		value, err := canonicalJSON(observation.Value)
		if err != nil {
			return nil, errors.New("behavior observation is missing a value or contains invalid JSON")
		}
		result = append(result, Observation{Key: observation.Key, Value: value})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// Compare returns nil only when neither report opts into behavior evidence.
func Compare(before, after *Evidence) *Comparison {
	if before == nil && after == nil {
		return nil
	}
	result := &Comparison{Status: "inconclusive", Provenance: Provenance}
	if before == nil || after == nil {
		result.Reason = "behavior evidence is missing from one report"
		return result
	}
	oldValues, oldErr := validateEvidence(before)
	newValues, newErr := validateEvidence(after)
	if oldErr != nil || newErr != nil {
		result.Reason = "behavior evidence is incomplete or invalid"
		return result
	}
	if before.ContractSHA256 != after.ContractSHA256 || !sameSpecs(before.Specs, after.Specs) {
		result.Reason = "behavior observation contract or observer files changed"
		return result
	}
	if before.FixtureSHA256 != after.FixtureSHA256 {
		result.Reason = "declared behavior fixture files changed"
		return result
	}
	result.Status, result.ContractSHA256, result.FixtureSHA256 = "passed", before.ContractSHA256, before.FixtureSHA256
	kinds := make(map[string]string, len(before.Specs))
	for _, spec := range before.Specs {
		kinds[spec.Key] = spec.Kind
	}
	for i, old := range oldValues {
		current := newValues[i]
		change := Change{Key: old.Key, Kind: kinds[old.Key], Status: "unchanged", Before: old.Value, After: current.Value}
		if !bytes.Equal(old.Value, current.Value) {
			change.Status, result.Status = "changed", "failed"
		}
		result.Changes = append(result.Changes, change)
	}
	return result
}

func validateEvidence(evidence *Evidence) ([]Observation, error) {
	if evidence.Version != Version || evidence.Status != "complete" || evidence.Error != "" || evidence.Provenance != Provenance ||
		!digestPattern.MatchString(evidence.ContractSHA256) || !digestPattern.MatchString(evidence.FixtureSHA256) {
		return nil, errors.New("invalid behavior evidence identity or status")
	}
	return validateObservations(evidence.Specs, evidence.Observations)
}

func sameSpecs(before, after []Spec) bool {
	if len(before) != len(after) {
		return false
	}
	kinds := make(map[string]string, len(before))
	for _, spec := range before {
		kinds[spec.Key] = spec.Kind
	}
	for _, spec := range after {
		if kinds[spec.Key] != spec.Kind {
			return false
		}
	}
	return true
}

// Object ordering and number spelling are irrelevant. Arrays retain order and
// multiplicity. The parser rejects duplicate keys rather than choosing a value.
func canonicalJSON(body []byte) (json.RawMessage, error) {
	if len(body) == 0 || len(body) > MaxDocumentBytes || !utf8.Valid(body) {
		return nil, errors.New("invalid JSON size or encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	return json.Marshal(value)
}

// CanonicalJSON preserves array order and exact numeric values while rejecting
// duplicate object keys, oversized documents and excessive nesting.
func CanonicalJSON(body []byte) (json.RawMessage, error) { return canonicalJSON(body) }

func readJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON nesting exceeds the format bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid JSON object key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate JSON object key")
			}
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		if number, ok := token.(json.Number); ok {
			return normalizeNumber(string(number))
		}
		return token, nil
	}
}

func normalizeNumber(number string) (json.Number, error) {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(number), "e")
	exponent := int64(0)
	if hasExponent {
		var err error
		exponent, err = strconv.ParseInt(exponentText, 10, 32)
		if err != nil {
			return "", errors.New("JSON number exponent exceeds the format bound")
		}
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent += int64(len(digits) - len(trimmed) - len(fraction))
	result := sign + trimmed
	if exponent != 0 {
		result += "e" + strconv.FormatInt(exponent, 10)
	}
	return json.Number(result), nil
}
