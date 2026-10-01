package state

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// An expired execution lease still owns capacity until the invocation reaper
// releases it. Deletion must never make its running guest or quota disappear.
var ErrEnvironmentInvocationWorkBusy = fmt.Errorf("environment invocation work is still running: %w", ErrConflict)

func cleanupEnvironmentInvocationsDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, slug string) error {
	q := sqlc.New()
	id, err := q.LockEnvironmentInvocationCleanup(ctx, db, sqlc.LockEnvironmentInvocationCleanupParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Slug: slug})
	if err != nil {
		return mapErr(err)
	}
	check, err := q.ValidateEnvironmentInvocationCleanup(ctx, db, id)
	if err != nil {
		return fmt.Errorf("state: validate environment invocation cleanup: %w", err)
	}
	if !check.Invalid.Valid || check.Invalid.Bool {
		return ErrInvocationEnvironmentWorkIsolation
	}
	if check.Busy {
		return ErrEnvironmentInvocationWorkBusy
	}
	if _, err := q.DeleteEnvironmentInvocations(ctx, db, id); err != nil {
		return fmt.Errorf("state: delete environment invocations: %w", err)
	}
	for _, remove := range []func(context.Context, sqlc.DBTX) error{
		func(ctx context.Context, db sqlc.DBTX) error { return q.DeleteEnvironmentWorkKeyLanes(ctx, db, id) },
		func(ctx context.Context, db sqlc.DBTX) error {
			return q.DeleteEnvironmentWorkFairnessLanes(ctx, db, id)
		},
		func(ctx context.Context, db sqlc.DBTX) error { return q.DeleteEnvironmentWorkDomains(ctx, db, id) },
	} {
		if err := remove(ctx, db); err != nil {
			return fmt.Errorf("state: delete environment work ownership: %w", err)
		}
	}
	// Keep cancellation receipts: reusing an old operation identity in another
	// namespace must remain a conflict after this environment is gone.
	return nil
}

// Validate before changing any map: MemStore must offer the same all-or-nothing
// behavior as the PostgreSQL environment deletion transaction.
func (m *MemStore) validateEnvironmentInvocationCleanupLocked(environmentID string) error {
	env, exists := m.projectEnvironments[environmentID]
	if !exists {
		return ErrNotFound
	}
	busy := false
	for id, inv := range m.invocations {
		owner, admitted := m.invocationWorkEnvironmentAdmissions[id]
		if admitted && owner.EnvironmentID == environmentID && inv.EnvironmentID != environmentID {
			return ErrInvocationEnvironmentWorkIsolation
		}
		for kind, digest := range map[string][]byte{"key": inv.WorkKeyDigest, "fairness": inv.WorkFairnessDigest} {
			if m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(inv.AppID, inv.WorkPolicyName, kind, digest)] == environmentID && inv.EnvironmentID != environmentID {
				return ErrInvocationEnvironmentWorkIsolation
			}
		}
		if inv.EnvironmentID != environmentID {
			continue
		}
		app, owned := m.apps[inv.AppID]
		if !owned || app.AccountID != env.AccountID || app.ProjectID != env.ProjectID || inv.AccountID != env.AccountID || !stageKeyedInvocationSupported(inv) {
			return ErrInvocationEnvironmentWorkIsolation
		}
		if inv.WorkPolicyName == "" {
			if admitted {
				return ErrInvocationEnvironmentWorkIsolation
			}
		} else {
			spec, pinned := m.projectEnvironmentWorkloadSpecs[owner.WorkloadSpecID]
			hash, err := WorkloadSettingsHash(spec.Settings)
			if !admitted || !admissionMatchesInvocation(owner, inv) || !pinned || spec.EnvironmentID != environmentID || spec.AppID != app.ID || spec.Hash != owner.SettingsHash || err != nil || hash != spec.Hash ||
				m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(app.ID, inv.WorkPolicyName, "key", inv.WorkKeyDigest)] != environmentID ||
				(inv.WorkFairnessLimit > 0 && m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(app.ID, inv.WorkPolicyName, "fairness", inv.WorkFairnessDigest)] != environmentID) {
				return ErrInvocationEnvironmentWorkIsolation
			}
		}
		busy = busy || inv.State == InvocationDispatching || inv.QuotaReserved
	}
	for key, ownerID := range m.invocationWorkEnvironmentDomains {
		if ownerID != environmentID {
			continue
		}
		parts := strings.Split(key, "\x00")
		if len(parts) != 4 {
			return ErrInvocationEnvironmentWorkIsolation
		}
		app, owned := m.apps[parts[0]]
		digest, err := hex.DecodeString(parts[3])
		if !owned || app.AccountID != env.AccountID || app.ProjectID != env.ProjectID || parts[1] == "" || (parts[2] != "key" && parts[2] != "fairness") || err != nil || len(digest) != 32 {
			return ErrInvocationEnvironmentWorkIsolation
		}
	}
	if busy {
		return ErrEnvironmentInvocationWorkBusy
	}
	return nil
}

func (m *MemStore) deleteEnvironmentInvocationsLocked(environmentID string) {
	for id, inv := range m.invocations {
		if inv.EnvironmentID == environmentID {
			delete(m.invocations, id)
			delete(m.invocationWorkEnvironmentAdmissions, id)
		}
	}
	for key, ownerID := range m.invocationWorkEnvironmentDomains {
		if ownerID == environmentID {
			delete(m.invocationWorkEnvironmentDomains, key)
		}
	}
}
