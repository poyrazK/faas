package apidserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
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

// platformFlagEnabled decides a platform flag for an account. It does not
// check the plan; callers that release a paid capability also check
// entitlement.
func (s *server) platformFlagEnabled(flagKey string, acct state.Account) bool {
	return s.platformFlags().Enabled(flagKey, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
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
