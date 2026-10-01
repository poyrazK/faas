package api

import (
	"net/http"
	"strings"
)

type PutAppSecretReferenceRequest struct {
	Reference string `json:"reference"`
}

func (r PutAppSecretReferenceRequest) Validate() *Problem {
	if !strings.HasPrefix(r.Reference, SecretRefPrefix) || ValidateEnvKey(strings.TrimPrefix(r.Reference, SecretRefPrefix)) != nil {
		return ErrValidation("reference must name a scoped secret as secret:NAME")
	}
	return nil
}

// These projections contain names only. Count is the application's shared
// variable/reference quota usage across all environments.
type AppSecretReferenceListResponse struct {
	EnvironmentID  string            `json:"environment_id"`
	Environment    string            `json:"environment"`
	References     map[string]string `json:"references"`
	SuppressedKeys []string          `json:"suppressed_keys,omitempty"`
	Count          int               `json:"count"`
	Quota          int               `json:"quota"`
}

type AppSecretReferenceResponse struct {
	EnvironmentID string `json:"environment_id"`
	Environment   string `json:"environment"`
	Key           string `json:"key"`
	Reference     string `json:"reference"`
}

// A separate storage bound keeps pruning from consuming delivery-key slots.
func ErrSecretReferenceSuppressionLimit() *Problem {
	return NewProblem(http.StatusForbidden, "secret_reference_suppression_limit", "Suppression limit reached", "Set an explicit reference to re-enable a suppressed key before retaining more removals.").
		WithLimit(EnvironmentSecretReferenceSuppressionsMaxPerApp, EnvironmentSecretReferenceSuppressionsMaxPerApp+1).
		WithDocs(docsBase + "/env#secret-references")
}
