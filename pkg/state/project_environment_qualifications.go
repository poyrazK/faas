package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const ProjectEnvironmentQualificationTTL = 24 * time.Hour

// ProjectEnvironmentQualification is an immutable, short-lived assertion
// about two fixed checks against one exact active release set and config
// version. It deliberately carries no command output, environment values, or
// secrets.
type ProjectEnvironmentQualification struct {
	ID                   string                                 `json:"id"`
	AccountID            string                                 `json:"account_id"`
	ProjectID            string                                 `json:"project_id"`
	EnvironmentSlug      string                                 `json:"environment"`
	ReleaseSetID         string                                 `json:"release_set_id"`
	ConfigurationVersion int64                                  `json:"configuration_version"`
	ConfigurationHash    string                                 `json:"configuration_hash"`
	WorkloadConfigHashes map[string]string                      `json:"workload_config_hashes"`
	SecretRevisionHashes map[string]string                      `json:"secret_revision_hashes"`
	Status               string                                 `json:"status"`
	Checks               []ProjectEnvironmentQualificationCheck `json:"checks"`
	CreatedAt            time.Time                              `json:"created_at"`
	ExpiresAt            time.Time                              `json:"expires_at"`
}

type ProjectEnvironmentQualificationCheck struct {
	Name    string                                  `json:"name"`
	Status  string                                  `json:"status"`
	Results []ProjectEnvironmentQualificationResult `json:"results"`
}

// ProjectEnvironmentQualificationResult records the bounded outcome for one
// exact workload deployment. Response bodies, headers, and arbitrary errors
// are deliberately excluded from the receipt.
type ProjectEnvironmentQualificationResult struct {
	WorkloadSlug string `json:"workload_slug"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	HTTPStatus   *int   `json:"http_status,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

// ProjectEnvironmentQualificationStore records qualification receipts and
// returns the latest receipt for an exact source release and config identity.
type ProjectEnvironmentQualificationStore interface {
	CreateProjectEnvironmentQualification(context.Context, string, string, string, string, int64, string, map[string]string, []ProjectEnvironmentQualificationCheck, map[string]string) (ProjectEnvironmentQualification, error)
	LatestProjectEnvironmentQualification(context.Context, string, string, string, string) (ProjectEnvironmentQualification, error)
}

var _ ProjectEnvironmentQualificationStore = (*PgStore)(nil)
var _ ProjectEnvironmentQualificationStore = (*MemStore)(nil)

func normalizeProjectEnvironmentQualificationChecks(checks []ProjectEnvironmentQualificationCheck) ([]ProjectEnvironmentQualificationCheck, string, error) {
	if len(checks) != 2 {
		return nil, "", ErrInvalidArgument
	}
	byName := make(map[string]string, len(checks))
	resultsByName := make(map[string]map[string]ProjectEnvironmentQualificationResult, len(checks))
	for _, check := range checks {
		if check.Name != "health" && check.Name != "smoke" {
			return nil, "", ErrInvalidArgument
		}
		if _, exists := byName[check.Name]; exists {
			return nil, "", ErrInvalidArgument
		}
		if len(check.Results) == 0 {
			return nil, "", ErrInvalidArgument
		}
		results := make(map[string]ProjectEnvironmentQualificationResult, len(check.Results))
		checkPassed := true
		for _, result := range check.Results {
			if !api.ValidProjectSlug(result.WorkloadSlug) {
				return nil, "", ErrInvalidArgument
			}
			if _, err := uuid.Parse(result.DeploymentID); err != nil {
				return nil, "", ErrInvalidArgument
			}
			if result.Status != "passed" && result.Status != "failed" {
				return nil, "", ErrInvalidArgument
			}
			if result.HTTPStatus != nil && (*result.HTTPStatus < 100 || *result.HTTPStatus > 599) {
				return nil, "", ErrInvalidArgument
			}
			if result.Status == "passed" {
				if result.HTTPStatus == nil || *result.HTTPStatus < 200 || *result.HTTPStatus > 299 || result.ErrorCode != "" {
					return nil, "", ErrInvalidArgument
				}
			} else {
				checkPassed = false
				if result.ErrorCode != "preview_unavailable" && result.ErrorCode != "request_failed" && result.ErrorCode != "unexpected_status" {
					return nil, "", ErrInvalidArgument
				}
			}
			key := result.WorkloadSlug
			if _, exists := results[key]; exists {
				return nil, "", ErrInvalidArgument
			}
			if result.HTTPStatus != nil {
				status := *result.HTTPStatus
				result.HTTPStatus = &status
			}
			results[key] = result
		}
		status := "passed"
		if !checkPassed {
			status = "failed"
		}
		if check.Status != status {
			return nil, "", ErrInvalidArgument
		}
		byName[check.Name] = status
		resultsByName[check.Name] = results
	}
	if len(byName) != 2 {
		return nil, "", ErrInvalidArgument
	}
	normalized := []ProjectEnvironmentQualificationCheck{
		{Name: "health", Status: byName["health"], Results: orderedQualificationResults(resultsByName["health"])},
		{Name: "smoke", Status: byName["smoke"], Results: orderedQualificationResults(resultsByName["smoke"])},
	}
	if !sameQualificationResultScope(resultsByName["health"], resultsByName["smoke"]) {
		return nil, "", ErrInvalidArgument
	}
	status := "passed"
	for _, check := range normalized {
		if check.Status != "passed" {
			status = "failed"
		}
	}
	return normalized, status, nil
}

func orderedQualificationResults(results map[string]ProjectEnvironmentQualificationResult) []ProjectEnvironmentQualificationResult {
	out := make([]ProjectEnvironmentQualificationResult, 0, len(results))
	for _, result := range results {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkloadSlug < out[j].WorkloadSlug })
	return out
}

