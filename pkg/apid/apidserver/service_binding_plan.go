package apidserver

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func serviceBindingPlanConflictProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Service binding plan is stale", "Service binding settings changed; create a fresh plan before applying.")
}

func validateServiceBindingPlanRequest(req api.UpdateAppRequest, environment string) *api.Problem {
	expected := req.ExpectedServiceBindingFingerprint
	if expected == nil {
		return nil
	}
	raw, err := hex.DecodeString(*expected)
	if err != nil || len(raw) != 32 || environment != "" {
		return api.ErrValidation("service binding fingerprint must be a SHA-256 value for an app-level service-only update")
	}
	body, _ := json.Marshal(req)
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		return api.ErrValidation("invalid service binding plan request")
	}
	changes := 0
	for key, value := range fields {
		if string(value) == "null" {
			continue
		}
		switch key {
		case "expected_service_binding_fingerprint":
		case "service_binding_targets", "service_binding_policy", "service_binding_transport", "service_reliability", "allowed_service_callers", "allowed_service_call_scopes":
			changes++
		default:
			return api.ErrValidation("fingerprint-guarded requests may change only service binding settings")
		}
	}
	// Explicit null caller/scopes/reliability resets also count as changes.
	if changes == 0 && len(req.AllowedServiceCallers) == 0 && len(req.AllowedServiceCallScopes) == 0 && len(req.ServiceReliability) == 0 {
		return api.ErrValidation("service binding plan contains no changes")
	}
	return nil
}
