package apidserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
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

// changeTimelineFor is the change_timeline release gate (ADR-751).
func (s *server) changeTimelineFor(subject platformflags.Subject) bool {
	return s.platformFlagOr(platformflags.ChangeTimeline, subject, s.changeTimelineEnabled)
}

// seedEnvPlatformFlags records the environment gates that predate their
// platform flags as each flag's boot fallback (version 0). Live queries seed
// theirs in configureLiveQueries, where the rollout is parsed.
func (s *server) seedEnvPlatformFlags(getenv func(string) string) {
	authAll := false
	switch strings.ToLower(strings.TrimSpace(getenv("FAAS_APPLICATION_AUTH_ENABLED"))) {
	case "1", "true", "yes", "on":
		authAll = true
	}
	var authAccounts []string
	for _, id := range strings.Split(getenv("FAAS_APPLICATION_AUTH_ACCOUNTS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			authAccounts = append(authAccounts, id)
		}
	}
	s.seedPlatformFlag(platformflags.ApplicationAuth, platformflags.FromAccountList(authAll || len(authAccounts) > 0, authAll, authAccounts))
	s.seedPlatformFlag(platformflags.ChangeTimeline, platformflags.FromAccountList(s.changeTimelineEnabled, true, nil))
	s.seedPlatformFlag(platformflags.AnomalyAlerts, platformflags.FromAccountList(s.anomalyAlertsEnabled, true, nil))
	s.seedPlatformFlag(platformflags.TriggerBatchDelivery, platformflags.FromAccountList(s.triggerBatchDeliveryEnabled, true, nil))
}
