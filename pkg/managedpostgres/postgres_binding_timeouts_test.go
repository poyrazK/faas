package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

type bindingTimeoutTestProvider struct {
	*bindingProvider
	policies []BindingTimeoutPolicy
	err      error
}

func (p *bindingTimeoutTestProvider) ApplyBindingTimeouts(_ context.Context, _, _ string, policy BindingTimeoutPolicy) error {
	p.policies = append(p.policies, policy)
	return p.err
}

func TestBindingTimeoutPolicyPersistsAcrossRetryAndRotation(t *testing.T) {
	f := newPostgresRotationFixture(t, false)
	ctx := t.Context()
	provider := &bindingTimeoutTestProvider{bindingProvider: f.provider}
	f.service.registry = testRegistry(t, provider, func(c *Config) { c.ProvisioningEnabled = true })
	databaseID := uuid.NewString()
	if _, err := f.store.pool.Exec(ctx, `UPDATE managed_postgres_databases SET provider_resource_id = $2
		WHERE id = $1`, f.binding.DatabaseID, databaseID); err != nil {
		t.Fatal(err)
	}
	verifiers, err := dbcreds.NewVerifierStore(f.store.pool, 0)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := pgscram.NewVerifier("timeout-test-password", pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	role := "gregale_test_role"
	if _, err := f.store.pool.Exec(ctx, `UPDATE managed_postgres_bindings SET provider_identity_id = $2
		WHERE id = $1`, f.binding.ID, role); err != nil {
		t.Fatal(err)
	}
	if err := verifiers.Put(ctx, databaseID, role, verifier); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.BindingTimeouts(ctx, f.binding.AccountID, f.binding.ID)
	if err != nil || status.State != "default" {
		t.Fatalf("initial status: %+v, %v", status, err)
	}
	if _, err := f.service.BindingTimeouts(ctx, uuid.NewString(), f.binding.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrelated account lookup: %v", err)
	}

	stmt, lock := int32(5000), int32(1000)
	if _, err := f.service.SetBindingTimeouts(ctx, f.binding.AccountID, f.binding.ID, BindingTimeoutPatch{StatementMS: &stmt}); err != nil {
		t.Fatal(err)
	}
	patch := BindingTimeoutPatch{LockMS: &lock}
	status, err = f.service.SetBindingTimeouts(ctx, f.binding.AccountID, f.binding.ID, patch)
	if err != nil || status.State != "pending" || status.StatementMS != stmt || status.LockMS != lock || status.Revision != 2 {
		t.Fatalf("saved status: %+v, %v", status, err)
	}
	replay, err := f.service.SetBindingTimeouts(ctx, f.binding.AccountID, f.binding.ID, patch)
	if err != nil || replay.Revision != status.Revision {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	provider.err = ErrUnavailable
	if err := f.service.sweepBindingTimeouts(ctx, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first apply: %v", err)
	}
	roles := []dbcreds.DatabaseRole{{DatabaseID: databaseID, Role: role}}
	revisions, err := verifiers.PoolRevisions(ctx, roles)
	if err != nil || len(revisions) != 1 || revisions[roles[0]] != 0 {
		t.Fatalf("first apply published a revision: %v, %v", revisions, err)
	}
	if _, err := f.store.pool.Exec(ctx, `UPDATE native_postgres_binding_timeouts
		SET retry_at = now() WHERE binding_id = $1`, f.binding.ID); err != nil {
		t.Fatal(err)
	}
	provider.err = nil
	if err := f.service.sweepBindingTimeouts(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(provider.policies) != 2 || provider.policies[0] != status.BindingTimeoutPolicy || provider.policies[1] != status.BindingTimeoutPolicy {
		t.Fatalf("applied policies: %+v", provider.policies)
	}
	revisions, err = verifiers.PoolRevisions(ctx, roles)
	if err != nil || revisions[roles[0]] != status.Revision {
		t.Fatalf("retry revision: %v, %v", revisions, err)
	}
	f.now = time.Now().Add(dbcreds.PoolPolicyMaxAge)
	status, err = f.service.BindingTimeouts(ctx, f.binding.AccountID, f.binding.ID)
	if err != nil || status.State != "applied" || status.AppliedAt == nil {
		t.Fatalf("settled status: %+v, %v", status, err)
	}
	if _, _, err := f.store.BeginBindingRotation(ctx, f.binding.AccountID, f.binding.ID, uuid.NewString(), f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(ctx, f.binding.AccountID, f.binding.ID); err != nil {
		t.Fatal(err)
	}
	if got := provider.lastIssueRequest.TimeoutPolicy; got != status.BindingTimeoutPolicy {
		t.Fatalf("rotating credential got %+v, want %+v", got, status.BindingTimeoutPolicy)
	}
}
