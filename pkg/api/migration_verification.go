package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MigrationDependencyChecks selects existing deployment environment by name.
// Connection strings and credentials are never part of verification intent.
type MigrationDependencyChecks struct {
	PostgresEnvKey      string                            `json:"postgres_env_key,omitempty"`
	ObjectStoragePrefix string                            `json:"object_storage_prefix,omitempty"`
	HTTPEnvKey          string                            `json:"http_env_key,omitempty"`
	ServiceConnections  []MigrationServiceConnectionCheck `json:"service_connections,omitempty"`
}

type CreateMigrationVerificationRequest struct {
	ID           string                    `json:"id"`
	DeploymentID string                    `json:"deployment_id"`
	Checks       MigrationDependencyChecks `json:"checks"`
	SmokePath    string                    `json:"smoke_path"`
	ColdWake     bool                      `json:"cold_wake"`
}

func (r CreateMigrationVerificationRequest) Validate() error {
	id, err := uuid.Parse(r.ID)
	if err != nil || id == uuid.Nil || id.String() != r.ID {
		return ErrValidation("id must be a canonical UUID")
	}
	dep, err := uuid.Parse(r.DeploymentID)
	if err != nil || dep == uuid.Nil {
		return ErrValidation("deployment_id must be a UUID")
	}
	if err := r.Checks.Validate(); err != nil {
		return err
	}
	path, err := NormalizeServiceBindingSmokePath(r.SmokePath)
	// Queries often contain credentials; migration checks persist paths only.
	if err != nil || path != r.SmokePath || strings.ContainsAny(path, "?#") || len(path) > MigrationVerificationMaxPathBytes {
		return ErrValidation("smoke_path must be an absolute path without a query or fragment")
	}
	return nil
}

func (c MigrationDependencyChecks) Validate() error {
	if len(c.ServiceConnections) > ProjectMigrationMaxServiceConnections {
		return ErrValidation("too many runtime service connections")
	}
	seen := map[string]bool{}
	for _, connection := range c.ServiceConnections {
		if ValidateMigrationConnectionKey(connection.EnvKey) != nil || seen[connection.EnvKey] || !IsServiceBindingTargetProbeCommand([]string{AppTaskServiceBindingTargetProbeCommand, connection.Service, connection.DeploymentID}, false) {
			return ErrValidation("service checks require unique runtime keys, canonical service names and exact deployment UUIDs")
		}
		seen[connection.EnvKey] = true
	}
	for _, key := range []string{c.PostgresEnvKey, c.HTTPEnvKey} {
		if key != "" && ValidateEnvKey(key) != nil {
			return ErrValidation("dependency environment key is invalid")
		}
	}
	if c.ObjectStoragePrefix != "" && !ValidObjectStorageBindingPrefix(c.ObjectStoragePrefix) {
		return ErrValidation("object storage prefix is invalid")
	}
	return nil
}

const AppTaskMigrationDependenciesCommand = "__gregale_migration_dependencies_v1__"

func MigrationDependenciesCommand(c MigrationDependencyChecks) []string {
	raw, _ := json.Marshal(c)
	return []string{AppTaskMigrationDependenciesCommand, string(raw)}
}

func ParseMigrationDependenciesCommand(command []string, shell bool) (MigrationDependencyChecks, bool) {
	var c MigrationDependencyChecks
	if shell || len(command) != 2 || command[0] != AppTaskMigrationDependenciesCommand {
		return c, false
	}
	d := json.NewDecoder(strings.NewReader(command[1]))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || c.Validate() != nil {
		return c, false
	}
	return c, command[1] == MigrationDependenciesCommand(c)[1]
}

type MigrationVerificationCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type MigrationDependencyReport struct {
	Checks []MigrationVerificationCheck `json:"checks"`
}

type MigrationVerification struct {
	ID                    string                             `json:"id"`
	App                   string                             `json:"app"`
	DeploymentID          string                             `json:"deployment_id"`
	Scope                 string                             `json:"scope"`
	ConfigurationRevision string                             `json:"configuration_revision"`
	Status                string                             `json:"status"`
	Phase                 string                             `json:"phase"`
	Reason                string                             `json:"reason,omitempty"`
	Request               CreateMigrationVerificationRequest `json:"request"`
	Checks                []MigrationVerificationCheck       `json:"checks"`
	DependencyTaskID      string                             `json:"dependency_task_id,omitempty"`
	ColdDependencyTaskID  string                             `json:"cold_dependency_task_id,omitempty"`
	WakeID                string                             `json:"wake_id,omitempty"`
	InstanceID            string                             `json:"instance_id,omitempty"`
	CreatedAt             time.Time                          `json:"created_at"`
	ExpiresAt             time.Time                          `json:"expires_at"`
	DrainedAt             *time.Time                         `json:"drained_at,omitempty"`
	CompletedAt           *time.Time                         `json:"completed_at,omitempty"`
	CancelRequestedAt     *time.Time                         `json:"cancel_requested_at,omitempty"`
	RecoveryStatus        string                             `json:"recovery_status,omitempty"`
}

type MigrationVerificationList struct {
	Runs []MigrationVerification `json:"runs"`
}

func migrationVerificationPath(app, id string) string {
	p := "/v1/apps/" + url.PathEscape(app) + "/migration-verifications"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}
func (c *Client) CreateMigrationVerification(ctx context.Context, app string, in CreateMigrationVerificationRequest) (MigrationVerification, error) {
	var out MigrationVerification
	err := c.do(ctx, http.MethodPost, migrationVerificationPath(app, ""), in, &out)
	return out, err
}
func (c *Client) GetMigrationVerification(ctx context.Context, app, id string) (MigrationVerification, error) {
	var out MigrationVerification
	err := c.do(ctx, http.MethodGet, migrationVerificationPath(app, id), nil, &out)
	return out, err
}
func (c *Client) ListMigrationVerifications(ctx context.Context, app string) (MigrationVerificationList, error) {
	var out MigrationVerificationList
	err := c.do(ctx, http.MethodGet, migrationVerificationPath(app, ""), nil, &out)
	return out, err
}
func (c *Client) AdvanceMigrationVerification(ctx context.Context, app, id string) (MigrationVerification, error) {
	var out MigrationVerification
	err := c.do(ctx, http.MethodPost, migrationVerificationPath(app, id)+"/advance", nil, &out)
	return out, err
}

func (c *Client) CancelMigrationVerification(ctx context.Context, app, id string) (MigrationVerification, error) {
	var out MigrationVerification
	err := c.do(ctx, http.MethodPost, migrationVerificationPath(app, id)+"/cancel", nil, &out)
	return out, err
}
