package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// ProjectReleaseCommand binds a backward-compatible migration to one workload.
// Output remains in the existing app-task log resource, never the deploy journal.
type ProjectReleaseCommand struct {
	AppID                string                                `json:"app_id"`
	Slug                 string                                `json:"slug"`
	CommandHash          string                                `json:"command_hash"`
	Compatibility        string                                `json:"compatibility"`
	Status               string                                `json:"status"`
	TaskID               string                                `json:"task_id,omitempty"`
	ExitCode             *int                                  `json:"exit_code,omitempty"`
	FailureCode          string                                `json:"failure_code,omitempty"`
	ContractCommandHash  string                                `json:"contract_command_hash,omitempty"`
	Contract             *ProjectMigrationContract             `json:"contract,omitempty"`
	VerifyBaseline       bool                                  `json:"verify_baseline,omitempty"`
	BaselineVerification *ProjectMigrationBaselineVerification `json:"baseline_verification,omitempty"`
}

// ProjectMigrationBaselineVerification records attributed critical-route health
// after the expansion task completes. It contains no command output or secrets.
type ProjectMigrationBaselineVerification struct {
	TaskID    string                        `json:"task_id"`
	ReleaseID string                        `json:"release_id"`
	StartedAt time.Time                     `json:"started_at"`
	CheckedAt time.Time                     `json:"checked_at"`
	Status    string                        `json:"status"`
	Evidence  []ProjectReleaseProbeEvidence `json:"evidence"`
}

func ProjectReleaseCommandHash(command []string, shell bool) string {
	raw, _ := json.Marshal(struct {
		Command []string
		Shell   bool
	}{command, shell})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func ProjectReleaseCommandPolicyID(command *ProjectReleaseCommand) string {
	if command == nil {
		return ""
	}
	identity := []string{command.AppID, command.Slug, command.CommandHash, command.Compatibility}
	if command.VerifyBaseline {
		identity = append(identity, "post-expansion-baseline")
	}
	if command.ContractCommandHash != "" {
		identity = append(identity, "contract", command.ContractCommandHash)
	}
	raw, _ := json.Marshal(identity)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// ContextWithProjectReleaseMigration asserts the declared command leaves the
// previous release usable after execution. It does not promise schema rollback.
func ContextWithProjectReleaseMigration(ctx context.Context) context.Context {
	mode := "atomic-migrate-verify"
	if len(projectReleaseConfigFromContext(ctx)) > 0 {
		mode = "atomic-config-migrate-verify"
	}
	return context.WithValue(ctx, projectSafeReleaseContextKey{}, mode)
}

// ProjectMigrationContract exposes admission and task outcomes without source commands.
type ProjectMigrationContract struct {
	Recovery           *ProjectMigrationRecovery `json:"recovery,omitempty"`
	Status             string                    `json:"status"`
	Reason             string                    `json:"reason,omitempty"`
	RequestID          string                    `json:"request_id,omitempty"`
	ExpectedGeneration uint64                    `json:"expected_generation,omitempty"`
	TaskID             string                    `json:"task_id,omitempty"`
	RetiredDeployments int                       `json:"retired_deployments"`
	ExitCode           *int                      `json:"exit_code,omitempty"`
	FailureCode        string                    `json:"failure_code,omitempty"`
}
