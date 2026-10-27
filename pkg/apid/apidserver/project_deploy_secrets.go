package apidserver

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// readProjectDeploySecrets never includes plaintext or decoder input in errors.
func readProjectDeploySecrets(r io.Reader, limits api.Limits) (map[string]string, *api.Problem) {
	max := api.ProjectSecretsMaxJSONBytes(limits)
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil || int64(len(body)) > max {
		return nil, api.ErrValidation("project secrets bundle exceeds the plan's secret limits")
	}
	var secrets map[string]string
	if err := json.Unmarshal(body, &secrets); err != nil || secrets == nil {
		return nil, api.ErrValidation("secrets must be a JSON object of secret names and string values")
	}
	if len(secrets) > limits.SecretCountMax {
		return nil, api.ErrPlanLimitSecrets(limits, len(secrets))
	}
	for key, value := range secrets {
		if prob := api.ValidateSecretKey(key); prob != nil {
			return nil, prob
		}
		if value == "" {
			return nil, api.ErrValidation("secret values must not be empty")
		}
		if prob := (api.PutAppSecretRequest{Value: value}).Validate(limits.SecretValueMaxBytes); prob != nil {
			return nil, prob
		}
	}
	return secrets, nil
}

func projectDeploySecretsPermission(r *http.Request, apply bool) *api.Problem {
	if !apply {
		return api.ErrValidation("secrets are accepted only when applying a project")
	}
	p, ok := principalFrom(r)
	if !ok || !principalHasScope(p, api.ScopesSecretsWriteSurface) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "project secrets require secrets:write or admin")
	}
	return nil
}

// Apply all selected workloads' sealed configuration before admitting the first
// build. A failed write leaves durable, retryable apps/configuration and queues
// no builds. Values never enter the source spool, response, audit, or logs.
func (s *server) applyProjectDeploySecrets(r *http.Request, acct state.Account, apps []state.App, environment string, secrets map[string]string) *api.Problem {
	scope := environment
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, app := range apps {
		for _, key := range keys {
			if _, prob := s.setAppSecret(r, acct, app, scope, key, api.PutAppSecretRequest{Value: secrets[key]}); prob != nil {
				return prob
			}
		}
	}
	return nil
}
