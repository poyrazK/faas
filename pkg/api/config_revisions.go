package api

import (
	"fmt"
	"net/http"
)

// Config revisions and config guards (ADR-1451).

// ConfigGuardSummary is the guard an env or secret write opened or joined.
type ConfigGuardSummary struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	BaseRevision  int64  `json:"base_revision"`
	WindowMinutes int    `json:"window_minutes,omitempty"`
}

// ConfigChangeReceipt is the additive part of an env or secret write
// response: the config revision the write recorded and the guard watching it.
type ConfigChangeReceipt struct {
	Revision int64               `json:"revision"`
	Guard    *ConfigGuardSummary `json:"guard,omitempty"`
}

// ConfigGuardDecision explains a closed guard: the rule, a one-line summary,
// and the new-config and old-config samples.
type ConfigGuardDecision struct {
	Rule             string   `json:"rule"`
	Summary          string   `json:"summary"`
	NewRequests      int64    `json:"new_requests"`
	NewServerErrors  int64    `json:"new_server_errors"`
	OldRequests      int64    `json:"old_requests"`
	OldServerErrors  int64    `json:"old_server_errors"`
	RestoredRevision int64    `json:"restored_revision,omitempty"`
	NotRestorable    []string `json:"not_restorable,omitempty"`
}

// ConfigGuard is a scope's config guard and, once closed, its verdict.
type ConfigGuard struct {
	ID           string               `json:"id"`
	Scope        string               `json:"scope"`
	Status       string               `json:"status"`
	BaseRevision int64                `json:"base_revision"`
	HeadRevision int64                `json:"head_revision"`
	Restorable   bool                 `json:"restorable"`
	OpenedAt     string               `json:"opened_at"`
	LastChangeAt string               `json:"last_change_at"`
	WindowEndsAt string               `json:"window_ends_at,omitempty"`
	ClosedAt     string               `json:"closed_at,omitempty"`
	Decision     *ConfigGuardDecision `json:"decision,omitempty"`
}

// ConfigRevision is one recorded config change, without values.
type ConfigRevision struct {
	Revision         int64  `json:"revision"`
	Scope            string `json:"scope"`
	Source           string `json:"source"`
	Key              string `json:"key,omitempty"`
	Actor            string `json:"actor"`
	CreatedAt        string `json:"created_at"`
	VariableCount    int    `json:"variable_count"`
	SecretCount      int    `json:"secret_count"`
	GuardID          string `json:"guard_id,omitempty"`
	GuardStatus      string `json:"guard_status,omitempty"`
	RestoredRevision int64  `json:"restored_revision,omitempty"`
}

// ConfigRevisionDetail is a revision with its variables and secret versions.
// Secret values and fingerprints are never returned.
type ConfigRevisionDetail struct {
	ConfigRevision
	Variables      map[string]string `json:"variables"`
	SecretVersions map[string]int64  `json:"secret_versions"`
}

// ListConfigRevisionsResponse is a scope's revisions, newest first, with
// its newest guard.
type ListConfigRevisionsResponse struct {
	Revisions []ConfigRevision `json:"revisions"`
	Guard     *ConfigGuard     `json:"guard,omitempty"`
}

// ConfigRollbackRequest restores a scope's variables to a revision. The
// scope is the `?scope=` query parameter, as on the env endpoints.
type ConfigRollbackRequest struct {
	Revision int64 `json:"revision"`
}

// ConfigSecretDifference is a secret a config rollback could not restore.
type ConfigSecretDifference struct {
	Key            string `json:"key"`
	TargetVersion  int64  `json:"target_version,omitempty"`
	CurrentVersion int64  `json:"current_version,omitempty"`
}

// ConfigRollbackResponse reports what a config rollback changed.
type ConfigRollbackResponse struct {
	RestoredRevision   int64                    `json:"restored_revision"`
	Change             *ConfigChangeReceipt     `json:"change,omitempty"`
	Set                []string                 `json:"set"`
	Deleted            []string                 `json:"deleted"`
	SecretsNotRestored []ConfigSecretDifference `json:"secrets_not_restored"`
}

// ErrConfigRevisionNotFound is returned when a scope has no such revision.
func ErrConfigRevisionNotFound(scope string, revision int64) *Problem {
	return NewProblem(http.StatusNotFound, CodeConfigRevisionNotFound, "Config revision not found",
		fmt.Sprintf("Scope %q has no config revision %d.", scope, revision)).
		WithHint("List revisions with `gregale env history`; the newest " + fmt.Sprint(ConfigRevisionsRetained) + " are kept.").
		WithDocs(docsBase + "/env#config-history")
}

// ErrConfigGuardNotFound is returned when a scope has never had a guard.
func ErrConfigGuardNotFound(scope string) *Problem {
	return NewProblem(http.StatusNotFound, CodeConfigGuardNotFound, "No config guard",
		fmt.Sprintf("No config change in scope %q has been guarded.", scope)).
		WithDocs(docsBase + "/env#config-guard")
}
