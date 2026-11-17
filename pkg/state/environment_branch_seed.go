package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Rehearsal twins of a simulated environment branch (ADR-20261031193635
// slice 6b). A rehearsal member, a dev-session app of the run, is seeded from
// one workload of a simulated branch: its deployment reuses the branch
// deployment's built artifact and it gets the branch's non-managed variables
// and secrets. Managed bindings are not copied; the recipe declares isolated
// fixtures. Rehearsal policy stays keyed by the twin's own app.

// EnvironmentBranchSeedReason marks a rehearsal member's deployment seeded
// from a branch. Unlike a branch deployment it has no lineage row, so schedd
// primes it with an ordinary boot.
const EnvironmentBranchSeedReason = "environment-branch-seed"

// ErrProjectEnvironmentBranchNotSimulated refuses seeding from a branch whose
// egress is not simulated.
var ErrProjectEnvironmentBranchNotSimulated = fmt.Errorf("%w: only a simulated environment branch can seed a rehearsal", ErrConflict)

// EnvironmentBranchSeed names the branch workload a member app is seeded from.
type EnvironmentBranchSeed struct {
	AccountID   string
	ProjectID   string
	Branch      string // the branch environment's slug
	Workload    string // the project workload's name
	MemberAppID string // the run's dev-session app
}

// EnvironmentBranchSeedStore seeds rehearsal members from simulated branches.
type EnvironmentBranchSeedStore interface {
	// SeedScenarioMemberFromEnvironmentBranch creates the member's deployment
	// in snapshotting, or returns the one already seeded. ErrNotFound means
	// the branch, its live workload or the member app is missing.
	SeedScenarioMemberFromEnvironmentBranch(ctx context.Context, seed EnvironmentBranchSeed) (Deployment, error)
}

func (s EnvironmentBranchSeed) valid() bool {
	return s.AccountID != "" && s.ProjectID != "" && s.Branch != "" && s.Workload != "" && s.MemberAppID != ""
}

func seedDeploymentFromArtifact(a projectCloneArtifact, input *projectEnvironmentBranchDeploymentInput) Deployment {
	d := branchDeploymentFromArtifact(a, DefaultEnvScope, input)
	d.AppID = input.Seed.MemberAppID
	d.Reason = EnvironmentBranchSeedReason
	return d
}

// MemStore

func (m *MemStore) SeedScenarioMemberFromEnvironmentBranch(ctx context.Context, seed EnvironmentBranchSeed) (Deployment, error) {
	if !seed.valid() {
		return Deployment{}, ErrInvalidArgument
	}
	input := &projectEnvironmentBranchDeploymentInput{AccountID: seed.AccountID, ProjectID: seed.ProjectID, Slug: seed.Branch, Seed: &seed}
	created, _, err := m.createDeployment(ctx, Deployment{AppID: seed.MemberAppID, Scope: DefaultEnvScope, branchInput: input}, nil, nil)
	return created, err
}

func (m *MemStore) prepareSeedDeploymentLocked(d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	seed := input.Seed
	branch, err := m.projectEnvironmentBranchBySlugLocked(input.AccountID, input.ProjectID, input.Slug)
	if err != nil {
		return Deployment{}, nil, "", err
	}
	if branch.EgressMode != EnvironmentBranchEgressSimulated {
		return Deployment{}, nil, "", ErrProjectEnvironmentBranchNotSimulated
	}
	member, ok := m.apps[seed.MemberAppID]
	if !ok || member.AccountID != input.AccountID || member.Status != AppActive || member.PreviewOfSlug == "" || member.PreviewPrNumber != 0 {
		return Deployment{}, nil, "", ErrNotFound
	}
	var existing *Deployment
	for _, dep := range m.deployments {
		if dep.AppID == member.ID && dep.Reason == EnvironmentBranchSeedReason && dep.Status != DeployFailed && dep.Status != DeployCancelled &&
			(existing == nil || dep.CreatedAt.After(existing.CreatedAt)) {
			dep := dep
			existing = &dep
		}
	}
	if existing != nil {
		return Deployment{}, existing, "", nil
	}
	var source Deployment
	for _, dep := range m.deployments {
		app, ok := m.apps[dep.AppID]
		if !ok || app.AccountID != input.AccountID || app.ProjectID != input.ProjectID || app.PreviewOfSlug != "" || app.Status == AppDeleted ||
			app.WorkloadName != seed.Workload || normalizedDeploymentScope(dep.Scope) != branch.Slug || dep.Status != DeployLive || dep.TrafficPercent <= 0 {
			continue
		}
		if app.Type != member.Type {
			return Deployment{}, nil, "", ErrConflict
		}
		if source.ID == "" || dep.CreatedAt.After(source.CreatedAt) {
			source = dep
		}
	}
	if source.ID == "" || source.RootfsKey == "" {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(source)); err != nil {
		return Deployment{}, nil, "", err
	}
	return seedDeploymentFromArtifact(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(source)), input), nil, source.ID, nil
}

