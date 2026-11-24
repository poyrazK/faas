package apidserver

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/flagedge"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

var (
	errFlagsWorkloadIdentity = errors.New("flags: invalid workload identity")
	// errFlagsNotReleased means the customer_flags platform flag (ADR-1700)
	// does not include the workload's account.
	errFlagsNotReleased = errors.New("flags: not enabled for this account")
)

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
	// Attributes simulates request attributes (gregale.* and app.*) for
	// attribute-targeted rules (ADR 20261114150000 §2).
	Attributes map[string]string `json:"attributes,omitempty"`
}

// validInspectAttributes bounds simulated attributes like the SDKs bound
// real ones: known names, valid values, at most the app attribute limit
// plus the two gateway attributes.
func validInspectAttributes(attrs map[string]string) bool {
	if len(attrs) > api.FlagsMaxAppAttributes+2 {
		return false
	}
	for name, value := range attrs {
		if !flags.ValidAttributeName(name) || !flags.ValidAttributeValue(value) {
			return false
		}
	}
	return true
}

func (s *server) featureFlagScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FeatureFlagScope, state.FeatureFlagStore, bool) {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.AppID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account key required", "project-wide flags require an account-scoped credential"))
		return state.FeatureFlagScope{}, nil, false
	}
	if !s.platformFlagFor(platformflags.CustomerFlags, flagSubject(r, acct)) {
		// Without a hint the CLI falls back to its generic 403 advice to
		// check the profile and credentials, which this is not (hunt #9 H9-43).
		hint := "Feature flags are not enabled on this Gregale installation; an operator must qualify them first. Your credentials are not the cause."
		if s.platformFlagOn(platformflags.CustomerFlags) {
			hint = "Feature flags are not enabled for this account yet; an operator releases them account by account. Your credentials are not the cause."
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags require operator qualification").
			WithHint(hint))
		return state.FeatureFlagScope{}, nil, false
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return state.FeatureFlagScope{}, nil, false
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		writeFeatureFlagLookupError(w, err, fmt.Sprintf("environment %q not found in project %s", r.PathValue("environment"), project.Slug))
		return state.FeatureFlagScope{}, nil, false
	}
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
	}
	return state.FeatureFlagScope{AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: env.ID}, store, ok
}

// featureFlagWriteScope is featureFlagScope for a change. Flags change what
// the environment's code does, so a protected environment other than
// production takes them from an approved project plan or promotion, like its
// variables, secrets, routes and policies (ADR-20261019071733 amendment 12).
// Production keeps direct edits (ADR-233).
func (s *server) featureFlagWriteScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FeatureFlagScope, state.FeatureFlagStore, bool) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return scope, store, false
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, scope.ProjectID, r.PathValue("environment"))
	if err != nil {
		writeFeatureFlagError(w, err)
		return state.FeatureFlagScope{}, nil, false
	}
	if env.Protected && !state.IsProductionScope(env.Slug) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Protected environment",
			"apply flag changes through an approved project plan or environment promotion"))
		return state.FeatureFlagScope{}, nil, false
	}
	return scope, store, true
}

// writeFeatureFlagLookupError names what one lookup did not find. The shared
// "environment, version, or customer" detail left the caller guessing which
// (Hunt 32 H32-72).
func writeFeatureFlagLookupError(w http.ResponseWriter, err error, missing string) {
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", missing))
		return
	}
	writeFeatureFlagError(w, err)
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
	scope, store, ok := s.featureFlagWriteScope(w, r, acct)
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
	if !s.edgeRoutesAllowed(r, acct, store, scope, req.Config) {
		writeEdgeFlagsUnavailable(w)
		return
	}
	s.publishFeatureFlags(w, r, store, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: *req.ExpectedVersion, Config: req.Config, Actor: acct.ID})
}