func sameQualificationResultScope(left, right map[string]ProjectEnvironmentQualificationResult) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for slug, result := range left {
		other, ok := right[slug]
		if !ok || result.DeploymentID != other.DeploymentID {
			return false
		}
	}
	return true
}

func validateProjectEnvironmentQualificationScope(accountID, projectID, environment, releaseSetID string) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(projectID); err != nil {
		return ErrInvalidArgument
	}
	if !api.ValidProjectEnvironmentSlug(environment) {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(releaseSetID); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeProjectEnvironmentQualificationSecretRevisionHashes(hashes map[string]string, checks []ProjectEnvironmentQualificationCheck) (map[string]string, error) {
	if len(checks) == 0 || len(hashes) != len(checks[0].Results) {
		return nil, ErrInvalidArgument
	}
	normalized := make(map[string]string, len(hashes))
	for _, result := range checks[0].Results {
		hash, ok := hashes[result.WorkloadSlug]
		if !ok || !api.ValidProjectEnvironmentConfigHash(hash) {
			return nil, ErrInvalidArgument
		}
		normalized[result.WorkloadSlug] = hash
	}
	return normalized, nil
}

func scanProjectEnvironmentQualification(row pgx.Row) (ProjectEnvironmentQualification, error) {
	var qualification ProjectEnvironmentQualification
	var checks, secretRevisionHashes, workloadConfigHashes []byte
	if err := row.Scan(&qualification.ID, &qualification.AccountID, &qualification.ProjectID,
		&qualification.EnvironmentSlug, &qualification.ReleaseSetID,
		&qualification.ConfigurationVersion, &qualification.ConfigurationHash, &secretRevisionHashes, &workloadConfigHashes, &qualification.Status,
		&checks, &qualification.CreatedAt, &qualification.ExpiresAt); err != nil {
		return ProjectEnvironmentQualification{}, mapErr(err)
	}
	if err := json.Unmarshal(secretRevisionHashes, &qualification.SecretRevisionHashes); err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: decode environment qualification secret revisions: %w", err)
	}
	if err := json.Unmarshal(workloadConfigHashes, &qualification.WorkloadConfigHashes); err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: decode qualification workload configs: %w", err)
	}
	if err := json.Unmarshal(checks, &qualification.Checks); err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: decode environment qualification: %w", err)
	}
	return qualification, nil
}

