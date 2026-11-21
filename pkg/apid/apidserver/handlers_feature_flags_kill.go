package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// featureFlagKillAttempts bounds re-reading the configuration when another
// publish wins the version race.
const featureFlagKillAttempts = 3

// killFeatureFlag sets one flag's kill on the latest configuration
// (ADR-20261117215858 §3). It is the one flag write an agent connection may
// make without a person's approval: it only makes the app serve the
// fallback its own code chose. It takes no expected version, re-reads on a
// race, and is idempotent; reviving a flag is an ordinary PUT.
func (s *server) killFeatureFlag(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagWriteScope(w, r, acct)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !flags.ValidKey(key) {
		api.WriteProblem(w, api.ErrValidation("invalid flag key"))
		return
	}
	var lastErr error
	for range featureFlagKillAttempts {
		current, err := store.GetFeatureFlags(r.Context(), scope, 0)
		if err != nil {
			writeFeatureFlagLookupError(w, err, "no flag configuration is published in this environment")
			return
		}
		cfg := current.Config
		f, err := flags.FindFlag(&cfg, key)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "flag "+key+" does not exist in this environment"))
			return
		}
		if f.Killed {
			writeJSON(w, http.StatusOK, api.FeatureFlagKillResponse{Key: key, Killed: true, Version: current.Version})
			return
		}
		// Copy the flags so the store's cached bundle is never mutated.
		cfg.Flags = append([]flags.Flag(nil), cfg.Flags...)
		if _, err := flags.EditKill(key, true)(&cfg); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		v, err := store.UpdateFeatureFlags(r.Context(), state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: cfg, Actor: acct.ID})
		if errors.Is(err, state.ErrConflict) {
			lastErr = err
			continue
		}
		if err != nil {
			writeFeatureFlagError(w, err)
			return
		}
		s.audit.Emit(r.Context(), "flags.flag_killed", &scope.AccountID, map[string]any{"project_id": scope.ProjectID, "environment_id": scope.EnvironmentID, "flag": key, "version": v.Version})
		writeJSON(w, http.StatusOK, api.FeatureFlagKillResponse{Key: key, Killed: true, Changed: true, Version: v.Version})
		return
	}
	writeFeatureFlagError(w, lastErr)
}
