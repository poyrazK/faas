package flags

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	PropagationContextVersion      = 1
	MaxPropagationContextBytes     = 8 << 10
	MaxPropagationContextHeaderLen = 12 << 10
)

// EvidenceOrigin names the app configuration that produced a decision. It is
// retained when that decision is inherited through additional service calls.
type EvidenceOrigin struct {
	AppID         string `json:"app_id"`
	EnvironmentID string `json:"environment_id"`
}

// PropagationDecision carries the source decision and its original owner.
// Source is configuration or fallback in the wire context; receiving SDKs
// mark the resulting request evidence as inherited.
type PropagationDecision struct {
	Decision
	Origin EvidenceOrigin `json:"origin"`
}

// PropagationContext is a bounded snapshot for one authenticated customer
// moving across a Gregale-managed service call. It contains only decisions
// explicitly marked used by the application.
type PropagationContext struct {
	Version    int                   `json:"version"`
	CustomerID string                `json:"customer_id"`
	Decisions  []PropagationDecision `json:"decisions"`
}

// DecodePropagationHeader validates the SDK envelope before the service proxy
// carries it across its trusted gateway-to-guest boundary.
func DecodePropagationHeader(value string) (PropagationContext, error) {
	if value == "" || len(value) > MaxPropagationContextHeaderLen {
		return PropagationContext{}, fmt.Errorf("invalid flag context header size")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != value {
		return PropagationContext{}, fmt.Errorf("invalid flag context encoding")
	}
	if len(raw) == 0 || len(raw) > MaxPropagationContextBytes {
		return PropagationContext{}, fmt.Errorf("invalid flag context size")
	}
	_, context, err := CanonicalPropagationContext(raw)
	return context, err
}

// EncodePropagationHeader validates, sorts and encodes an SDK context.
func EncodePropagationHeader(context PropagationContext) (string, error) {
	raw, err := json.Marshal(context)
	if err != nil {
		return "", err
	}
	canonical, _, err := CanonicalPropagationContext(raw)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(canonical))
	if len(encoded) > MaxPropagationContextHeaderLen {
		return "", fmt.Errorf("flag context header too large")
	}
	return encoded, nil
}

// CanonicalPropagationContext validates the wire contract and returns stable
// JSON suitable for a trusted service proxy to forward.
func CanonicalPropagationContext(raw []byte) (string, PropagationContext, error) {
	if len(raw) == 0 || len(raw) > MaxPropagationContextBytes {
		return "", PropagationContext{}, fmt.Errorf("invalid flag context size")
	}
	var context PropagationContext
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&context); err != nil {
		return "", PropagationContext{}, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", PropagationContext{}, fmt.Errorf("trailing flag context")
	}
	if context.Version != PropagationContextVersion || !validUUID(context.CustomerID) || len(context.Decisions) == 0 || len(context.Decisions) > api.FlagsMaxEvidencePerRequest {
		return "", PropagationContext{}, fmt.Errorf("invalid flag context envelope")
	}
	seen := make(map[string]struct{}, len(context.Decisions))
	for _, propagated := range context.Decisions {
		decision := propagated.Decision
		if err := validateDecision(decision, false); err != nil || !validUUID(propagated.Origin.AppID) || !validUUID(propagated.Origin.EnvironmentID) {
			return "", PropagationContext{}, fmt.Errorf("invalid propagated flag decision")
		}
		if _, ok := seen[decision.Flag]; ok {
			return "", PropagationContext{}, fmt.Errorf("duplicate propagated flag decision")
		}
		seen[decision.Flag] = struct{}{}
	}
	if context.Decisions == nil {
		context.Decisions = []PropagationDecision{}
	}
	slices.SortFunc(context.Decisions, func(a, b PropagationDecision) int {
		return strings.Compare(a.Flag, b.Flag)
	})
	canonical, err := json.Marshal(context)
	if err != nil {
		return "", PropagationContext{}, err
	}
	if len(canonical) > MaxPropagationContextBytes {
		return "", PropagationContext{}, fmt.Errorf("flag context too large")
	}
	return string(canonical), context, nil
}

func validUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == strings.ToLower(value)
}

func validateDecision(decision Decision, allowInherited bool) error {
	_, booleanValue := decision.Value.(bool)
	stringValue, variantValue := decision.Value.(string)
	validType := (decision.Type == "" || decision.Type == "boolean") && booleanValue || decision.Type == "variant" && variantValue && ValidKey(stringValue)
	if !ValidKey(decision.Flag) || decision.ConfigVersion < 0 || decision.ConfigVersion > api.FlagsMaxConfigVersion || decision.RuleID != "" && !ValidKey(decision.RuleID) || decision.Bucket != nil && (*decision.Bucket < 0 || *decision.Bucket >= 10000) || decision.RolloutBucket != nil && (*decision.RolloutBucket < 0 || *decision.RolloutBucket >= 10000) || !validType {
		return fmt.Errorf("invalid decision fields")
	}
	if !validDecisionReason(decision.Reason) {
		return fmt.Errorf("invalid decision reason")
	}
	fallbackReason := decision.Reason == "flag_missing" || decision.Reason == "configuration_stale" || decision.Reason == "type_mismatch"
	switch decision.Source {
	case "configuration":
		if fallbackReason || decision.InheritedFrom != nil {
			return fmt.Errorf("decision source does not match reason")
		}
	case "fallback":
		if !fallbackReason || decision.InheritedFrom != nil {
			return fmt.Errorf("decision source does not match reason")
		}
	case "inherited":
		if !allowInherited || decision.InheritedFrom == nil || !validUUID(decision.InheritedFrom.AppID) || !validUUID(decision.InheritedFrom.EnvironmentID) {
			return fmt.Errorf("invalid inherited decision origin")
		}
	default:
		return fmt.Errorf("invalid decision source")
	}
	if decision.Reason == "rule_match" && decision.RuleID == "" || decision.Reason != "rule_match" && (decision.RuleID != "" || decision.Bucket != nil || decision.RolloutBucket != nil) {
		return fmt.Errorf("invalid rule evidence")
	}
	return nil
}

func validDecisionReason(reason string) bool {
	switch reason {
	case "flag_missing", "default", "disabled", "customer_missing", "rule_match", "configuration_stale", "type_mismatch":
		return true
	default:
		return false
	}
}
