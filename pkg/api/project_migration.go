package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// Only the request carries values. Durable project migration receipts contain
// key names and deployment identities, never source tokens or secret values.
type CreateProjectMigrationRequest struct {
	ID                       string                    `json:"id"`
	PlanToken                string                    `json:"plan_token"`
	Only                     []string                  `json:"only"`
	InitialSecrets           ProjectInitialSecrets     `json:"initial_secrets,omitempty"`
	ServiceConnections       ProjectServiceConnections `json:"service_connections,omitempty"`
	PublicServiceConnections ProjectServiceConnections `json:"public_service_connections,omitempty"`
	PublicBuildURLs          ProjectPublicBuildURLs    `json:"public_build_urls,omitempty"`
	BuildDependencies        map[string][]string       `json:"build_dependencies,omitempty"`
}
type ResumeProjectMigrationRequest struct {
	ExpectedVersion int64                 `json:"expected_version"`
	Only            []string              `json:"only"`
	PlanToken       string                `json:"plan_token"`
	InitialSecrets  ProjectInitialSecrets `json:"initial_secrets,omitempty"`
}
type ProjectMigrationService struct {
	Name                     string                     `json:"name"`
	App                      string                     `json:"app,omitempty"`
	AppID                    string                     `json:"app_id,omitempty"`
	DeploymentID             string                     `json:"deployment_id,omitempty"`
	BuildID                  string                     `json:"build_id,omitempty"`
	Attempt                  int                        `json:"attempt"`
	Status                   string                     `json:"status"`
	Reason                   string                     `json:"reason,omitempty"`
	DependsOn                []string                   `json:"depends_on"`
	RequiredEnvKeys          []string                   `json:"required_env_keys"`
	ConfiguredKeys           []string                   `json:"configured_keys"`
	ServiceConnections       map[string]string          `json:"service_connections,omitempty"`
	PublicServiceConnections map[string]string          `json:"public_service_connections,omitempty"`
	PublicBuildURLs          map[string]string          `json:"public_build_urls,omitempty"`
	BuildDependencies        []string                   `json:"build_dependencies,omitempty"`
	BuildWait                *ProjectMigrationBuildWait `json:"build_wait,omitempty"`
	VerificationID           string                     `json:"verification_id,omitempty"`
	VerificationStatus       string                     `json:"verification_status,omitempty"`
	CutoverID                string                     `json:"cutover_id,omitempty"`
	CutoverStatus            string                     `json:"cutover_status,omitempty"`
}

// ProjectMigrationBuildWait describes a saved wait before a builder slot is used.
type ProjectMigrationBuildWait struct {
	Status     string    `json:"status"`
	Code       string    `json:"code,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	DeadlineAt time.Time `json:"deadline_at"`
}
type ProjectMigration struct {
	Version      int64                     `json:"version"`
	ID           string                    `json:"id"`
	Project      string                    `json:"project"`
	ProjectID    string                    `json:"project_id,omitempty"`
	SourceSHA256 string                    `json:"source_sha256"`
	Status       string                    `json:"status"`
	Reason       string                    `json:"reason,omitempty"`
	Services     []ProjectMigrationService `json:"services"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	RetryAfter   *time.Time                `json:"retry_after,omitempty"`
}
type ProjectMigrationList struct {
	Migrations []ProjectMigration `json:"migrations"`
}

func ValidProjectMigrationID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func (c *Client) CreateProjectMigration(ctx context.Context, in CreateProjectMigrationRequest) (ProjectMigration, error) {
	var out ProjectMigration
	err := c.do(ctx, http.MethodPost, "/v1/project-migrations", in, &out)
	return out, err
}
func (c *Client) GetProjectMigration(ctx context.Context, id string) (ProjectMigration, error) {
	var out ProjectMigration
	err := c.do(ctx, http.MethodGet, "/v1/project-migrations/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ListProjectMigrations(ctx context.Context, project string) (ProjectMigrationList, error) {
	var out ProjectMigrationList
	err := c.do(ctx, http.MethodGet, "/v1/project-migrations?project="+url.QueryEscape(project), nil, &out)
	return out, err
}
func (c *Client) ResumeProjectMigration(ctx context.Context, id string, in ResumeProjectMigrationRequest) (ProjectMigration, error) {
	var out ProjectMigration
	err := c.do(ctx, http.MethodPost, "/v1/project-migrations/"+url.PathEscape(id)+"/resume", in, &out)
	return out, err
}
