package apidserver_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// The flag evaluator and persistence types live outside pkg/api. Check their
// actual flattened JSON fields so the generated SDK contract cannot drift.
func TestFeatureFlagsSpecContracts(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{
		"FlagRule": flags.Rule{}, "FeatureFlag": flags.Flag{}, "FlagsConfig": flags.Config{},
		"FlagVariant": flags.FlagVariant{}, "ProgressiveRollout": flags.ProgressiveRollout{},
		"FlagsBundle": flags.Bundle{}, "FeatureFlagVersion": state.FeatureFlagVersion{},
		"FlagDecision": flags.Decision{}, "FlagEvidence": flags.Evidence{},
		"UpdateFeatureFlagsRequest":   apidserver.UpdateFeatureFlagsRequest{},
		"RollbackFeatureFlagsRequest": apidserver.RollbackFeatureFlagsRequest{},
		"InspectFeatureFlagRequest":   apidserver.InspectFeatureFlagRequest{},
		"FlagRolloutPromotionRequest": apidserver.PromoteFeatureFlagRolloutRequest{},
		"FlagRolloutPromotion":        apidserver.FeatureFlagRolloutPromotionResponse{},
		"FlagRequestEvidence":         apidserver.FeatureFlagRequestEvidence{}, "FlagEvidencePage": apidserver.FeatureFlagEvidencePage{},
		"FlagOutcome": apidserver.FeatureFlagOutcome{}, "FlagOutcomesResponse": apidserver.FeatureFlagOutcomesResponse{},
	} {
		t.Run(name, func(t *testing.T) {
			fields := flagJSONFields(reflect.TypeOf(value))
			properties := flagSchemaProperties(spec.Schemas[name], spec)
			if len(properties) == 0 {
				t.Fatal("missing schema properties")
			}
			for field := range fields {
				if !properties[field] {
					t.Errorf("encoded field %q missing from spec", field)
				}
			}
			for property := range properties {
				if !fields[property] {
					t.Errorf("spec property %q missing from encoded type", property)
				}
			}
		})
	}
}

func flagJSONFields(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Anonymous {
			for name := range flagJSONFields(field.Type) {
				out[name] = true
			}
			continue
		}
		if name := strings.Split(field.Tag.Get("json"), ",")[0]; name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func flagSchemaProperties(body map[string]any, spec *specDoc) map[string]bool {
	out := map[string]bool{}
	if ref, ok := body["$ref"].(string); ok {
		return flagSchemaProperties(spec.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")], spec)
	}
	if properties, ok := body["properties"].(map[string]any); ok {
		for name := range properties {
			out[name] = true
		}
	}
	if parts, ok := body["allOf"].([]any); ok {
		for _, part := range parts {
			for name := range flagSchemaProperties(part.(map[string]any), spec) {
				out[name] = true
			}
		}
	}
	return out
}

func TestFeatureFlagsDisabledByDefault(t *testing.T) {
	e := apidserver.Setup(t, "pro")
	// Project access resolves the addressed project first (#4882), so the gate
	// is observed on a project the caller owns.
	if _, err := (*e.Store()).CreateProject(t.Context(), state.Project{AccountID: (*e.Acct()).ID, Slug: "example"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/projects/example/environments/production/flags", "/v1/runtime/flags"} {
		if res := e.Do(t, "GET", path, nil, nil); res.Code != 403 {
			t.Fatalf("disabled flags %s = %d", path, res.Code)
		}
	}
}
