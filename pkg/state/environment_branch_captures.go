package state

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Credential-free captures (ADR-20261031193635 slice 5a). An environment
// branch restores only from an init-tier capture of its parent deployment
// whose boot received placeholder managed secrets, so the restored memory
// holds no parent credential. Captures live in their own table: no production
// snapshot lookup can select one.

const (
	EnvironmentBranchCaptureRequested = "requested"
	EnvironmentBranchCaptureReady     = "ready"
	EnvironmentBranchCaptureFailed    = "failed"

	// EnvironmentBranchCaptureEnvironment is the workload environment the
	// capture boot runs under: never production, so tokens it holds are
	// refused by production-only integrations.
	EnvironmentBranchCaptureEnvironment = "branch-capture"
	// CredentialFreeHost is the host of every placeholder URL.
	CredentialFreeHost = "credential-free.invalid"
	// CredentialFreePlaceholder replaces a managed secret that is not a URL.
	CredentialFreePlaceholder = "credential-free-placeholder"
	// environmentBranchCaptureFailureMax bounds the stored failure text.
	environmentBranchCaptureFailureMax = 512
)

// EnvironmentBranchCapture is one parent deployment's credential-free capture.
type EnvironmentBranchCapture struct {
	ID           string
	DeploymentID string
	State        string
	Attempts     int
	LeaseToken   string
	// InstanceID is the capture VM of the latest attempt; the next claim
	// retires it if schedd stopped before it did.
	InstanceID string
	Failure    string
	EnvironmentBranchCaptureArtifact
	RequestedAt time.Time
	ReadyAt     time.Time
	leaseUntil  time.Time // MemStore bookkeeping
}

// EnvironmentBranchCaptureArtifact is what a published capture restores from.
type EnvironmentBranchCaptureArtifact struct {
	FCVersion        string
	StorageKey       string
	BaseImageVersion string
	CPUTemplate      string
	CPUModel         string
	MemBytes         int64
	DiskBytes        int64
	StoredBytes      int64
}

// EnvironmentBranchCaptureStore queues, claims and publishes captures.
type EnvironmentBranchCaptureStore interface {
	// RequestEnvironmentBranchCapture queues the deployment's capture, or
	// returns its row in whatever state it is: a failed capture stays failed,
	// and its branches keep cold-booting.
	RequestEnvironmentBranchCapture(ctx context.Context, deploymentID string) (EnvironmentBranchCapture, error)
	// EnvironmentBranchCapture returns the deployment's capture, ErrNotFound
	// when none was requested.
	EnvironmentBranchCapture(ctx context.Context, deploymentID string) (EnvironmentBranchCapture, error)
	// ClaimEnvironmentBranchCapture leases the oldest requested capture whose
	// lease is free, counting the attempt; ErrNotFound when none is due.
	ClaimEnvironmentBranchCapture(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (EnvironmentBranchCapture, error)
	// PublishEnvironmentBranchCapture makes a leased capture ready.
	PublishEnvironmentBranchCapture(ctx context.Context, id, leaseToken string, artifact EnvironmentBranchCaptureArtifact) error
	// FailEnvironmentBranchCapture releases a leased capture for a retry, or
	// fails it for good when permanent.
	FailEnvironmentBranchCapture(ctx context.Context, id, leaseToken, failure string, permanent bool) error
	// RecordEnvironmentBranchCaptureInstance notes the leased attempt's
	// capture VM before it boots.
	RecordEnvironmentBranchCaptureInstance(ctx context.Context, id, leaseToken, instanceID string) error
	// RetireEnvironmentBranchCapture fails a ready capture for good: a restore
	// from it fell back to cold boot. Its branches cold-boot from then on.
	RetireEnvironmentBranchCapture(ctx context.Context, id, failure string) error
	// EnvironmentBranchCaptureDeploymentIDs lists deployments whose capture is
	// requested or ready, so imaged's orphan sweep keeps their directories.
	EnvironmentBranchCaptureDeploymentIDs(ctx context.Context) ([]string, error)
	// EnvironmentBranchLineageParent returns the deployment a branch
	// deployment was created from: the only one whose capture it restores.
	// ErrNotFound when no branch created the deployment.
	EnvironmentBranchLineageParent(ctx context.Context, deploymentID string) (string, error)
}

// CredentialFreeValue is the placeholder that replaces a managed binding
// secret in a capture boot. A URL keeps its scheme, so clients still parse
// it, but points at a host that cannot resolve and carries no credential.
func CredentialFreeValue(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		placeholder := url.URL{Scheme: parsed.Scheme, Host: CredentialFreeHost, Path: "/placeholder"}
		if parsed.User != nil {
			placeholder.User = url.UserPassword("placeholder", "placeholder")
		}
		return placeholder.String()
	}
	return CredentialFreePlaceholder
}

