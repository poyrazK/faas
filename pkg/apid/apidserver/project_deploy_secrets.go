package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/secretbox"
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
//
// Each key goes only to the services whose Compose environment names it
// (ADR-20261108090000). A key no selected service names goes to every selected
// service that has no value for it yet, as before, but never replaces one.
// Writing every key to every service handed one service's credentials to all
// of them and replaced values a service had set itself. An unchanged value is
// not rewritten, so it does not evict snapshots, and every quota is checked
// before the first write.
func (s *server) applyProjectDeploySecrets(r *http.Request, acct state.Account, apps []state.App, workloads []reposcan.Workload, environment string, secrets map[string]string) *api.Problem {
	scope := environment
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	declared := map[string]map[string]bool{}
	declaredAnywhere := map[string]bool{}
	for _, w := range workloads {
		names := map[string]bool{}
		for _, key := range w.EnvKeys {
			names[key] = true
			declaredAnywhere[key] = true
		}
		declared[strings.ToLower(w.Name)] = names
	}
	limits := api.MustLimitsFor(acct.Plan)
	hmacKey := hostHMACKeyBytes()
	type write struct {
		app state.App
		key string
	}
	var writes []write
	for _, app := range apps {
		added := 0
		for _, key := range keys {
			existing, err := s.store.GetAppSecretInScope(r.Context(), acct.ID, app.ID, scope, key)
			if err != nil && !errors.Is(err, state.ErrNotFound) {
				return api.ErrCapacity("could not read project secrets")
			}
			exists := err == nil && existing != nil
			switch {
			case declared[strings.ToLower(app.WorkloadName)][key]:
			case !declaredAnywhere[key] && !exists:
			default:
				continue
			}
			if exists && len(hmacKey) > 0 {
				if hash, hashErr := secretbox.ValueFingerprint([]byte(secrets[key]), hmacKey); hashErr == nil && hash == existing.ValueHash {
					continue
				}
			}
			if !exists {
				added++
			}
			writes = append(writes, write{app: app, key: key})
		}
		if added > 0 {
			count, err := s.store.CountAppSecrets(r.Context(), acct.ID, app.ID)
			if err != nil {
				return api.ErrCapacity("could not count secrets")
			}
			if count+added > limits.SecretCountMax {
				return api.ErrPlanLimitSecrets(limits, count+added)
			}
		}
	}
	for _, w := range writes {
		if _, prob := s.setAppSecret(r, acct, w.app, scope, w.key, api.PutAppSecretRequest{Value: secrets[w.key]}); prob != nil {
			return prob
		}
	}
	return nil
}
