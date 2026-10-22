// adr: 1451 — config history, rollback and guard reads.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/configguard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const configRevisionsDefaultLimit = 20

func configRevisionDTO(r state.ConfigRevision) api.ConfigRevision {
	return api.ConfigRevision{Revision: r.Revision, Scope: r.Scope, Source: r.Source, Key: r.Key, Actor: r.Actor,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), VariableCount: len(r.Variables), SecretCount: len(r.Secrets),
		GuardID: r.GuardID, GuardStatus: r.GuardStatus, RestoredRevision: r.RestoredRevision}
}

func formatOptionalConfigTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func configGuardDTO(g state.ConfigGuard) *api.ConfigGuard {
	out := &api.ConfigGuard{ID: g.ID, Scope: g.Scope, Status: g.Status, BaseRevision: g.BaseRevision,
		HeadRevision: g.HeadRevision, Restorable: g.Restorable, OpenedAt: g.OpenedAt.UTC().Format(time.RFC3339Nano),
		LastChangeAt: g.LastChangeAt.UTC().Format(time.RFC3339Nano), WindowEndsAt: formatOptionalConfigTime(g.WindowEndsAt),
		ClosedAt: formatOptionalConfigTime(g.ClosedAt)}
	if len(g.Decision) > 0 {
		var decision api.ConfigGuardDecision
		if err := json.Unmarshal(g.Decision, &decision); err == nil {
			out.Decision = &decision
		}
	}
	return out
}

// configRevisionScope resolves the app and scope of a config request, or
// writes the problem and returns ok=false.
func (s *server) configRevisionScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, string, state.ConfigRevisionStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, "", nil, false
	}
	scope, _, prob := scopeFromQuery(r, false)
	if prob != nil {
		api.WriteProblem(w, prob)
		return state.App{}, "", nil, false
	}
	store, ok := s.store.(state.ConfigRevisionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("config history is unavailable"))
		return state.App{}, "", nil, false
	}
	return app, scope, store, true
}

// listConfigRevisions serves GET /v1/apps/{slug}/config/revisions.
func (s *server) listConfigRevisions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, scope, store, ok := s.configRevisionScope(w, r, acct)
	if !ok {
		return
	}
	limit := configRevisionsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.ConfigRevisionsListMax {
			api.WriteProblem(w, api.ErrValidation("limit must be between 1 and "+strconv.Itoa(api.ConfigRevisionsListMax)))
			return
		}
		limit = n
	}
	revisions, err := store.ListConfigRevisions(r.Context(), app.ID, scope, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list config revisions"))
		return
	}
	out := api.ListConfigRevisionsResponse{Revisions: make([]api.ConfigRevision, 0, len(revisions))}
	for _, revision := range revisions {
		out.Revisions = append(out.Revisions, configRevisionDTO(revision))
	}
	if guard, err := store.LatestConfigGuard(r.Context(), app.ID, scope); err == nil {
		out.Guard = configGuardDTO(guard)
	}
	writeJSON(w, http.StatusOK, out)
}

// getConfigRevision serves GET /v1/apps/{slug}/config/revisions/{revision}.
func (s *server) getConfigRevision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, scope, store, ok := s.configRevisionScope(w, r, acct)
	if !ok {
		return
	}
	number, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || number < 1 {
		api.WriteProblem(w, api.ErrValidation("revision must be a positive integer"))
		return
	}
	revision, err := store.GetConfigRevision(r.Context(), app.ID, scope, number)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrConfigRevisionNotFound(scope, number))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read config revision"))
		return
	}
	out := api.ConfigRevisionDetail{ConfigRevision: configRevisionDTO(revision), Variables: revision.Variables,
		SecretVersions: make(map[string]int64, len(revision.Secrets))}
	for key, fp := range revision.Secrets {
		out.SecretVersions[key] = fp.Version
	}
	writeJSON(w, http.StatusOK, out)
}

// getConfigGuard serves GET /v1/apps/{slug}/config/guard.
func (s *server) getConfigGuard(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, scope, store, ok := s.configRevisionScope(w, r, acct)
	if !ok {
		return
	}
	guard, err := store.LatestConfigGuard(r.Context(), app.ID, scope)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrConfigGuardNotFound(scope))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read config guard"))
		return
	}
	writeJSON(w, http.StatusOK, configGuardDTO(guard))
}

