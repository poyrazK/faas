package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) validateProjectInitialSecrets(r *http.Request, values api.ProjectInitialSecrets, workloads []reposcan.Workload, limits api.Limits) *api.Problem {
	if len(values) == 0 {
		return nil
	}
	principal, ok := principalFrom(r)
	if !ok || !principalHasScope(principal, api.ScopesSecretsWriteSurface) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Secret write permission required", "Initial configuration and runtime service connections require secrets:write or admin scope")
	}
	if problem := projectsetup.Validate(values, workloads, limits); problem != nil {
		return problem
	}
	if setSecretRecipient == nil || hostHMACKey == nil || setSecretRecipient() == nil || len(hostHMACKey()) == 0 {
		return api.ErrCapacity("secret storage is temporarily unavailable")
	}
	return nil
}

func (s *server) persistProjectInitialSecret(ctx context.Context, acct state.Account, app state.App, scope, key, value string, limits api.Limits) *api.Problem {
	if problem := s.checkSecretQuota(ctx, acct, app, scope, key, limits); problem != nil {
		return problem
	}
	if problem := s.sealAndPersist(ctx, acct, app, scope, key, value, "", limits); problem != nil {
		return problem
	}
	invalidated, err := s.invalidateAppSnapshots(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not invalidate application snapshots")
	}
	s.audit.Emit(ctx, "secret.set", &acct.ID, map[string]any{
		"app_id": app.ID, "scope": scope, "name": key,
		"source": "project_apply", "snapshots_invalidated": invalidated,
	})
	s.notifyRuntimeConfigChange(ctx, db.NotifySecretRotated, acct, app, "set", scope, key)
	return nil
}
