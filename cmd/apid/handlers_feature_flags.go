package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

var errFlagsWorkloadIdentity = errors.New("flags: invalid workload identity")

type updateFeatureFlagsRequest struct {
	ExpectedVersion *int64       `json:"expected_version"`
	Config          flags.Config `json:"config"`
}
type rollbackFeatureFlagsRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Version         int64  `json:"version"`
}
type inspectFeatureFlagRequest struct {
	CustomerID      string `json:"customer_id"`
	SubjectID       string `json:"subject_id,omitempty"`
	Version         int64  `json:"version,omitempty"`
	Fallback        bool   `json:"fallback"`
	FallbackVariant string `json:"fallback_variant,omitempty"`
}

func (s *server) featureFlagScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FeatureFlagScope, state.FeatureFlagStore, bool) {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.AppID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account key required", "project-wide flags require an account-scoped credential"))
		return state.FeatureFlagScope{}, nil, false
	}
	if !s.featureFlagsEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags require operator qualification"))
		return state.FeatureFlagScope{}, nil, false
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return state.FeatureFlagScope{}, nil, false
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		writeFeatureFlagError(w, err)
		return state.FeatureFlagScope{}, nil, false
	}
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
	}
	return state.FeatureFlagScope{AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: env.ID}, store, ok
}
func writeFeatureFlagError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "flag environment, version, or customer not found"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flags_version_conflict", "Configuration changed", "refresh the configuration and supply its expected_version"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	default:
		api.WriteProblem(w, api.ErrInternal("feature flag operation failed"))
	}
}
func flagVersionParam(r *http.Request, key string) (int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 {
		return 0, state.ErrInvalidArgument
	}
	return v, nil
}
func (s *server) getFeatureFlags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	version, err := flagVersionParam(r, "version")
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, version)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}
func (s *server) listFeatureFlagVersions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	before, err := flagVersionParam(r, "before_version")
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	rows, err := store.ListFeatureFlagVersions(r.Context(), scope, before)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
func (s *server) updateFeatureFlags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.FlagsMaxBundleBytes)
	var req updateFeatureFlagsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.ExpectedVersion == nil {
		api.WriteProblem(w, api.ErrValidation("expected_version is required"))
		return
	}
	s.publishFeatureFlags(w, r, store, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: *req.ExpectedVersion, Config: req.Config, Actor: acct.ID})
}
func (s *server) rollbackFeatureFlags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	var req rollbackFeatureFlagsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.ExpectedVersion == nil || req.Version < 1 {
		api.WriteProblem(w, api.ErrValidation("expected_version and positive version are required"))
		return
	}
	s.publishFeatureFlags(w, r, store, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: *req.ExpectedVersion, RestoreVersion: req.Version, Actor: acct.ID})
}
func (s *server) publishFeatureFlags(w http.ResponseWriter, r *http.Request, store state.FeatureFlagStore, u state.FeatureFlagUpdate) {
	v, err := store.UpdateFeatureFlags(r.Context(), u)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "flags.configuration_published", &u.Scope.AccountID, map[string]any{"project_id": u.Scope.ProjectID, "environment_id": u.Scope.EnvironmentID, "version": v.Version, "restored_from": v.RestoredFrom})
	writeJSON(w, http.StatusOK, v)
}
func (s *server) inspectFeatureFlag(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	var req inspectFeatureFlagRequest
	if err := decodeJSON(r, &req); err != nil || req.Version < 0 || !flags.ValidKey(r.PathValue("key")) || req.FallbackVariant != "" && !flags.ValidKey(req.FallbackVariant) || req.SubjectID != "" && (!flags.ValidSubjectID(req.SubjectID) || req.CustomerID == "") {
		api.WriteProblem(w, api.ErrValidation("invalid decision context"))
		return
	}
	if req.CustomerID != "" {
		id, err := uuid.Parse(req.CustomerID)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("customer_id must be a platform customer UUID"))
			return
		}
		req.CustomerID = id.String()
		tenants, ok := s.store.(state.PlatformTenantStore)
		if !ok {
			api.WriteProblem(w, api.ErrInternal("tenant store unavailable"))
			return
		}
		if _, err := tenants.GetPlatformTenant(r.Context(), acct.ID, req.CustomerID); err != nil {
			writeFeatureFlagError(w, err)
			return
		}
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, req.Version)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	key := r.PathValue("key")
	for _, definition := range v.Flags {
		if definition.Key == key && definition.Type == "variant" {
			writeJSON(w, http.StatusOK, flags.EvaluateVariantForSubject(v.Bundle, key, req.CustomerID, req.SubjectID, req.FallbackVariant))
			return
		}
	}
	if req.FallbackVariant != "" {
		writeJSON(w, http.StatusOK, flags.EvaluateVariantForSubject(v.Bundle, key, req.CustomerID, req.SubjectID, req.FallbackVariant))
		return
	}
	writeJSON(w, http.StatusOK, flags.EvaluateForSubject(v.Bundle, key, req.CustomerID, req.SubjectID, req.Fallback))
}
func (s *server) runtimeFeatureFlagScope(r *http.Request) (state.FeatureFlagScope, error) {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	c, err := s.flagsWorkloadVerifier.Verify(strings.TrimPrefix(raw, "Bearer "), workloadidentity.FlagsAudience, time.Now())
	if err != nil {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	app, err := s.store.AppByID(r.Context(), c.AppID)
	if err != nil {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	acct, err := s.store.AccountByID(r.Context(), c.AccountID)
	if err != nil || acct.Status != state.AccountActive || app.AccountID != acct.ID || app.ProjectID == "" || app.PreviewOfSlug != "" || app.Status == state.AppDeleted {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	instance, err := s.store.InstanceByID(r.Context(), c.InstanceID)
	if err != nil || instance.AppID != app.ID || (instance.State != string(state.StateRunning) && instance.State != string(state.StateWaking) && instance.State != string(state.StateColdBooting)) {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	dep, err := s.store.DeploymentByID(r.Context(), instance.DeploymentID)
	if err != nil || dep.AppID != app.ID {
		return state.FeatureFlagScope{}, errFlagsWorkloadIdentity
	}
	environment := dep.Scope
	if environment == "" || environment == "default" {
		environment = "production"
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, app.ProjectID, environment)
	if err != nil {
		return state.FeatureFlagScope{}, err
	}
	return state.FeatureFlagScope{AccountID: acct.ID, ProjectID: app.ProjectID, EnvironmentID: env.ID}, nil
}
func (s *server) runtimeFeatureFlags(w http.ResponseWriter, r *http.Request) {
	if !s.featureFlagsEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags require operator qualification"))
		return
	}
	scope, err := s.runtimeFeatureFlagScope(r)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "a live workload token for gregale:flags is required"))
		return
	}
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, 0)
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	if featureFlagConfigRequiresSubjectTargeting(v.Config) && !supportsFlagCapability(r.Header.Get(api.FlagSDKCapabilitiesHeader), api.FlagSDKSubjectTargetingCapability) {
		api.WriteProblem(w, api.NewProblem(http.StatusUpgradeRequired, "flags_sdk_capability_required", "Runtime SDK update required", "this configuration uses subject targeting; update the runtime SDK to a version that supports subject-targeting-v1"))
		return
	}
	// Refresh responses never become shared caches of customer targeting lists.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", fmt.Sprintf(`"flags-%s-%d"`, scope.EnvironmentID, v.Version))
	writeJSON(w, http.StatusOK, v.Bundle)
}

func featureFlagConfigRequiresSubjectTargeting(config flags.Config) bool {
	for _, definition := range config.Flags {
		if !definition.Enabled {
			continue
		}
		for _, rule := range definition.Rules {
			if len(rule.Subjects) > 0 || rule.RolloutUnit == "subject" {
				return true
			}
		}
	}
	return false
}

func supportsFlagCapability(header, capability string) bool {
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == capability {
			return true
		}
	}
	return false
}
