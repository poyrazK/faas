package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/envbatch"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) envBatchHandler() httpkit.AccountHandler {
	store, _ := s.store.(state.EnvBatchStore)
	return envbatch.Apply(envbatch.Deps{Store: store, Actor: configActor,
		Resolve: func(w http.ResponseWriter, r *http.Request, account state.Account) (state.App, string, bool) {
			app, scope, _, ok := s.configRevisionScope(w, r, account)
			return app, scope, ok
		},
		Guard: func(r *http.Request, app state.App, scope string) bool {
			return configGuardRequested(r) && s.configGuardHasTraffic(r.Context(), app.ID, scope)
		},
		After: s.afterEnvBatch,
	})
}

func (s *server) afterEnvBatch(r *http.Request, account state.Account, app state.App, scope string, request api.EnvBatchRequest, response api.EnvBatchResponse) {
	// The transaction already recorded history and invalidated snapshots.
	// These notifications and classifier updates may be repeated on replay.
	// An older receipt must not reclassify values superseded by another write.
	current := false
	if store, ok := s.store.(state.ConfigRevisionStore); ok {
		rows, err := store.ListConfigRevisions(r.Context(), app.ID, scope, 1)
		current = err == nil && len(rows) == 1 && rows[0].Revision == response.ConfigChange.Revision
	}
	for _, key := range response.UpdatedKeys {
		if current && s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
			if err := s.runEnvClassifier(r.Context(), account, app, scope, key, request.Values[key]); err != nil {
				s.log.Warn("env batch classification failed", "app", app.Slug, "key", key)
			}
		}
		s.recordAppActivity(r.Context(), r, account, app, state.OrgActivity{Kind: "env.set", ResourceType: "environment_variable", ResourceID: scope + ":" + key,
			ResourceLabel: key, SourceType: "env.set", SourceID: request.RequestID + ":" + key, Data: activityData(map[string]any{"scope": scope, "request_id": request.RequestID})})
	}
	for _, key := range response.DeletedKeys {
		s.recordAppActivity(r.Context(), r, account, app, state.OrgActivity{Kind: "env.deleted", ResourceType: "environment_variable", ResourceID: scope + ":" + key,
			ResourceLabel: key, SourceType: "env.deleted", SourceID: request.RequestID + ":" + key, Data: activityData(map[string]any{"scope": scope, "request_id": request.RequestID})})
	}
	s.audit.Emit(r.Context(), "env.batch", &account.ID, map[string]any{"app_id": app.ID, "scope": scope, "request_id": request.RequestID, "revision": response.ConfigChange.Revision})
	s.notifyRuntimeConfigChange(r.Context(), db.NotifyAppEnvChanged, account, app, "batch", scope, "")
}
