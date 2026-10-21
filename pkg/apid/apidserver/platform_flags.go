package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPlatformFlag records a boot-time fallback definition (ADR-1700) and
// installs it in the current runtime-configuration manager. The fallback is
// version 0, so a durable operator value always wins, before and after a
// restart. WithRuntimeConfigManager re-applies recorded fallbacks to the
// production manager.
func (s *server) seedPlatformFlag(flagKey string, def platformflags.Definition) {
	if s.platformFlagBootstrap == nil {
		s.platformFlagBootstrap = map[string]platformflags.Definition{}
	}
	s.platformFlagBootstrap[flagKey] = def
	if s.runtimeConfig == nil {
		s.runtimeConfig = newRuntimeConfigManager(nil)
	}
	s.runtimeConfig.seedPlatformFlag(flagKey, def)
}

// platformFlags is the compiled platform-flag holder. It is nil before any
// manager exists, which evaluates every flag as off.
func (s *server) platformFlags() *platformflags.Set {
	if s == nil || s.runtimeConfig == nil {
		return nil
	}
	return s.runtimeConfig.PlatformFlags()
}

// platformFlagEnabled decides a platform flag for an account outside any
// organization context. It does not check the plan; callers that release a
// paid capability also check entitlement.
func (s *server) platformFlagEnabled(flagKey string, acct state.Account) bool {
	return s.platformFlagFor(flagKey, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
}

// platformFlagFor decides a platform flag for a full subject.
func (s *server) platformFlagFor(flagKey string, subject platformflags.Subject) bool {
	return s.platformFlags().Enabled(flagKey, subject)
}

// flagSubject is who a request decides platform flags for (ADR
// 20261021080640): the account, its plan, and the organization the request
// acts under, read from request context with no I/O.
func flagSubject(r *http.Request, acct state.Account) platformflags.Subject {
	return platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: requestOrgID(r)}
}

// appFlagSubject decides for work on a loaded app: the app's organization.
func appFlagSubject(acct state.Account, app state.App) platformflags.Subject {
	return platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID}
}

// requestOrgID is the organization a request acts under. In order:
//  1. the active-org principal (/v1/orgs/{slug}/…, X-Active-Org);
//  2. the project, app or managed Postgres scope of a company resource;
//  3. the organization the API key was minted for.
//
// It is empty outside a company context, where org rules never match and
// account rules already target the person.
func requestOrgID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id, ok := authz.ActiveOrgID(r); ok && id != "" {
		return id
	}
	if id := projectaccess.OrganizationID(r.Context()); id != "" {
		return id
	}
	if grant, ok := appaccess.From(r); ok && grant.App.OrgID != "" {
		return grant.App.OrgID
	}
	if id := managedpostgres.Organization(r.Context()); id != "" {
		return id
	}
	if _, key, ok := middleware.AccountFromContext(r); ok && key != nil {
		return key.OrgID
	}
	return ""
}

// platformFlagOn reports a platform flag's kill switch, for paths that are
// not account-scoped (workers, operator-owned integrations).
func (s *server) platformFlagOn(flagKey string) bool {
	return s.platformFlags().Snapshot().GloballyEnabled(flagKey)
}

// checkRuntimeConfigChange validates a submitted runtime-configuration value
// and returns the form to store. Platform-flag values are normalized and
// cross-checked against the live snapshot: a rule cannot reference a group
// that does not exist, and a group cannot be removed while a flag uses it.
func (s *server) checkRuntimeConfigChange(def runtimeConfigDefinition, value json.RawMessage) (json.RawMessage, *api.Problem) {
	if err := validateRuntimeConfigValue(def, value); err != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration value", err.Error())
	}
	switch def.Kind {
	case runtimeConfigKindPlatformFlag:
		flag, _ := platformflags.ParseDefinition(value)
		groups, _ := s.platformFlags().Snapshot().Groups()
		if err := platformflags.ValidateDefinition(flag, groups); err != nil {
			return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Unknown platform flag group", err.Error())
		}
		if key, ok := platformflags.FlagKeyFromConfigKey(def.Key); ok {
			if spec, ok := platformflags.Lookup(key); ok {
				if err := platformflags.ValidateTargeting(spec, flag); err != nil {
					return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Rule targets an attribute this flag cannot evaluate", err.Error())
				}
				if err := platformflags.ValidateRamps(spec, flag); err != nil {
					return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Flag cannot ramp automatically", err.Error())
				}
			}
		}
		return flag.Marshal(), nil
	case runtimeConfigKindPlatformFlagGroups:
		groups, _ := platformflags.ParseGroups(value)
		if missing := s.missingReferencedGroups(groups); len(missing) > 0 {
			return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Platform flag group in use",
				fmt.Sprintf("remove these groups from the flags that use them first: %s", strings.Join(missing, "; ")))
		}
		return groups.Marshal(), nil
	case runtimeConfigKindPlatformFlagOptIns:
		optIns, _ := platformflags.ParseOptIns(value)
		if key, ok := platformflags.FlagKeyFromOptInsConfigKey(def.Key); ok {
			if spec, ok := platformflags.Lookup(key); ok {
				if err := platformflags.ValidateOptIns(spec, optIns); err != nil {
					return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Opt-in this flag cannot evaluate", err.Error())
				}
			}
		}
		return optIns.Marshal(), nil
	}
	return value, nil
}

