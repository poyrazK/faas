package api_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
	"testing"
)

func TestAuditIntegerParameterCannotCauseInternalEncodingError(t *testing.T) {
	kinds := map[string]api.EdgeRuleParamKind{"page": {Type: "integer"}}
	for _, raw := range []string{"01", "+1", "-01"} {
		t.Run(raw, func(t *testing.T) {
			got, err := api.EdgeRuleParamInstance(kinds, map[string][]string{"page": {raw}})
			if err != nil {
				t.Errorf("client integer spelling %q causes an internal JSON-encoding error rather than a schema mismatch: %v", raw, err)
			}
			if err == nil && !json.Valid(got) {
				t.Fatal("invalid fixture JSON", string(got))
			}
		})
	}
}

func TestAuditInvalidSchemaRejectedBeforeGatewayLoad(t *testing.T) {
	for _, raw := range []string{`{"type":"integre"}`, `{"type":"integer","minimum":"zero"}`, `{"type":"string","pattern":"["}`} {
		t.Run(raw, func(t *testing.T) {
			action := api.EdgeRuleValidateAction{Schema: json.RawMessage(raw)}
			_, runtimeErr := edgevalidate.Compile([]byte(raw), false)
			if runtimeErr == nil {
				t.Fatal("fixture must fail actual gateway schema compiler")
			}
			if prob := action.Validate(); prob == nil {
				t.Errorf("API accepts a schema the serving compiler cannot load: %v", runtimeErr)
			}
		})
	}
}

func TestParameterOnlyRuleAcceptsLegacyNullBodySchema(t *testing.T) {
	action := api.EdgeRuleValidateAction{Schema: json.RawMessage(`null`), Parameters: &api.EdgeRuleValidateParameters{Query: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer"}}}`)}}
	if problem := action.Validate(); problem != nil {
		t.Fatalf("valid parameter-only rule rejected: %v", problem)
	}
	if problem := action.Parameters.Validate("/**"); problem != nil {
		t.Fatal(problem)
	}
}

func TestEdgeValidationNumberBoundsMatchLimits(t *testing.T) {
	if api.MaxEdgeRuleValidateNumberBytes != edgevalidate.MaxNumberBytes || api.MaxEdgeRuleValidateNumberExponent != edgevalidate.MaxNumberExponent || api.MaxEdgeRuleValidateNumberWorkBytes != edgevalidate.MaxNumberWorkBytes {
		t.Fatal("API and standalone validator numeric bounds diverged")
	}
}