func validEnvironmentBranchCaptureArtifact(a EnvironmentBranchCaptureArtifact) bool {
	return a.StorageKey != "" && a.FCVersion != "" && a.MemBytes >= 0 && a.DiskBytes >= 0 && a.StoredBytes >= 0
}

func clipEnvironmentBranchCaptureFailure(failure string) string {
	if len(failure) > environmentBranchCaptureFailureMax {
		return failure[:environmentBranchCaptureFailureMax]
	}
	return failure
}

// MemStore

func (m *MemStore) RequestEnvironmentBranchCapture(_ context.Context, deploymentID string) (EnvironmentBranchCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	if existing, ok := m.environmentBranchCaptures[deploymentID]; ok {
		return existing, nil
	}
	capture := EnvironmentBranchCapture{ID: uuid.NewString(), DeploymentID: deploymentID, State: EnvironmentBranchCaptureRequested, RequestedAt: m.clock().UTC()}
	m.environmentBranchCaptures[deploymentID] = capture
	return capture, nil
}

func (m *MemStore) EnvironmentBranchCapture(_ context.Context, deploymentID string) (EnvironmentBranchCapture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, ok := m.environmentBranchCaptures[deploymentID]
	if !ok {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	return capture, nil
}

func (m *MemStore) ClaimEnvironmentBranchCapture(_ context.Context, leaseToken string, now time.Time, lease time.Duration) (EnvironmentBranchCapture, error) {
	if leaseToken == "" || lease <= 0 {
		return EnvironmentBranchCapture{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []EnvironmentBranchCapture
	for _, capture := range m.environmentBranchCaptures {
		if capture.State == EnvironmentBranchCaptureRequested && !capture.leaseUntil.After(now) {
			due = append(due, capture)
		}
	}
	if len(due) == 0 {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].RequestedAt.Equal(due[j].RequestedAt) {
			return due[i].RequestedAt.Before(due[j].RequestedAt)
		}
		return due[i].ID < due[j].ID
	})
	capture := due[0]
	capture.LeaseToken, capture.leaseUntil = leaseToken, now.Add(lease)
	capture.Attempts++
	m.environmentBranchCaptures[capture.DeploymentID] = capture
	return capture, nil
}

func (m *MemStore) leasedEnvironmentBranchCaptureLocked(id, leaseToken string) (EnvironmentBranchCapture, error) {
	for _, capture := range m.environmentBranchCaptures {
		if capture.ID != id {
			continue
		}
		if capture.State != EnvironmentBranchCaptureRequested || capture.LeaseToken == "" || capture.LeaseToken != leaseToken {
			return EnvironmentBranchCapture{}, ErrConflict
		}
		return capture, nil
	}
	return EnvironmentBranchCapture{}, ErrNotFound
}

func (m *MemStore) PublishEnvironmentBranchCapture(_ context.Context, id, leaseToken string, artifact EnvironmentBranchCaptureArtifact) error {
	if !validEnvironmentBranchCaptureArtifact(artifact) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, err := m.leasedEnvironmentBranchCaptureLocked(id, leaseToken)
	if err != nil {
		return err
	}
	capture.State, capture.LeaseToken, capture.leaseUntil = EnvironmentBranchCaptureReady, "", time.Time{}
	capture.EnvironmentBranchCaptureArtifact, capture.ReadyAt, capture.Failure = artifact, m.clock().UTC(), ""
	m.environmentBranchCaptures[capture.DeploymentID] = capture
	return nil
}

func (m *MemStore) FailEnvironmentBranchCapture(_ context.Context, id, leaseToken, failure string, permanent bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, err := m.leasedEnvironmentBranchCaptureLocked(id, leaseToken)
	if err != nil {
		return err
	}
	capture.LeaseToken, capture.leaseUntil, capture.Failure = "", time.Time{}, clipEnvironmentBranchCaptureFailure(failure)
	if permanent {
		capture.State = EnvironmentBranchCaptureFailed
	}
	m.environmentBranchCaptures[capture.DeploymentID] = capture
	return nil
}

func (m *MemStore) RecordEnvironmentBranchCaptureInstance(_ context.Context, id, leaseToken, instanceID string) error {
	if instanceID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, err := m.leasedEnvironmentBranchCaptureLocked(id, leaseToken)
	if err != nil {
		return err
	}
	capture.InstanceID = instanceID
	m.environmentBranchCaptures[capture.DeploymentID] = capture
	return nil
}

func (m *MemStore) RetireEnvironmentBranchCapture(_ context.Context, id, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for deploymentID, capture := range m.environmentBranchCaptures {
		if capture.ID != id {
			continue
		}
		if capture.State != EnvironmentBranchCaptureReady {
			return ErrConflict
		}
		capture.State, capture.Failure, capture.ReadyAt = EnvironmentBranchCaptureFailed, clipEnvironmentBranchCaptureFailure(failure), time.Time{}
		m.environmentBranchCaptures[deploymentID] = capture
		return nil
	}
	return ErrNotFound
}

func (m *MemStore) EnvironmentBranchCaptureDeploymentIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.environmentBranchCaptures))
	for deploymentID, capture := range m.environmentBranchCaptures {
		if capture.State != EnvironmentBranchCaptureFailed {
			out = append(out, deploymentID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemStore) EnvironmentBranchLineageParent(_ context.Context, deploymentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lineage, ok := m.projectEnvironmentBranchDeployments[deploymentID]
	if !ok {
		return "", ErrNotFound
	}
	return lineage.SourceDeploymentID, nil
}

// PgStore

func (s *PgStore) EnvironmentBranchLineageParent(ctx context.Context, deploymentID string) (string, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return "", ErrNotFound
	}
	var parent string
	err := s.pool.QueryRow(ctx, `select source_deployment_id::text from project_environment_branch_deployments where deployment_id = $1`, deploymentID).Scan(&parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return parent, mapErr(err)
}

const environmentBranchCaptureColumns = `id::text, deployment_id::text, state, attempts, coalesce(lease_token::text, ''), coalesce(instance_id::text, ''), failure,
	fc_version, storage_key, base_image_version, cpu_template, cpu_model, mem_bytes, disk_bytes, stored_bytes,
	requested_at, ready_at`

func scanEnvironmentBranchCapture(row pgx.Row) (EnvironmentBranchCapture, error) {
	var c EnvironmentBranchCapture
	var ready *time.Time
	err := row.Scan(&c.ID, &c.DeploymentID, &c.State, &c.Attempts, &c.LeaseToken, &c.InstanceID, &c.Failure,
		&c.FCVersion, &c.StorageKey, &c.BaseImageVersion, &c.CPUTemplate, &c.CPUModel, &c.MemBytes, &c.DiskBytes, &c.StoredBytes,
		&c.RequestedAt, &ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentBranchCapture{}, mapErr(err)
	}
	c.RequestedAt = c.RequestedAt.UTC()
	if ready != nil {
		c.ReadyAt = ready.UTC()
	}
	return c, nil
}

func (s *PgStore) RequestEnvironmentBranchCapture(ctx context.Context, deploymentID string) (EnvironmentBranchCapture, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	capture, err := scanEnvironmentBranchCapture(s.pool.QueryRow(ctx, `
		insert into environment_branch_captures (deployment_id)
		select d.id from deployments d where d.id = $1
		on conflict (deployment_id) do nothing
		returning `+environmentBranchCaptureColumns, deploymentID))
	if !errors.Is(err, ErrNotFound) {
		return capture, err
	}
	return s.EnvironmentBranchCapture(ctx, deploymentID)
}

func (s *PgStore) EnvironmentBranchCapture(ctx context.Context, deploymentID string) (EnvironmentBranchCapture, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return EnvironmentBranchCapture{}, ErrNotFound
	}
	return scanEnvironmentBranchCapture(s.pool.QueryRow(ctx, `select `+environmentBranchCaptureColumns+`
		from environment_branch_captures where deployment_id = $1`, deploymentID))
}

func (s *PgStore) ClaimEnvironmentBranchCapture(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (EnvironmentBranchCapture, error) {
	if _, err := uuid.Parse(leaseToken); err != nil || lease <= 0 {
		return EnvironmentBranchCapture{}, ErrInvalidArgument
	}
	return scanEnvironmentBranchCapture(s.pool.QueryRow(ctx, `
		with due as (
			select id from environment_branch_captures
			 where state = 'requested' and (lease_until is null or lease_until <= $1)
			 order by requested_at, id
			 limit 1
			 for update skip locked
		)
		update environment_branch_captures c
		   set lease_token = $2, lease_until = $3, attempts = c.attempts + 1
		  from due where c.id = due.id
		returning `+environmentBranchCaptureColumnsQualified,
		now.UTC(), leaseToken, now.UTC().Add(lease)))
}

const environmentBranchCaptureColumnsQualified = `c.id::text, c.deployment_id::text, c.state, c.attempts, coalesce(c.lease_token::text, ''), coalesce(c.instance_id::text, ''), c.failure,
	c.fc_version, c.storage_key, c.base_image_version, c.cpu_template, c.cpu_model, c.mem_bytes, c.disk_bytes, c.stored_bytes,
	c.requested_at, c.ready_at`

func (s *PgStore) PublishEnvironmentBranchCapture(ctx context.Context, id, leaseToken string, artifact EnvironmentBranchCaptureArtifact) error {
	if !validEnvironmentBranchCaptureArtifact(artifact) {
		return ErrInvalidArgument
	}
	return s.finishEnvironmentBranchCapture(ctx, id, leaseToken, `
		update environment_branch_captures
		   set state = 'ready', lease_token = null, lease_until = null, failure = '', ready_at = now(),
		       fc_version = $3, storage_key = $4, base_image_version = $5, cpu_template = $6, cpu_model = $7,
		       mem_bytes = $8, disk_bytes = $9, stored_bytes = $10
		 where id = $1 and lease_token = $2 and state = 'requested'`,
		artifact.FCVersion, artifact.StorageKey, artifact.BaseImageVersion, artifact.CPUTemplate, artifact.CPUModel,
		artifact.MemBytes, artifact.DiskBytes, artifact.StoredBytes)
}

func (s *PgStore) FailEnvironmentBranchCapture(ctx context.Context, id, leaseToken, failure string, permanent bool) error {
	return s.finishEnvironmentBranchCapture(ctx, id, leaseToken, `
		update environment_branch_captures
		   set state = case when $4 then 'failed' else 'requested' end,
		       lease_token = null, lease_until = null, failure = $3
		 where id = $1 and lease_token = $2 and state = 'requested'`,
		clipEnvironmentBranchCaptureFailure(failure), permanent)
}

func (s *PgStore) RecordEnvironmentBranchCaptureInstance(ctx context.Context, id, leaseToken, instanceID string) error {
	if _, err := uuid.Parse(instanceID); err != nil {
		return ErrInvalidArgument
	}
	return s.finishEnvironmentBranchCapture(ctx, id, leaseToken, `
		update environment_branch_captures set instance_id = $3
		 where id = $1 and lease_token = $2 and state = 'requested'`, instanceID)
}

func (s *PgStore) RetireEnvironmentBranchCapture(ctx context.Context, id, failure string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	// The ready CHECK pairs state ready with ready_at, so a retired row
	// clears it; the artifact columns stay as a record of what failed.
	tag, err := s.pool.Exec(ctx, `update environment_branch_captures set state = 'failed', failure = $2, ready_at = null
		where id = $1 and state = 'ready'`, id, clipEnvironmentBranchCaptureFailure(failure))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from environment_branch_captures where id = $1)`, id).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *PgStore) EnvironmentBranchCaptureDeploymentIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `select deployment_id::text from environment_branch_captures
		where state in ('requested', 'ready') order by deployment_id`)
	if err != nil {
		return nil, mapErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, mapErr(err)
}

func (s *PgStore) finishEnvironmentBranchCapture(ctx context.Context, id, leaseToken, query string, args ...any) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(leaseToken); err != nil {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, query, append([]any{id, leaseToken}, args...)...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from environment_branch_captures where id = $1)`, id).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}
