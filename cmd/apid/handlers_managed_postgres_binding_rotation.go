package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) rotateManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	binding, err := s.managedPostgresBindings.Rotate(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	app, err := s.store.AppByID(r.Context(), binding.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "app not found")
		return
	}
	if binding.RotationWakeID == "" || binding.RotationPreviousGeneration < 1 {
		managedPostgresProblem(w, managedpostgres.ErrConflict)
		return
	}
	if !binding.RotationCleanupReady {
		if _, err := state.InvalidateAppSnapshotsAtExistingStamp(r.Context(), s.store, app.ID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not invalidate application snapshots"))
			return
		}

		deployments, err := s.store.ListDeploymentsForApp(r.Context(), app.ID, 0, 0)
		if err != nil {
			managedPostgresProblem(w, err)
			return
		}
		hasLiveDeployment := false
		for _, deployment := range deployments {
			if deployment.Status == state.DeployLive {
				hasLiveDeployment = true
				break
			}
		}
		if !hasLiveDeployment {
			instances, err := s.store.ListInstancesForApp(r.Context(), app.ID)
			if err != nil {
				managedPostgresProblem(w, err)
				return
			}
			for _, instance := range instances {
				if state.State(instance.State).CountsForRAM() {
					api.WriteProblem(w, api.ErrCapacity("cannot finish rotation while resident instances remain without a live deployment"))
					return
				}
			}
			rotations, ok := s.store.(state.ManagedPostgresBindingRotationStore)
			if !ok {
				managedPostgresProblem(w, managedpostgres.ErrUnavailable)
				return
			}
			if err := rotations.FinalizeManagedPostgresBindingRotationsForApp(r.Context(), app.ID, binding.RotationWakeID); err != nil {
				managedPostgresProblem(w, err)
				return
			}
		} else {
			claimed := false
			if app.Status == state.AppActive {
				claimed, err = claimAppRestart(r.Context(), s.store, app.ID)
				if err != nil {
					api.WriteProblem(w, api.ErrCapacity("could not claim runtime configuration refresh"))
					return
				}
			}
			payload, marshalErr := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": binding.RotationWakeID})
			if marshalErr == nil {
				marshalErr = s.notif.Notify(r.Context(), db.NotifyRuntimeConfigRestart, string(payload))
			}
			if marshalErr != nil {
				if claimed {
					if releaseErr := releaseAppRestartClaim(context.WithoutCancel(r.Context()), s.store, app.ID); releaseErr != nil {
						s.log.Error("managed postgres binding rotation: release failed restart claim", "app", app.ID, "err", releaseErr)
					}
				}
				api.WriteProblem(w, api.ErrCapacity("could not queue runtime configuration refresh; retry rotation"))
				return
			}
		}
	}
	s.audit.Emit(r.Context(), "managed_postgres.binding.rotated", &acct.ID, map[string]any{
		"binding_id": binding.ID, "database_id": binding.DatabaseID,
		"app_id": binding.AppID, "credential_generation": binding.CredentialGeneration,
		"wake_id": binding.RotationWakeID,
	})
	writeJSON(w, http.StatusOK, managedPostgresBindingView(binding))
}