// edgeRoutesAllowed refuses a publish that adds or changes edge routes while
// the edge_flags platform flag is off (ADR-20261124212701). The flag is
// decided for the project's owner, the subject the gateway routes for, not
// the member who publishes. An edit that leaves existing routes as they are
// publishes, so turning the flag off never blocks kills or rollouts.
func (s *server) edgeRoutesAllowed(r *http.Request, acct state.Account, store state.FeatureFlagStore, scope state.FeatureFlagScope, next flags.Config) bool {
	released := flags.HasEdge(next) && s.platformFlagFor(platformflags.EdgeFlags, s.edgeFlagsSubject(r, acct, scope))
	return flagedge.PublishAllowed(released, next, func() (flags.Config, error) {
		current, err := store.GetFeatureFlags(r.Context(), scope, 0)
		return current.Config, err
	})
}

// edgeFlagsSubject is the project owner's account and plan, as gatewayd
// sees them on the app; the acting account when the owner cannot be read.
func (s *server) edgeFlagsSubject(r *http.Request, acct state.Account, scope state.FeatureFlagScope) platformflags.Subject {
	owner := acct
	if !sameAccountID(acct.ID, scope.AccountID) {
		if loaded, err := s.store.AccountByID(r.Context(), scope.AccountID); err == nil {
			owner = loaded
		}
	}
	return platformflags.Subject{AccountID: owner.ID, Plan: string(owner.Plan)}
}

func writeEdgeFlagsUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Edge flags unavailable", "edge flag routing is not enabled for this account").
		WithHint("An operator releases edge flag routing account by account (ADR-20261124212701). Your credentials are not the cause."))
}

func sameAccountID(a, b string) bool {
	return strings.ReplaceAll(a, "-", "") == strings.ReplaceAll(b, "-", "")
}

func (s *server) rollbackFeatureFlags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagWriteScope(w, r, acct)
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
	// Restoring a version republishes its edge routes: the same gate applies.
	if restored, err := store.GetFeatureFlags(r.Context(), scope, req.Version); err == nil && !s.edgeRoutesAllowed(r, acct, store, scope, restored.Config) {
		writeEdgeFlagsUnavailable(w)
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
	if err := decodeJSON(r, &req); err != nil || req.Version < 0 || !flags.ValidKey(r.PathValue("key")) || req.FallbackVariant != "" && !flags.ValidKey(req.FallbackVariant) || req.SubjectID != "" && (!flags.ValidSubjectID(req.SubjectID) || req.CustomerID == "") || !validInspectAttributes(req.Attributes) {
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
			writeFeatureFlagLookupError(w, err, fmt.Sprintf("customer %s is not a platform customer of this account; register it with gregale api tenant create", req.CustomerID))
			return
		}
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, req.Version)
	if err != nil {
		missing := "no flag configuration is published in this environment"
		if req.Version > 0 {
			missing = fmt.Sprintf("flag configuration version %d not found", req.Version)
		}
		writeFeatureFlagLookupError(w, err, missing)
		return
	}
	key := r.PathValue("key")
	evalCtx := flags.EvaluationContext{Customer: req.CustomerID, Subject: req.SubjectID, Attributes: req.Attributes}
	for _, definition := range v.Flags {
		if definition.Key == key && definition.Type == "variant" {
			writeJSON(w, http.StatusOK, flags.EvaluateVariantWithContext(v.Bundle, key, evalCtx, req.FallbackVariant))
			return
		}
	}
	if req.FallbackVariant != "" {
		writeJSON(w, http.StatusOK, flags.EvaluateVariantWithContext(v.Bundle, key, evalCtx, req.FallbackVariant))
		return
	}
	writeJSON(w, http.StatusOK, flags.EvaluateWithContext(v.Bundle, key, evalCtx, req.Fallback))
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
	if !s.platformFlagFor(platformflags.CustomerFlags, appFlagSubject(acct, app)) {
		return state.FeatureFlagScope{}, errFlagsNotReleased
	}
	env, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, app.ProjectID, environment)
	if err != nil {
		return state.FeatureFlagScope{}, err
	}
	return state.FeatureFlagScope{AccountID: acct.ID, ProjectID: app.ProjectID, EnvironmentID: env.ID}, nil
}
func (s *server) runtimeFeatureFlags(w http.ResponseWriter, r *http.Request) {
	if !s.platformFlagOn(platformflags.CustomerFlags) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags require operator qualification"))
		return
	}
	scope, err := s.runtimeFeatureFlagScope(r)
	if errors.Is(err, errFlagsNotReleased) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Flags unavailable", "feature flags are not enabled for this account"))
		return
	}
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
	capabilities := r.Header.Get(api.FlagSDKCapabilitiesHeader)
	for _, required := range featureFlagRequiredCapabilities(v.Config) {
		if !supportsFlagCapability(capabilities, required.capability) {
			api.WriteProblem(w, api.NewProblem(http.StatusUpgradeRequired, "flags_sdk_capability_required", "Runtime SDK update required",
				fmt.Sprintf("this configuration uses %s; update the runtime SDK to a version that supports %s", required.feature, required.capability)))
			return
		}
	}
	// Refresh responses never become shared caches of customer targeting lists.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", fmt.Sprintf(`"flags-%s-%d"`, scope.EnvironmentID, v.Version))
	writeJSON(w, http.StatusOK, runtimeBundleForSDK(v.Bundle, capabilities))
}