// missingReferencedGroups lists "group (flag, flag)" for every group a
// current flag references that the proposed groups would remove.
func (s *server) missingReferencedGroups(proposed platformflags.Groups) []string {
	snapshot := s.platformFlags().Snapshot()
	defs := map[string]platformflags.Definition{}
	for _, spec := range platformflags.Catalog() {
		if def, _, ok := snapshot.Definition(spec.Key); ok {
			defs[spec.Key] = def
		}
	}
	var missing []string
	for group, flags := range platformflags.GroupReferences(defs) {
		if _, ok := proposed[group]; !ok {
			missing = append(missing, fmt.Sprintf("%s (%s)", group, strings.Join(flags, ", ")))
		}
	}
	slices.Sort(missing)
	return missing
}

// platformFlagOr decides a flag whose pre-ADR-1700 gate still runs in place:
// once an operator has written the flag (version > 0) it decides for the
// account; until then legacy, the existing gate, decides as before. The
// environment value is also seeded as version 0 so the operator surfaces show
// what is in effect.
func (s *server) platformFlagOr(flagKey string, subject platformflags.Subject, legacy bool) bool {
	if _, version, ok := s.platformFlags().Snapshot().Definition(flagKey); ok && version > 0 {
		return s.platformFlagFor(flagKey, subject)
	}
	return legacy
}

// applicationAuthEnabled is the application_auth release gate (ADR-1190).
func (s *server) applicationAuthEnabled(subject platformflags.Subject) bool {
	return s.platformFlagOr(platformflags.ApplicationAuth, subject, api.ApplicationAuthEnabled(subject.AccountID))
}

// guestTracingFor is the guest_tracing release gate (ADR-958). It decides by
// account and plan: creating an app has no organization yet.
func (s *server) guestTracingFor(acct state.Account) bool {
	return s.platformFlagOr(platformflags.GuestTracing, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}, s.guestTracingEnabled)
}

// staticEgressIPFor is the static_egress_ip release gate (ADR-119); the
// environment value is still re-read on every call until an operator writes
// the flag.
func (s *server) staticEgressIPFor(subject platformflags.Subject) bool {
	return s.platformFlagOr(platformflags.StaticEgressIP, subject, api.StaticEgressIPEnabled())
}

// changeTimelineFor is the change_timeline release gate (ADR-751).
func (s *server) changeTimelineFor(subject platformflags.Subject) bool {
	return s.platformFlagOr(platformflags.ChangeTimeline, subject, s.changeTimelineEnabled)
}