// rollbackConfig serves POST /v1/apps/{slug}/config/rollback?scope=: it sets and
// deletes variables until the scope matches the revision, as a guarded
// change. Secrets cannot be restored from a revision; they are reported.
func (s *server) rollbackConfig(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ConfigRollbackRequest
	if err := decodeJSON(r, &req); err != nil || req.Revision < 1 {
		api.WriteProblem(w, api.ErrValidation("body must name a positive revision"))
		return
	}
	app, scope, store, ok := s.configRevisionScope(w, r, acct)
	if !ok {
		return
	}
	target, err := store.GetConfigRevision(r.Context(), app.ID, scope, req.Revision)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrConfigRevisionNotFound(scope, req.Revision))
		return
	}
	variables, secrets, stateErr := store.ConfigState(r.Context(), acct.ID, app.ID, scope)
	if err != nil || stateErr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read config"))
		return
	}
	out := api.ConfigRollbackResponse{RestoredRevision: target.Revision, Set: []string{}, Deleted: []string{},
		SecretsNotRestored: secretDifferences(target.Secrets, secrets)}
	writes := configguard.VariablesToward(target.Variables, variables)
	if len(writes) > 0 {
		change := s.beginConfigChange(r, acct, app, scope, "")
		applyErr := s.applyVariableWrites(r.Context(), acct, app, scope, writes, &out)
		revision := s.finishRestore(r.Context(), change, target.Revision)
		if applyErr != nil {
			if !writeEnvironmentGitOpsOwnershipProblem(w, applyErr) {
				api.WriteProblem(w, api.ErrCapacity("could not restore config"))
			}
			return
		}
		out.Change = configChangeReceipt(change, revision)
		s.audit.Emit(r.Context(), "config.rollback", &acct.ID, map[string]any{"app_id": app.ID, "scope": scope,
			"restored_revision": target.Revision, "set": out.Set, "deleted": out.Deleted})
	}
	writeJSON(w, http.StatusOK, out)
}

// finishRestore records a config.rollback revision for a guarded rollback.
func (s *server) finishRestore(ctx context.Context, c *configChange, restored int64) *state.ConfigRevision {
	if c == nil {
		return nil
	}
	revision, err := c.store.FinishConfigChange(ctx, state.ConfigChangeFinish{AccountID: c.acct.ID, AppID: c.app.ID,
		Scope: c.scope, Actor: c.actor, Source: state.ConfigSourceRollback, GuardID: c.ticket.GuardID,
		RestoredRevision: restored, Now: time.Now().UTC()})
	if err != nil {
		s.configRevisionFailed(ctx, c.acct, c.app, c.scope, "finish", err)
		return nil
	}
	return &revision
}

// applyVariableWrites applies a restore's variable writes, invalidates the
// scope's snapshots and tells the runtime, as the env handlers do.
func (s *server) applyVariableWrites(ctx context.Context, acct state.Account, app state.App, scope string, writes []configguard.VariableWrite, out *api.ConfigRollbackResponse) error {
	for _, write := range writes {
		var err error
		if write.Delete {
			err = s.store.DeleteAppEnvInScope(ctx, acct.ID, app.ID, scope, write.Key)
			if errors.Is(err, state.ErrNotFound) {
				err = nil
			}
		} else {
			err = s.store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, scope, write.Key, write.Value)
		}
		if err != nil {
			return err
		}
		kind := "set"
		if write.Delete {
			kind = "delete"
		}
		if out != nil {
			if write.Delete {
				out.Deleted = append(out.Deleted, write.Key)
			} else {
				out.Set = append(out.Set, write.Key)
			}
		}
		s.notifyRuntimeConfigChange(ctx, db.NotifyAppEnvChanged, acct, app, kind, scope, write.Key)
	}
	_, err := state.InvalidateAppSnapshotsInScope(ctx, s.store, app.ID, scope)
	return err
}

func secretDifferences(target, current map[string]state.ConfigSecretFingerprint) []api.ConfigSecretDifference {
	keys := configguard.SecretsDiffering(target, current)
	out := make([]api.ConfigSecretDifference, 0, len(keys))
	for _, key := range keys {
		out = append(out, api.ConfigSecretDifference{Key: key, TargetVersion: target[key].Version, CurrentVersion: current[key].Version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