// attachSeedDeploymentLocked copies the branch deployment's sidecar layers and
// the branch's non-managed values onto the member. Values the member already
// has win: the recipe may set its own.
func (m *MemStore) attachSeedDeploymentLocked(input projectEnvironmentBranchDeploymentInput, sourceID string, created Deployment) {
	m.copyBranchDeploymentLayersLocked(sourceID, created.ID)
	source := m.deployments[sourceID]
	now := time.Now().UTC()
	for _, env := range m.envs {
		if env.AppID != source.AppID || env.Scope != input.Slug {
			continue
		}
		key := envKey{AppID: created.AppID, Scope: DefaultEnvScope, Key: env.Key}
		if _, ok := m.envs[key]; ok {
			continue
		}
		env.AppID, env.Scope, env.CreatedAt, env.UpdatedAt = created.AppID, DefaultEnvScope, now, now
		m.envs[key] = env
	}
	for _, secret := range m.secrets {
		if secret.AppID != source.AppID || secret.Scope != input.Slug || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
			continue
		}
		key := secretKey{AppID: created.AppID, Scope: DefaultEnvScope, Key: secret.Key}
		if _, ok := m.secrets[key]; ok {
			continue
		}
		secret.AppID, secret.Scope, secret.CreatedAt, secret.UpdatedAt = created.AppID, DefaultEnvScope, now, now
		secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
		secret.DeliveryVersion, secret.DeliveredVersion, secret.DeliveryStatus = 1, 0, SecretDeliveryPending
		secret.LastDeliveryAttemptAt, secret.LastDeliveredAt = nil, nil
		secret.LastDeliveryErrorCode, secret.LastDeliveredWakeID, secret.LastDeliveredInstanceID = "", "", ""
		m.secrets[key] = secret
	}
}

// PgStore

func (s *PgStore) SeedScenarioMemberFromEnvironmentBranch(ctx context.Context, seed EnvironmentBranchSeed) (Deployment, error) {
	if !seed.valid() {
		return Deployment{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(seed.MemberAppID); err != nil {
		return Deployment{}, ErrNotFound
	}
	input := &projectEnvironmentBranchDeploymentInput{AccountID: seed.AccountID, ProjectID: seed.ProjectID, Slug: seed.Branch, Seed: &seed}
	created, _, err := s.createDeployment(ctx, Deployment{AppID: seed.MemberAppID, Scope: DefaultEnvScope, branchInput: input}, nil, nil)
	return created, err
}

func prepareSeedDeploymentTx(ctx context.Context, tx pgx.Tx, d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	seed := input.Seed
	var mode string
	err := tx.QueryRow(ctx, `
		select b.egress_mode
		  from project_environment_branches b
		  join project_environments e on e.id = b.environment_id
		 where e.account_id = $1 and e.project_id = $2 and e.slug = $3
		 for share of b
	`, input.AccountID, input.ProjectID, input.Slug).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrProjectEnvironmentNotBranch
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if mode != EnvironmentBranchEgressSimulated {
		return Deployment{}, nil, "", ErrProjectEnvironmentBranchNotSimulated
	}
	var memberType string
	err = tx.QueryRow(ctx, `
		select type from apps
		 where id = $1 and account_id = $2 and status = 'active'
		   and preview_of_slug is not null and coalesce(preview_pr_number, 0) = 0
	`, seed.MemberAppID, input.AccountID).Scan(&memberType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	var existingID string
	err = tx.QueryRow(ctx, `
		select id::text from deployments
		 where app_id = $1 and reason = $2 and status not in ('failed', 'cancelled')
		 order by created_at desc limit 1
	`, seed.MemberAppID, EnvironmentBranchSeedReason).Scan(&existingID)
	if err == nil {
		existing, err := deploymentByIDDB(ctx, tx, existingID)
		if err != nil {
			return Deployment{}, nil, "", err
		}
		return Deployment{}, &existing, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", mapErr(err)
	}
	var sourceID, sourceType string
	err = tx.QueryRow(ctx, `
		select d.id::text, a.type
		  from deployments d
		  join apps a on a.id = d.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.workload_name = $3
		   and a.preview_of_slug is null and a.status <> 'deleted'
		   and d.scope = $4 and d.status = 'live' and d.traffic_percent > 0
		 order by d.created_at desc, d.id desc
		 limit 1
	`, input.AccountID, input.ProjectID, seed.Workload, input.Slug).Scan(&sourceID, &sourceType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrNotFound
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if sourceType != memberType {
		return Deployment{}, nil, "", ErrConflict
	}
	source, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, `select `+deploymentSelectColumnsWithRootfs+` from deployments where id = $1`, sourceID))
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if source.RootfsKey == "" || source.RootfsBytes <= 0 {
		return Deployment{}, nil, "", ErrNotFound
	}
	// Serialize with layer GC, as a branch deployment does.
	if err := requireDeploymentLayerArtifactsTx(ctx, tx, source.ID); err != nil {
		return Deployment{}, nil, "", err
	}
	return seedDeploymentFromArtifact(normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(source)), input), nil, source.ID, nil
}

// attachSeedDeploymentTx copies the artifact, sidecar layers and the branch's
// non-managed values, then moves the member's deployment to snapshotting.
// There is no lineage row: schedd's prime boots it.
func attachSeedDeploymentTx(ctx context.Context, tx pgx.Tx, input projectEnvironmentBranchDeploymentInput, sourceID string, d Deployment, created *Deployment) error {
	if err := copyBranchDeploymentArtifactTx(ctx, tx, sourceID, d, created); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into app_envs (account_id, app_id, scope, key, value)
		select e.account_id, $1, 'default', e.key, e.value
		  from app_envs e join deployments d on d.app_id = e.app_id
		 where d.id = $2 and e.scope = $3
		on conflict (app_id, scope, key) do nothing
	`, created.AppID, sourceID, input.Slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash, secret_version, secret_class)
		select s.account_id, $1, 'default', s.key, s.ciphertext, s.kid, s.value_hash, s.secret_version, s.secret_class
		  from app_secrets s join deployments d on d.app_id = s.app_id
		 where d.id = $2 and s.scope = $3
		   and s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null
		on conflict (app_id, scope, key) do nothing
	`, created.AppID, sourceID, input.Slug); err != nil {
		return mapErr(err)
	}
	return stageBranchDeploymentTx(ctx, tx, created)
}
