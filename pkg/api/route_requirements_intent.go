package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

const RoutePublicExceptionMarker = "declared_public_exception"

// CanonicalRouteRequirements isolates and orders already validated intent and
// removes private commentary. Semantic validation belongs to the evaluator's
// parser; this helper has no state or evaluator dependencies.
func CanonicalRouteRequirements(config RouteRequirementsConfig) (RouteRequirementsConfig, string, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return RouteRequirementsConfig{}, "", fmt.Errorf("encode route intent: %w", err)
	}
	var isolated RouteRequirementsConfig
	if err := json.Unmarshal(body, &isolated); err != nil {
		return RouteRequirementsConfig{}, "", fmt.Errorf("isolate route intent: %w", err)
	}
	sort.Slice(isolated.Routes, func(i, j int) bool {
		return isolated.Routes[i].Method+" "+isolated.Routes[i].Path < isolated.Routes[j].Method+" "+isolated.Routes[j].Path
	})
	sort.Slice(isolated.Groups, func(i, j int) bool { return isolated.Groups[i].Name < isolated.Groups[j].Name })
	for i := range isolated.Groups {
		sort.Strings(isolated.Groups[i].Methods)
	}
	sort.Slice(isolated.Public, func(i, j int) bool {
		return isolated.Public[i].Method+" "+isolated.Public[i].Path < isolated.Public[j].Method+" "+isolated.Public[j].Path
	})
	for i := range isolated.Public {
		isolated.Public[i].Reason = RoutePublicExceptionMarker
	}
	body, err = json.Marshal(isolated)
	if err != nil {
		return isolated, "", fmt.Errorf("encode normalized route intent: %w", err)
	}
	return isolated, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}
