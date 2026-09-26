package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func serviceCallersForCreate(raw *[]string) (*[]string, *api.Problem) {
	if raw == nil {
		return nil, nil
	}
	callers, err := api.NormalizeAllowedServiceCallers(*raw)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	return &callers, nil
}

// serviceCallersForPatch preserves omitted, null, and array as distinct
// states. A pointer-to-slice DTO would collapse omitted and explicit null.
func serviceCallersForPatch(raw json.RawMessage) (*[]string, bool, *api.Problem) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, true, api.ErrValidation(fmt.Sprintf("allowed_service_callers must be an array of app names or null: %v", err))
	}
	callers, err := api.NormalizeAllowedServiceCallers(names)
	if err != nil {
		return nil, true, api.ErrValidation(err.Error())
	}
	return &callers, true, nil
}

func serviceCallScopesForCreate(raw *api.ServiceCallerScopes) (*api.ServiceCallerScopes, *api.Problem) {
	if raw == nil {
		return nil, nil
	}
	scopes, err := api.NormalizeServiceCallerScopes(*raw)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	return &scopes, nil
}

func serviceCallScopesForPatch(raw json.RawMessage) (*api.ServiceCallerScopes, bool, *api.Problem) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var input api.ServiceCallerScopes
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, true, api.ErrValidation(fmt.Sprintf("allowed_service_call_scopes must be an object mapping app names to methods and path_prefixes, or null: %v", err))
	}
	scopes, err := api.NormalizeServiceCallerScopes(input)
	if err != nil {
		return nil, true, api.ErrValidation(err.Error())
	}
	return &scopes, true, nil
}