func (s *PgStore) CreateProjectEnvironmentQualification(ctx context.Context, accountID, projectID, environment, releaseSetID string, configurationVersion int64, configurationHash string, secretRevisionHashes map[string]string, checks []ProjectEnvironmentQualificationCheck, workloadConfigHashes map[string]string) (ProjectEnvironmentQualification, error) {
	if err := validateProjectEnvironmentQualificationScope(accountID, projectID, environment, releaseSetID); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if configurationVersion < 0 || !api.ValidProjectEnvironmentConfigHash(configurationHash) {
		return ProjectEnvironmentQualification{}, ErrInvalidArgument
	}
	normalized, status, err := normalizeProjectEnvironmentQualificationChecks(checks)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	normalizedSecretRevisionHashes, err := normalizeProjectEnvironmentQualificationSecretRevisionHashes(secretRevisionHashes, normalized)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	checksJSON, err := json.Marshal(normalized)
	if err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: encode environment qualification: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: begin environment qualification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Receipt insertion takes this parent FK lock. Acquire it before the
	// environment lock, matching clone/promotion capture and flag writers.
	if _, err := sqlc.New().LockFeatureFlagProject(ctx, tx, sqlc.LockFeatureFlagProjectParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID)}); err != nil {
		return ProjectEnvironmentQualification{}, mapErr(err)
	}
	var environmentID string
	err = tx.QueryRow(ctx, `select id::text
		from project_environments
		where project_id = $1 and slug = $2
		for share`, projectID, environment).Scan(&environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentQualification{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentQualification{}, mapErr(err)
	}
	var activeID string
	err = tx.QueryRow(ctx, `select rs.id::text
		from project_release_sets rs
		join projects p on p.id = rs.project_id
		where rs.id = $1 and rs.project_id = $2 and rs.environment_slug = $3
		  and p.account_id = $4 and rs.active
		for share of rs`, releaseSetID, projectID, environment, accountID).Scan(&activeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	if err != nil {
		return ProjectEnvironmentQualification{}, mapErr(err)
	}
	currentConfig, err := projectEnvironmentConfigLatestTx(ctx, tx, accountID, projectID, environment)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if currentConfig.Version != configurationVersion || currentConfig.ConfigHash != configurationHash {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	currentSecretRevisionHashes, err := projectEnvironmentSecretRevisionHashesTx(ctx, tx, accountID, releaseSetID, environment)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if !sameProjectEnvironmentSecretRevisionHashes(normalizedSecretRevisionHashes, currentSecretRevisionHashes) {
		return ProjectEnvironmentQualification{}, ErrConflict
	}
	if err := validateQualificationResultsForReleaseSet(ctx, tx, accountID, releaseSetID, normalized); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	currentWorkloadHashes, scoped, err := projectEnvironmentWorkloadConfigHashesTx(ctx, tx, accountID, projectID, environment, releaseSetID)
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if err := validateQualificationWorkloadHashes(workloadConfigHashes, currentWorkloadHashes, scoped); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if workloadConfigHashes == nil {
		workloadConfigHashes = map[string]string{}
	}
	workloadConfigHashesJSON, err := json.Marshal(workloadConfigHashes)
	if err != nil {
		return ProjectEnvironmentQualification{}, ErrInvalidArgument
	}
	secretRevisionHashesJSON, err := json.Marshal(normalizedSecretRevisionHashes)
	if err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: encode environment qualification secret revisions: %w", err)
	}
	qualification, err := scanProjectEnvironmentQualification(tx.QueryRow(ctx, `
		insert into project_environment_qualifications
			(account_id, project_id, environment_slug, release_set_id, configuration_version, configuration_hash, secret_revision_hashes, workload_config_hashes, status, checks, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10::jsonb, now() + interval '24 hours')
		returning id, account_id, project_id, environment_slug, release_set_id,
		          configuration_version, configuration_hash, secret_revision_hashes, workload_config_hashes, status, checks, created_at, expires_at`,
		accountID, projectID, environment, releaseSetID, configurationVersion, configurationHash, secretRevisionHashesJSON, workloadConfigHashesJSON, status, checksJSON))
	if err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentQualification{}, fmt.Errorf("state: commit environment qualification: %w", err)
	}
	return qualification, nil
}

