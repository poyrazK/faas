package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity"
)

type capacityTestProvider struct {
	*fakeProvider
	read func(context.Context, Database) (connectioncapacity.Observation, error)
}

func (p *capacityTestProvider) ObserveConnectionCapacity(ctx context.Context, d Database) (connectioncapacity.Observation, error) {
	return p.read(ctx, d)
}

func capacityAllocation(d Database, now time.Time) *QualifiedConnectionAllocation {
	return &QualifiedConnectionAllocation{Version: 1, AccountID: d.AccountID, DatabaseID: d.ID, BackendID: d.BackendID,
		BackendFingerprint: d.BackendFingerprint, ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID,
		Generation: d.DesiredGeneration, UsableConnections: 40, Source: "native_qualification", SourceSHA256: strings.Repeat("b", 64),
		CheckedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
}

func capacityTarget(registry *Registry) Database {
	backend, _ := registry.Default(testSpec().Region)
	return Database{ID: uuid.NewString(), AccountID: uuid.NewString(), Name: "primary", State: StateReady, Spec: testSpec(),
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "private-project",
		DataResourceID: "private-project/private-branch", DesiredGeneration: 2, ObservedGeneration: 2}
}

func TestConnectionCapacityRefusesUnqualifiedAllocationBeforeSQL(t *testing.T) {
	calls := 0
	provider := &capacityTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, read: func(context.Context, Database) (connectioncapacity.Observation, error) {
		calls++
		return connectioncapacity.Observation{}, nil
	}}
	registry := testRegistry(t, provider, nil)
	d := capacityTarget(registry)
	now := time.Now().UTC()
	for name, change := range map[string]func(*QualifiedConnectionAllocation){
		"database":   func(a *QualifiedConnectionAllocation) { a.DatabaseID = uuid.NewString() },
		"account":    func(a *QualifiedConnectionAllocation) { a.AccountID = uuid.NewString() },
		"backend":    func(a *QualifiedConnectionAllocation) { a.BackendID += "-other" },
		"placement":  func(a *QualifiedConnectionAllocation) { a.BackendFingerprint = strings.Repeat("c", 64) },
		"generation": func(a *QualifiedConnectionAllocation) { a.Generation++ },
		"provider":   func(a *QualifiedConnectionAllocation) { a.ProviderResourceID += "-other" },
		"dataset":    func(a *QualifiedConnectionAllocation) { a.DataResourceID += "-other" },
		"expired":    func(a *QualifiedConnectionAllocation) { a.ExpiresAt = now },
		"future":     func(a *QualifiedConnectionAllocation) { a.CheckedAt = now.Add(time.Second) },
		"long_lived": func(a *QualifiedConnectionAllocation) { a.ExpiresAt = now.Add(time.Hour) },
		"source":     func(a *QualifiedConnectionAllocation) { a.Source = "max_connections" },
		"provenance": func(a *QualifiedConnectionAllocation) { a.SourceSHA256 = "unqualified" },
		"zero":       func(a *QualifiedConnectionAllocation) { a.UsableConnections = 0 },
		"overflow":   func(a *QualifiedConnectionAllocation) { a.UsableConnections = api.DataAPIConnectionBudgetMaxCount + 1 },
		"version":    func(a *QualifiedConnectionAllocation) { a.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			a := capacityAllocation(d, now)
			change(a)
			got := observeConnectionCapacity(t.Context(), registry, d, a, now)
			if got.State != "unknown" || got.Reason != "allocation_unverified" || got.Evidence != nil || calls != 0 {
				t.Fatalf("unqualified allocation reached SQL or became capacity: %+v calls=%d", got, calls)
			}
		})
	}
	if got := observeConnectionCapacity(t.Context(), registry, d, nil, now); got.State != "unknown" || calls != 0 {
		t.Fatal(got, calls)
	}
}

func TestConnectionCapacityClampsSQLCeilingsAndPreservesUnknownCoverage(t *testing.T) {
	observation := connectioncapacity.Observation{MaxConnections: 100, SuperuserReserved: 3, Reserved: 2, DatabaseLimit: 25, RoleLimit: 20}
	var providerErr error
	provider := &capacityTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, read: func(ctx context.Context, _ Database) (connectioncapacity.Observation, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("SQL observation has no deadline")
		}
		return observation, providerErr
	}}
	registry := testRegistry(t, provider, nil)
	d := capacityTarget(registry)
	now := time.Now().UTC()
	a := capacityAllocation(d, now)
	a.ExpiresAt = now.Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got := observeConnectionCapacity(ctx, registry, d, a, now)
	if got.State != "verified" || got.Evidence.UsableConnections != 20 || !got.ExpiresAt.Equal(a.ExpiresAt) || validateConnectionCapacityResult(d, got, now) != nil {
		t.Fatal(got)
	}
	snapshot := ConnectionBudgetSnapshot{DatabaseID: d.ID, DatabaseName: d.Name, DatabaseState: "ready", BackendFingerprint: d.BackendFingerprint,
		Generation: d.DesiredGeneration, CheckedAt: now, Capacity: got.Evidence}
	report := describeConnectionBudget(snapshot, now)
	if report.State != "unknown" || report.Enforced || report.Capacity.State != "verified" || *report.Capacity.UsableConnections != 20 ||
		!slices.Contains(report.Reasons, "runtime_pool_configuration_unverified") || !slices.Contains(report.Reasons, "external_consumers_unobserved") ||
		report.Capacity.Source != a.Source || report.Capacity.ExpiresAt != got.ExpiresAt.Format(time.RFC3339Nano) {
		t.Fatal(report)
	}
	encoded, _ := json.Marshal(report)
	for _, private := range []string{d.ProviderResourceID, d.BackendFingerprint, a.SourceSHA256, "max_connections"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private observation metadata escaped", string(encoded))
		}
	}
	if expired := describeConnectionBudget(snapshot, got.ExpiresAt); expired.Capacity.State != "unknown" || expired.Capacity.UsableConnections != nil {
		t.Fatal("expired capacity retained", expired)
	}
	observation.DatabaseLimit = 0
	zero := observeConnectionCapacity(ctx, registry, d, a, now)
	if zero.State != "verified" || zero.Evidence.UsableConnections != 0 {
		t.Fatal("zero ceiling was manufactured as missing evidence", zero)
	}
	providerErr = errors.New("SQL permission denied at private-host with private-password")
	failed := observeConnectionCapacity(ctx, registry, d, a, now)
	if failed.State != "unknown" || failed.Reason != "sql_capacity_unavailable" || failed.Evidence != nil {
		t.Fatal(failed)
	}
}

func TestDecodeConnectionAllocationBoundsAndDocuments(t *testing.T) {
	for _, input := range []string{`{"unexpected":true}`, `{} {}`, `{`, `{}` + strings.Repeat(" ", api.DataAPIConnectionAllocationMaxBytes)} {
		if _, err := DecodeConnectionAllocation(strings.NewReader(input)); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid allocation document")
		}
	}
	a := QualifiedConnectionAllocation{Version: 1, Source: "provider_api", CheckedAt: time.Now().UTC()}
	encoded, _ := json.Marshal(a)
	decoded, err := DecodeConnectionAllocation(strings.NewReader(string(encoded)))
	if err != nil || decoded.Version != a.Version || !decoded.CheckedAt.Equal(a.CheckedAt) {
		t.Fatal(decoded, err)
	}
}