// seedEnvPlatformFlags records the environment gates that predate their
// platform flags as each flag's boot fallback (version 0). Live queries seed
// theirs in configureLiveQueries, where the rollout is parsed.
func (s *server) seedEnvPlatformFlags(getenv func(string) string) {
	authAll := envTruthy(getenv("FAAS_APPLICATION_AUTH_ENABLED"))
	authAccounts := envAccountList(getenv("FAAS_APPLICATION_AUTH_ACCOUNTS"))
	s.seedPlatformFlag(platformflags.ApplicationAuth, platformflags.FromAccountList(authAll || len(authAccounts) > 0, authAll, authAccounts))
	s.seedPlatformFlag(platformflags.ChangeTimeline, platformflags.FromAccountList(s.changeTimelineEnabled, true, nil))
	s.seedPlatformFlag(platformflags.AnomalyAlerts, platformflags.FromAccountList(s.anomalyAlertsEnabled, true, nil))
	s.seedPlatformFlag(platformflags.TriggerBatchDelivery, platformflags.FromAccountList(s.triggerBatchDeliveryEnabled, true, nil))
	schedules := envAccountList(getenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS"))
	s.seedPlatformFlag(platformflags.OperationSchedules, platformflags.FromAccountList(len(schedules) > 0, false, schedules))
	children := envAccountList(getenv("FAAS_OPERATION_CHILDREN_ACCOUNTS"))
	s.seedPlatformFlag(platformflags.OperationChildren, platformflags.FromAccountList(len(children) > 0, false, children))
	s.seedPlatformFlag(platformflags.ServiceMap, platformflags.FromAccountList(s.serviceMap.Enabled, true, nil))
	s.seedPlatformFlag(platformflags.GuestTracing, platformflags.FromAccountList(s.guestTracingEnabled, true, nil))
	s.seedPlatformFlag(platformflags.StaticEgressIP, platformflags.FromAccountList(envTruthy(getenv("FAAS_STATIC_EGRESS_IP_ENABLED")), true, nil))
	// Realtime previews (ADR 20261021080640): apid's own switches; a realtime
	// node keeps its own as a capability.
	s.seedPlatformFlag(platformflags.RealtimeResume, platformflags.FromAccountList(getenv("FAAS_REALTIME_RESUME_PREVIEW_ENABLED") == "1", true, nil))
	s.seedPlatformFlag(platformflags.RealtimeTickets, platformflags.FromAccountList(s.realtime.TicketsPreviewEnabled, true, nil))
	s.seedPlatformFlag(platformflags.RealtimeAccess, platformflags.FromAccountList(s.realtime.AccessPreviewEnabled, true, nil))
	s.seedPlatformFlag(platformflags.RealtimeOccupancy, platformflags.FromAccountList(s.realtime.OccupancyPreviewEnabled, true, nil))
}

// realtimeEndpointPreviews decides the realtime preview flags for one
// endpoint's account and app (ADR 20261021080640). Until an operator writes
// one of them it returns nil, so registrations stay byte-identical for older
// realtime nodes, which refuse an unknown field. Each decision defaults to on:
// realtimed still requires its own switch, so "on" defers to the node.
func (s *server) realtimeEndpointPreviews(ctx context.Context, accountID, appID string) (*realtime.EndpointPreviews, error) {
	snapshot := s.platformFlags().Snapshot()
	written := false
	for _, key := range platformflags.RealtimePreviews {
		if _, version, ok := snapshot.Definition(key); ok && version > 0 {
			written = true
		}
	}
	if !written {
		return nil, nil
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load account %s: %w", accountID, err)
	}
	app, err := s.store.AppByID(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("load app %s: %w", appID, err)
	}
	subject := appFlagSubject(acct, app)
	return &realtime.EndpointPreviews{
		Resume:    s.platformFlagOr(platformflags.RealtimeResume, subject, true),
		Tickets:   s.platformFlagOr(platformflags.RealtimeTickets, subject, true),
		Access:    s.platformFlagOr(platformflags.RealtimeAccess, subject, true),
		Occupancy: s.platformFlagOr(platformflags.RealtimeOccupancy, subject, true),
	}, nil
}

// registerPlatformFlagRetireBy exports each preview flag's retire-by date as
// apid_platform_flag_retire_by_timestamp_seconds{flag}, so the
// FaasPlatformFlagPastRetireBy alert reminds operators to fold a preview into
// general availability or remove it (ADR 20261021080640).
func registerPlatformFlagRetireBy(registry *prometheus.Registry) {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apid_platform_flag_retire_by_timestamp_seconds",
		Help: "Unix timestamp of a preview platform flag's retire-by date (start of day, UTC).",
	}, []string{"flag"})
	for _, spec := range platformflags.Catalog() {
		if at, ok := spec.RetireByTime(); ok {
			gauge.WithLabelValues(spec.Key).Set(float64(at.Unix()))
		}
	}
	registry.MustRegister(gauge)
}

// envAccountList splits a comma-separated account allowlist, dropping blanks.
func envAccountList(value string) []string {
	var out []string
	for _, id := range strings.Split(value, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// envTruthy is the 1/true/yes/on reading pkg/api's environment gates use.
func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
