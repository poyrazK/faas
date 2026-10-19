package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity"
)

func TestConnectionCapacityPGPersistsAndInvalidatesEvidence(t *testing.T) {
	s, _, ctx, account := postgresStoreFixture(t)
	d := postgresReadyDatabase(t, s, account, "capacity-primary", time.Now().UTC().Add(-time.Minute))
	var providerErr error
	provider := &capacityTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, read: func(ctx context.Context, target Database) (connectioncapacity.Observation, error) {
		if _, ok := ctx.Deadline(); !ok || target.ID != d.ID {
			t.Error("unbounded or incorrect capacity target")
		}
		return connectioncapacity.Observation{MaxConnections: 50, SuperuserReserved: 3, Reserved: 2, DatabaseLimit: -1, RoleLimit: -1}, providerErr
	}}
	registry := testRegistry(t, provider, nil)
	backend, _ := registry.Default(d.Spec.Region)
	if _, err := s.pool.Exec(ctx, `UPDATE managed_postgres_databases SET backend_fingerprint=$2,data_resource_id=$3 WHERE id=$1`, d.ID, backend.Fingerprint, "private-project/private-dataset"); err != nil {
		t.Fatal(err)
	}
	var err error
	d, err = s.Get(ctx, account, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := capacityAllocation(d, time.Now().UTC())
	result, err := ObserveConnectionCapacity(ctx, registry, s, account, d.ID, a)
	if err != nil || result.State != "verified" || result.Evidence.UsableConnections != 40 {
		t.Fatal(result, err)
	}
	// A new process reads the persisted cache without opening tenant SQL.
	restarted, _ := NewPostgresStore(s.pool)
	bindings := &BindingService{databases: restarted, bindings: restarted, now: func() time.Time { return time.Now().UTC() }}
	report, err := bindings.ConnectionBudget(ctx, account, d.ID)
	if err != nil || report.Capacity.State != "verified" || *report.Capacity.UsableConnections != 40 || report.State != "unknown" || report.Enforced || slices.Contains(report.Reasons, "capacity_unverified") {
		t.Fatal(report, err)
	}
	if _, err := bindings.ConnectionBudget(ctx, uuid.NewString(), d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account capacity read", err)
	}
	providerErr = errors.New("permission denied for private-provider")
	failed, err := ObserveConnectionCapacity(ctx, registry, s, account, d.ID, a)
	if err != nil || failed.State != "unknown" {
		t.Fatal(failed, err)
	}
	report, err = bindings.ConnectionBudget(ctx, account, d.ID)
	if err != nil || report.Capacity.State != "unknown" || report.Capacity.UsableConnections != nil {
		t.Fatal("failure reused a successful observation", report, err)
	}
	providerErr = nil
	if _, err = ObserveConnectionCapacity(ctx, registry, s, account, d.ID, a); err != nil {
		t.Fatal(err)
	}
	// A generation mismatch and dataset replacement invalidate cached proof
	// immediately, even before its short freshness window expires.
	for _, update := range []string{
		`UPDATE managed_postgres_databases SET state='updating',desired_generation=desired_generation+1 WHERE id=$1`,
		`UPDATE managed_postgres_databases SET state='ready',observed_generation=desired_generation,data_resource_id='private-replacement' WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(ctx, update, d.ID); err != nil {
			t.Fatal(err)
		}
		report, err = bindings.ConnectionBudget(ctx, account, d.ID)
		if err != nil || report.Capacity.State != "unknown" || !slices.Contains(report.Reasons, "capacity_unverified") {
			t.Fatal("identity change retained capacity", report, err)
		}
	}
}

func TestConnectionCapacityPGLeasesFenceConcurrentAndLateWorkers(t *testing.T) {
	s, _, ctx, account := postgresStoreFixture(t)
	d := postgresReadyDatabase(t, s, account, "capacity-leases", time.Now().UTC().Add(-time.Minute))
	if _, err := s.pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id='private-dataset' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, account, d.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	claim := func(d Database, token string, at time.Time) error {
		return s.ClaimConnectionCapacity(ctx, d, token, at, at.Add(api.DataAPIConnectionCapacityLease))
	}
	if err := claim(d, "first", now); err != nil {
		t.Fatal(err)
	}
	if err := claim(d, "second", now.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate worker acquired live claim", err)
	}
	wrong := d
	wrong.AccountID = uuid.NewString()
	if err := claim(wrong, "foreign", now); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-account claim", err)
	}
	later := now.Add(api.DataAPIConnectionCapacityLease + time.Second)
	if err := claim(d, "second", later); err != nil {
		t.Fatal(err)
	}
	result := ConnectionCapacityResult{State: "unknown", CheckedAt: now, ExpiresAt: now}
	if err := s.FinishConnectionCapacity(ctx, d, "first", result, later); !errors.Is(err, ErrConflict) {
		t.Fatal("expired worker overwrote successor", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_postgres_databases SET backend_fingerprint=$2 WHERE id=$1`, d.ID, strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	result.CheckedAt, result.ExpiresAt = later, later
	if err := s.FinishConnectionCapacity(ctx, d, "second", result, later); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced backend accepted late result", err)
	}
}
