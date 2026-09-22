// Package nodejoin owns the durable coordination state for provider-neutral
// compute-node adoption. It intentionally does not know how a provider made
// the machine or how Ansible connects to it.
package nodejoin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Phase string

const (
	PhasePlanned    Phase = "planned"
	PhasePreflight  Phase = "preflight"
	PhaseConverging Phase = "converging"
	PhasePrepared   Phase = "prepared"
	PhaseVerifying  Phase = "verifying"
	PhaseActive     Phase = "active"
	PhaseFailed     Phase = "failed"
	PhaseRolledBack Phase = "rolled_back"
)

var (
	ErrNotFound       = errors.New("nodejoin: job not found")
	ErrLeaseHeld      = errors.New("nodejoin: join lease is held by another worker")
	ErrExisting       = errors.New("nodejoin: a different join request already exists")
	ErrResumeRequired = errors.New("nodejoin: join previously failed; rerun with --resume")
	ErrLeaseExpired   = errors.New("nodejoin: join lease is missing or expired")
	ErrInvalidPhase   = errors.New("nodejoin: invalid phase")
)

type Spec struct {
	NodeName      string
	DatabaseNode  string
	SSHHost       string
	ManifestHash  string
	ReleaseGitSHA string
}

type Job struct {
	ID             string
	NodeName       string
	DatabaseNode   string
	SSHHost        string
	ManifestHash   string
	ReleaseGitSHA  string
	Phase          Phase
	Attempt        int
	LastError      string
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

type Store interface {
	CreateOrResume(ctx context.Context, spec Spec, resume bool) (Job, error)
	AcquireLease(ctx context.Context, nodeName, owner string, ttl time.Duration) (Job, error)
	RefreshLease(ctx context.Context, nodeName, owner string, ttl time.Duration) error
	UpdatePhase(ctx context.Context, nodeName, owner string, phase Phase, lastError string) error
	MarkFailed(ctx context.Context, nodeName, owner string, cause error) error
	MarkComplete(ctx context.Context, nodeName, owner string) error
	MarkRolledBack(ctx context.Context, nodeName, owner string, cause error) error
	ReleaseLease(ctx context.Context, nodeName, owner string) error
	Get(ctx context.Context, nodeName string) (Job, error)
}

type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) CreateOrResume(ctx context.Context, spec Spec, resume bool) (Job, error) {
	if err := validateSpec(spec); err != nil {
		return Job{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, fmt.Errorf("nodejoin: begin job transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	job, err := scanJob(tx.QueryRow(ctx, `
		select id, node_name, database_node, ssh_host, manifest_hash,
		       release_git_sha, phase, attempt, coalesce(last_error, ''),
		       coalesce(lease_owner, ''), lease_expires_at, created_at,
		       updated_at, completed_at
		from node_join_jobs where node_name = $1 for update`, spec.NodeName))
	if errors.Is(err, pgx.ErrNoRows) {
		job, err = scanJob(tx.QueryRow(ctx, `
			insert into node_join_jobs
			  (node_name, database_node, ssh_host, manifest_hash, release_git_sha)
			values ($1, $2, $3, $4, $5)
			returning id, node_name, database_node, ssh_host, manifest_hash,
			          release_git_sha, phase, attempt, coalesce(last_error, ''),
			          coalesce(lease_owner, ''), lease_expires_at, created_at,
			          updated_at, completed_at`,
			spec.NodeName, spec.DatabaseNode, spec.SSHHost, spec.ManifestHash, spec.ReleaseGitSHA))
	} else if err == nil {
		if job.LeaseOwner != "" && job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(time.Now()) {
			if !sameSpec(job, spec) || resume {
				return Job{}, ErrLeaseHeld
			}
		}
		if !sameSpec(job, spec) {
			if !resume {
				return Job{}, ErrExisting
			}
			job, err = scanJob(tx.QueryRow(ctx, `
				update node_join_jobs
				set database_node = $2, ssh_host = $3, manifest_hash = $4,
				    release_git_sha = $5, phase = 'planned', attempt = 0,
				    last_error = null, lease_owner = null,
				    lease_expires_at = null, completed_at = null, updated_at = now()
				where node_name = $1
				returning id, node_name, database_node, ssh_host, manifest_hash,
				          release_git_sha, phase, attempt, coalesce(last_error, ''),
				          coalesce(lease_owner, ''), lease_expires_at, created_at,
				          updated_at, completed_at`,
				spec.NodeName, spec.DatabaseNode, spec.SSHHost, spec.ManifestHash, spec.ReleaseGitSHA))
		} else if job.Phase == PhaseFailed || job.Phase == PhaseRolledBack {
			if !resume {
				return Job{}, ErrResumeRequired
			}
			job, err = scanJob(tx.QueryRow(ctx, `
				update node_join_jobs
				set phase = 'planned', last_error = null, lease_owner = null,
				    lease_expires_at = null, completed_at = null, updated_at = now()
				where node_name = $1
				returning id, node_name, database_node, ssh_host, manifest_hash,
				          release_git_sha, phase, attempt, coalesce(last_error, ''),
				          coalesce(lease_owner, ''), lease_expires_at, created_at,
				          updated_at, completed_at`, spec.NodeName))
		}
	}
	if err != nil {
		return Job{}, fmt.Errorf("nodejoin: create/resume job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("nodejoin: commit job: %w", err)
	}
	return job, nil
}

func (s *PGStore) AcquireLease(ctx context.Context, nodeName, owner string, ttl time.Duration) (Job, error) {
	if ttl <= 0 || owner == "" || nodeName == "" {
		return Job{}, fmt.Errorf("nodejoin: lease owner, node name, and positive TTL are required")
	}
	job, err := scanJob(s.pool.QueryRow(ctx, `
		update node_join_jobs
		set lease_owner = $2, lease_expires_at = now() + ($3 * interval '1 millisecond'),
		    attempt = attempt + 1, updated_at = now()
	where node_name = $1
	  and (lease_owner is null or lease_expires_at <= now() or lease_owner = $2)
	returning id, node_name, database_node, ssh_host, manifest_hash,
	          release_git_sha, phase, attempt, coalesce(last_error, ''),
	          coalesce(lease_owner, ''), lease_expires_at, created_at,
		updated_at, completed_at`, nodeName, owner, ttl.Milliseconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := s.Get(ctx, nodeName); errors.Is(getErr, ErrNotFound) {
			return Job{}, ErrNotFound
		}
		return Job{}, ErrLeaseHeld
	}
	if err != nil {
		return Job{}, fmt.Errorf("nodejoin: acquire lease: %w", err)
	}
	return job, nil
}

func (s *PGStore) RefreshLease(ctx context.Context, nodeName, owner string, ttl time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		update node_join_jobs
		set lease_expires_at = now() + ($3 * interval '1 millisecond'), updated_at = now()
		where node_name = $1 and lease_owner = $2 and lease_expires_at > now()`,
		nodeName, owner, ttl.Milliseconds())
	if err != nil {
		return fmt.Errorf("nodejoin: refresh lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseExpired
	}
	return nil
}

func (s *PGStore) UpdatePhase(ctx context.Context, nodeName, owner string, phase Phase, lastError string) error {
	if !validPhase(phase) {
		return ErrInvalidPhase
	}
	tag, err := s.pool.Exec(ctx, `
		update node_join_jobs
		set phase = $3, last_error = nullif($4, ''), updated_at = now()
		where node_name = $1 and lease_owner = $2 and lease_expires_at > now()`,
		nodeName, owner, phase, lastError)
	if err != nil {
		return fmt.Errorf("nodejoin: update phase: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseExpired
	}
	return nil
}

func (s *PGStore) MarkFailed(ctx context.Context, nodeName, owner string, cause error) error {
	return s.finish(ctx, nodeName, owner, PhaseFailed, errorString(cause), false)
}

func (s *PGStore) MarkComplete(ctx context.Context, nodeName, owner string) error {
	return s.finish(ctx, nodeName, owner, PhaseActive, "", true)
}

func (s *PGStore) MarkRolledBack(ctx context.Context, nodeName, owner string, cause error) error {
	return s.finish(ctx, nodeName, owner, PhaseRolledBack, errorString(cause), false)
}

func (s *PGStore) finish(ctx context.Context, nodeName, owner string, phase Phase, message string, complete bool) error {
	completed := "null"
	if complete {
		completed = "now()"
	}
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`
		update node_join_jobs
		set phase = $3, last_error = nullif($4, ''), lease_owner = null,
		    lease_expires_at = null, completed_at = %s, updated_at = now()
		where node_name = $1 and lease_owner = $2 and lease_expires_at > now()`, completed),
		nodeName, owner, phase, message)
	if err != nil {
		return fmt.Errorf("nodejoin: finish job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseExpired
	}
	return nil
}

func (s *PGStore) ReleaseLease(ctx context.Context, nodeName, owner string) error {
	_, err := s.pool.Exec(ctx, `update node_join_jobs set lease_owner = null, lease_expires_at = null, updated_at = now() where node_name = $1 and lease_owner = $2`, nodeName, owner)
	return err
}

func (s *PGStore) Get(ctx context.Context, nodeName string) (Job, error) {
	job, err := scanJob(s.pool.QueryRow(ctx, `
		select id, node_name, database_node, ssh_host, manifest_hash,
		       release_git_sha, phase, attempt, coalesce(last_error, ''),
		       coalesce(lease_owner, ''), lease_expires_at, created_at,
		       updated_at, completed_at
		from node_join_jobs where node_name = $1`, nodeName))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("nodejoin: get job: %w", err)
	}
	return job, nil
}

type rowScanner interface{ Scan(...any) error }

func scanJob(row rowScanner) (Job, error) {
	var j Job
	var phase string
	if err := row.Scan(&j.ID, &j.NodeName, &j.DatabaseNode, &j.SSHHost, &j.ManifestHash,
		&j.ReleaseGitSHA, &phase, &j.Attempt, &j.LastError, &j.LeaseOwner,
		&j.LeaseExpiresAt, &j.CreatedAt, &j.UpdatedAt, &j.CompletedAt); err != nil {
		return Job{}, err
	}
	j.Phase = Phase(phase)
	return j, nil
}

func validateSpec(s Spec) error {
	if s.NodeName == "" || s.DatabaseNode == "" || s.SSHHost == "" || s.ManifestHash == "" || s.ReleaseGitSHA == "" {
		return fmt.Errorf("nodejoin: node, database node, SSH host, manifest hash, and release SHA are required")
	}
	return nil
}

func sameSpec(j Job, s Spec) bool {
	return j.NodeName == s.NodeName && j.DatabaseNode == s.DatabaseNode && j.SSHHost == s.SSHHost && j.ManifestHash == s.ManifestHash && j.ReleaseGitSHA == s.ReleaseGitSHA
}

func validPhase(p Phase) bool {
	switch p {
	case PhasePlanned, PhasePreflight, PhaseConverging, PhasePrepared, PhaseVerifying, PhaseActive, PhaseFailed, PhaseRolledBack:
		return true
	}
	return false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// MemStore is a concurrency-safe in-memory implementation used by CLI
// lifecycle tests. It follows the same lease rules as PGStore, including
// rejecting a second owner until the lease expires.
type MemStore struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func NewMemStore() *MemStore { return &MemStore{jobs: make(map[string]Job)} }

func (s *MemStore) CreateOrResume(_ context.Context, spec Spec, resume bool) (Job, error) {
	if err := validateSpec(spec); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if old, ok := s.jobs[spec.NodeName]; ok {
		desiredSame := sameSpec(old, spec)
		if old.LeaseOwner != "" && old.LeaseExpiresAt != nil && old.LeaseExpiresAt.After(time.Now()) && (!desiredSame || resume) {
			return Job{}, ErrLeaseHeld
		}
		if !desiredSame && !resume {
			return Job{}, ErrExisting
		}
		if desiredSame && (old.Phase == PhaseFailed || old.Phase == PhaseRolledBack) && !resume {
			return Job{}, ErrResumeRequired
		}
		if !desiredSame || (resume && (old.Phase == PhaseFailed || old.Phase == PhaseRolledBack)) {
			old.DatabaseNode, old.SSHHost, old.ManifestHash, old.ReleaseGitSHA = spec.DatabaseNode, spec.SSHHost, spec.ManifestHash, spec.ReleaseGitSHA
			old.Phase, old.LastError, old.LeaseOwner, old.LeaseExpiresAt, old.CompletedAt = PhasePlanned, "", "", nil, nil
			if !desiredSame {
				old.Attempt = 0
			}
			old.UpdatedAt = now
			s.jobs[spec.NodeName] = old
		}
		return old, nil
	}
	j := Job{ID: uuid.NewString(), NodeName: spec.NodeName, DatabaseNode: spec.DatabaseNode, SSHHost: spec.SSHHost, ManifestHash: spec.ManifestHash, ReleaseGitSHA: spec.ReleaseGitSHA, Phase: PhasePlanned, CreatedAt: now, UpdatedAt: now}
	s.jobs[spec.NodeName] = j
	return j, nil
}

func (s *MemStore) AcquireLease(_ context.Context, nodeName, owner string, ttl time.Duration) (Job, error) {
	if ttl <= 0 || owner == "" || nodeName == "" {
		return Job{}, fmt.Errorf("nodejoin: lease owner, node name, and positive TTL are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return Job{}, ErrNotFound
	}
	now := time.Now()
	if j.LeaseOwner != "" && j.LeaseOwner != owner && (j.LeaseExpiresAt == nil || j.LeaseExpiresAt.After(now)) {
		return Job{}, ErrLeaseHeld
	}
	expires := now.Add(ttl)
	j.LeaseOwner, j.LeaseExpiresAt, j.Attempt, j.UpdatedAt = owner, &expires, j.Attempt+1, now
	s.jobs[nodeName] = j
	return j, nil
}

func (s *MemStore) RefreshLease(_ context.Context, nodeName, owner string, ttl time.Duration) error {
	if ttl <= 0 || owner == "" || nodeName == "" {
		return fmt.Errorf("nodejoin: lease owner, node name, and positive TTL are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return ErrNotFound
	}
	if j.LeaseOwner != owner || j.LeaseExpiresAt == nil || !j.LeaseExpiresAt.After(time.Now()) {
		return ErrLeaseExpired
	}
	e := time.Now().Add(ttl)
	j.LeaseExpiresAt, j.UpdatedAt = &e, time.Now()
	s.jobs[nodeName] = j
	return nil
}

func (s *MemStore) UpdatePhase(_ context.Context, nodeName, owner string, phase Phase, lastError string) error {
	if !validPhase(phase) {
		return ErrInvalidPhase
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return ErrNotFound
	}
	if j.LeaseOwner != owner || j.LeaseExpiresAt == nil || !j.LeaseExpiresAt.After(time.Now()) {
		return ErrLeaseExpired
	}
	j.Phase, j.LastError, j.UpdatedAt = phase, lastError, time.Now()
	s.jobs[nodeName] = j
	return nil
}

func (s *MemStore) MarkFailed(ctx context.Context, nodeName, owner string, cause error) error {
	return s.finish(ctx, nodeName, owner, PhaseFailed, errorString(cause), false)
}
func (s *MemStore) MarkComplete(ctx context.Context, nodeName, owner string) error {
	return s.finish(ctx, nodeName, owner, PhaseActive, "", true)
}
func (s *MemStore) MarkRolledBack(ctx context.Context, nodeName, owner string, cause error) error {
	return s.finish(ctx, nodeName, owner, PhaseRolledBack, errorString(cause), false)
}
func (s *MemStore) finish(_ context.Context, nodeName, owner string, phase Phase, message string, complete bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return ErrNotFound
	}
	if j.LeaseOwner != owner || j.LeaseExpiresAt == nil || !j.LeaseExpiresAt.After(time.Now()) {
		return ErrLeaseExpired
	}
	now := time.Now()
	j.Phase, j.LastError, j.LeaseOwner, j.LeaseExpiresAt, j.UpdatedAt = phase, message, "", nil, now
	if complete {
		j.CompletedAt = &now
	}
	s.jobs[nodeName] = j
	return nil
}
func (s *MemStore) ReleaseLease(_ context.Context, nodeName, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return ErrNotFound
	}
	if j.LeaseOwner == owner {
		j.LeaseOwner, j.LeaseExpiresAt, j.UpdatedAt = "", nil, time.Now()
		s.jobs[nodeName] = j
	}
	return nil
}
func (s *MemStore) Get(_ context.Context, nodeName string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[nodeName]
	if !ok {
		return Job{}, ErrNotFound
	}
	return j, nil
}
