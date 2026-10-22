package state

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AppRuntimeGenerationReader reads an app's runtime generation (ADR-1741).
// It changes whenever anything a wake depends on changes: the app, its
// account, its deployments and releases, its runtime configuration, its
// standards enrollment and workload retirements. Callers compare it for
// equality only.
type AppRuntimeGenerationReader interface {
	AppRuntimeGeneration(ctx context.Context, appID string) (int64, error)
}

var (
	_ AppRuntimeGenerationReader = (*PgStore)(nil)
	_ AppRuntimeGenerationReader = (*MemStore)(nil)
)

// AppRuntimeGeneration reads the counter the migration's triggers bump in
// the writer's transaction.
func (s *PgStore) AppRuntimeGeneration(ctx context.Context, appID string) (int64, error) {
	id, err := uuid.Parse(appID)
	if err != nil {
		return 0, ErrInvalidArgument
	}
	generation, err := sqlc.New().AppRuntimeGeneration(ctx, s.pool, NewPgtypeUUID(id))
	if err != nil {
		return 0, mapErr(err)
	}
	return generation, nil
}

// AppRuntimeGeneration derives MemStore's generation from a digest of the
// same state the Postgres triggers cover, leaving out the same bookkeeping
// fields. It changes exactly when a covered input changes.
func (m *MemStore) AppRuntimeGeneration(_ context.Context, appID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appRuntimeGenerationLocked(appID)
}

func (m *MemStore) appRuntimeGenerationLocked(appID string) (int64, error) {
	app, ok := m.apps[appID]
	if !ok {
		return 0, ErrNotFound
	}
	h := fnv.New64a()
	write := func(v any) { _, _ = h.Write(runtimeGenerationDigest(v)) }
	write(app)
	acct := m.accounts[app.AccountID]
	write([]any{acct.Status, acct.Plan, acct.AbuseHoldAt})
	var deployments []Deployment
	for _, dep := range m.deployments {
		if dep.AppID == appID {
			deployments = append(deployments, dep)
		}
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].ID < deployments[j].ID })
	for _, dep := range deployments {
		write(dep)
		// Fields hidden from JSON are still deployments columns, which the
		// Postgres trigger compares as whole rows.
		write([]any{dep.RuntimeBase, dep.SecretReloadSignal, dep.SecretReloadSignalKnown,
			dep.EnvironmentWorkloadRuntime, dep.EnvironmentWorkloadHeldValue, dep.TrafficPercentExplicit,
			dep.MCPTaskMigrationOverride, dep.ExpectedReview, dep.ReviewedRolloutMode, dep.StagedReview,
			dep.ReviewedSettings})
		write(m.deploymentRuntimeEnvironmentOwners[dep.ID])
		// deployments.snapshot_recovery_generation in Postgres: an operator
		// cold boot (ADR-1523) must reach every cached wake input and ticket.
		write(m.snapshotRecoveryGenerations[dep.ID])
	}
	var keys []string
	for key, env := range m.envs {
		if env.AppID == appID {
			keys = append(keys, string(runtimeGenerationDigest(key))+string(runtimeGenerationDigest(env)))
		}
	}
	for key, secret := range m.secrets {
		if secret.AppID == appID {
			keys = append(keys, string(runtimeGenerationDigest(key))+string(runtimeGenerationDigest(secret)))
		}
	}
	sort.Strings(keys)
	write(keys)
	if at, ok := m.runtimeConfigChangedAt[appID]; ok {
		write(at.UnixNano())
	}
	var scopes []string
	for key, at := range m.environmentRuntimeConfigChangedAt {
		if key.AppID == appID {
			scopes = append(scopes, key.Scope+"@"+at.UTC().String())
		}
	}
	sort.Strings(scopes)
	write(scopes)
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, appID) {
			write(row)
		}
	}
	var retirements []string
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.retirements {
			if r.AppID == appID {
				retirements = append(retirements, string(runtimeGenerationDigest(r)))
			}
		}
	}
	sort.Strings(retirements)
	write(retirements)
	// The workload-retirement gate finds retirements through environment
	// slugs; the Postgres trigger moves every app of a project whose
	// environment is renamed or removed.
	var environments []string
	for id, env := range m.projectEnvironments {
		if app.ProjectID != "" && env.ProjectID == app.ProjectID {
			environments = append(environments, id+"/"+env.Slug)
		}
	}
	sort.Strings(environments)
	write(environments)
	var releases []string
	for id, release := range m.projectReleaseSets {
		if member := releaseMemberForApp(release, appID); member != "" {
			releases = append(releases, id+"/"+member+"/"+string(runtimeGenerationDigest(release.Active)))
		}
	}
	sort.Strings(releases)
	write(releases)
	return int64(h.Sum64()&0x7fffffffffffffff) | 1, nil
}

// runtimeGenerationBookkeeping lists the fields, normalized, that the
// triggers leave out: scale and miss stamps, activity counters, secret
// delivery and reload records, and enrollment leases and observations.
var runtimeGenerationBookkeeping = []string{
	"lastscaleinat", "lastscaleoutat", "lastdeployfailedemailat", "purgeclaimedat", "previewdestroycommentedat",
	"snapshotmisscount", "snapshotmisslastat", "snapshotmissbackoffuntil", "firstwakeat", "first5xxwindowendsat",
	"first5xxcount", "livenessrestartcount", "canarystepstartedat", "runtimeupgraderoutingtoken",
	"updatedat", "deliveredversion", "deliverystatus", "lastdelivery", "lastdelivered", "lastruntimereload",
	"leaseowner", "leasegeneration", "leaseuntil", "observedrevision", "observationcheckedat", "observationrevision",
}

// runtimeGenerationDigest encodes v canonically, without bookkeeping fields.
func runtimeGenerationDigest(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	for key := range fields {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		for _, prefix := range runtimeGenerationBookkeeping {
			if strings.HasPrefix(normalized, prefix) {
				delete(fields, key)
				break
			}
		}
	}
	out, _ := json.Marshal(fields)
	return out
}