func projectEnvironmentSecretRevisionHashesTx(ctx context.Context, tx pgx.Tx, accountID, releaseSetID, environment string) (map[string]string, error) {
	revisionsByWorkload := make(map[string][]api.ProjectEnvironmentSecretRevision)
	rows, err := tx.Query(ctx, `
		select a.slug, coalesce(secret.key, ''), coalesce(secret.secret_version, 0),
		       case when secret.managed_postgres_binding_id is not null then 'managed_postgres'
		            when secret.managed_object_storage_credential_id is not null then 'object_storage' else '' end,
		       coalesce(secret.managed_postgres_binding_id::text, secret.managed_object_storage_credential_id::text, ''),
		       coalesce(secret.managed_credential_generation, 0)
		from project_release_members member
		join apps a on a.id = member.app_id
		left join app_secrets secret on secret.account_id = a.account_id and secret.app_id = a.id and secret.scope = $3
		where member.release_id = $1 and a.account_id = $2
		order by a.slug, secret.key`, releaseSetID, accountID, environment)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var workloadSlug, key, managedBy, bindingID string
		var version, credentialGeneration int64
		if err := rows.Scan(&workloadSlug, &key, &version, &managedBy, &bindingID, &credentialGeneration); err != nil {
			return nil, mapErr(err)
		}
		if _, exists := revisionsByWorkload[workloadSlug]; !exists {
			revisionsByWorkload[workloadSlug] = nil
		}
		if key != "" {
			revisionsByWorkload[workloadSlug] = append(revisionsByWorkload[workloadSlug], api.ProjectEnvironmentSecretRevision{
				Key: key, Version: version, ManagedBy: managedBy, BindingID: bindingID,
				CredentialGeneration: credentialGeneration,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	hashes := make(map[string]string, len(revisionsByWorkload))
	for workloadSlug, revisions := range revisionsByWorkload {
		hash, err := api.ProjectEnvironmentSecretRevisionHash(revisions)
		if err != nil {
			return nil, fmt.Errorf("state: fingerprint environment secret revisions: %w", err)
		}
		hashes[workloadSlug] = hash
	}
	return hashes, nil
}

func sameProjectEnvironmentSecretRevisionHashes(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for workload, hash := range left {
		if right[workload] != hash {
			return false
		}
	}
	return true
}

func validateQualificationResultsForReleaseSet(ctx context.Context, tx pgx.Tx, accountID, releaseSetID string, checks []ProjectEnvironmentQualificationCheck) error {
	rows, err := tx.Query(ctx, `
		select a.slug, member.deployment_id::text
		from project_release_members member
		join apps a on a.id = member.app_id
		where member.release_id = $1 and a.account_id = $2
		order by a.slug`, releaseSetID, accountID)
	if err != nil {
		return mapErr(err)
	}
	defer rows.Close()
	members := make(map[string]string)
	for rows.Next() {
		var slug, deploymentID string
		if err := rows.Scan(&slug, &deploymentID); err != nil {
			return mapErr(err)
		}
		members[slug] = deploymentID
	}
	if err := rows.Err(); err != nil {
		return mapErr(err)
	}
	if len(members) == 0 {
		return ErrConflict
	}
	for _, check := range checks {
		if len(check.Results) != len(members) {
			return ErrInvalidArgument
		}
		for _, result := range check.Results {
			if members[result.WorkloadSlug] != result.DeploymentID {
				return ErrInvalidArgument
			}
		}
	}
	return nil
}

func (s *PgStore) LatestProjectEnvironmentQualification(ctx context.Context, accountID, projectID, environment, releaseSetID string) (ProjectEnvironmentQualification, error) {
	if err := validateProjectEnvironmentQualificationScope(accountID, projectID, environment, releaseSetID); err != nil {
		return ProjectEnvironmentQualification{}, err
	}
	return scanProjectEnvironmentQualification(s.pool.QueryRow(ctx, `
		select q.id, q.account_id, q.project_id, q.environment_slug, q.release_set_id,
		       q.configuration_version, q.configuration_hash, q.secret_revision_hashes, q.workload_config_hashes, q.status, q.checks, q.created_at, q.expires_at
		from project_environment_qualifications q
		join projects p on p.id = q.project_id
		where q.account_id = $1 and q.project_id = $2 and q.environment_slug = $3
		  and q.release_set_id = $4 and p.account_id = $1
		order by q.created_at desc, q.id desc limit 1`, accountID, projectID, environment, releaseSetID))
}