// runtimeBundleForSDK leaves killed flags out of the bundle for an SDK
// without kill-v1: it would ignore "killed" and serve the configured default,
// while a missing flag makes it serve the application fallback, which is
// what a kill means (ADR 20261114150000 §3).
//
// Edge routes are the gateway's (ADR-20261124212701): no SDK reads them.
func runtimeBundleForSDK(b flags.Bundle, capabilities string) flags.Bundle {
	b.Config = flags.WithoutEdge(b.Config)
	if supportsFlagCapability(capabilities, api.FlagSDKKillCapability) || len(flags.KilledKeys(b.Config)) == 0 {
		return b
	}
	out := b
	out.Flags = make([]flags.Flag, 0, len(b.Flags))
	for _, f := range b.Flags {
		if !f.Killed {
			out.Flags = append(out.Flags, f)
		}
	}
	return out
}

type featureFlagCapability struct{ feature, capability string }

// featureFlagRequiredCapabilities lists the SDK capabilities an enabled
// flag's rules depend on. An SDK without one would evaluate the rule without
// its constraint, so the runtime endpoint refuses it with 426 instead.
func featureFlagRequiredCapabilities(config flags.Config) []featureFlagCapability {
	subjects, attributes, experiments, subjectExperiments := false, false, false, false
	for _, definition := range config.Flags {
		if !definition.Enabled {
			continue
		}
		for _, rule := range definition.Rules {
			subjects = subjects || len(rule.Subjects) > 0 || rule.RolloutUnit == "subject"
			attributes = attributes || len(rule.Attributes) > 0
			experiments = experiments || rule.Experiment != nil
			subjectExperiments = subjectExperiments || rule.Experiment != nil && rule.Experiment.Unit == flags.ExperimentUnitSubject
		}
	}
	var out []featureFlagCapability
	if subjects {
		out = append(out, featureFlagCapability{"subject targeting", api.FlagSDKSubjectTargetingCapability})
	}
	if attributes {
		out = append(out, featureFlagCapability{"attribute targeting", api.FlagSDKAttributeTargetingCapability})
	}
	// A rule experiment's layer slice and the holdout change who the rule
	// serves; an SDK that ignored them would serve the rule to everyone
	// (ADR 20261119173702 §2).
	if experiments {
		out = append(out, featureFlagCapability{"experiment layers and holdout", api.FlagSDKExperimentLayersCapability})
	}
	if subjectExperiments {
		out = append(out, featureFlagCapability{"subject experiment exposures", api.FlagSDKSubjectExposuresCapability})
	}
	return out
}

func supportsFlagCapability(header, capability string) bool {
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == capability {
			return true
		}
	}
	return false
}
