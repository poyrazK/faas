package datapipeline

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

func TestCapacityQualificationRequiresPhysicalScopeAndExactPlacement(t *testing.T) {
	now := time.Now().UTC()
	q := connectioncapacity.Qualification{Scope: "shared-physical-server", Endpoints: []string{"db.example:5432"}, Placement: "immutable-provider-resource", Generation: 1, ReportSHA256: strings.Repeat("a", 64), QualifiedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Allocation: connectioncapacity.Limits{Connections: 95, Slots: 10, Senders: 10}, External: connectioncapacity.Limits{Connections: 2}}
	if err := validateQualifications([]connectioncapacity.Qualification{q}, now); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*connectioncapacity.Qualification){"unbounded external": func(q *connectioncapacity.Qualification) { q.External.Connections = -1 }, "no live report": func(q *connectioncapacity.Qualification) { q.ReportSHA256 = "" }, "stale qualification": func(q *connectioncapacity.Qualification) { q.QualifiedAt = now.Add(-48 * time.Hour) }, "unpinned target": func(q *connectioncapacity.Qualification) {
		q.Endpoints = nil
		q.DatabaseID = "00000000-0000-0000-0000-000000000001"
	}} {
		t.Run(name, func(t *testing.T) {
			bad := q
			change(&bad)
			if validateQualifications([]connectioncapacity.Qualification{bad}, now) == nil {
				t.Fatal("unqualified allocation accepted")
			}
		})
	}
	expired := q
	expired.QualifiedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	if err := validateQualifications([]connectioncapacity.Qualification{expired}, now); err != nil {
		t.Fatal("expired evidence prevented cleanup worker startup", err)
	}
	if expired.Valid(now) {
		t.Fatal("expired qualification admitted new work")
	}
	if _, err := (Config{Capacity: []connectioncapacity.Qualification{expired}}).Policy(); err != nil {
		t.Fatal("expired staging qualification prevented API startup", err)
	}
	alias := q
	alias.Endpoints = []string{"alias.example:5432"}
	alias.Allocation.Connections++
	if validateQualifications([]connectioncapacity.Qualification{q, alias}, now) == nil {
		t.Fatal("aliases received different budgets for the same physical scope")
	}
	alias = q
	alias.Endpoints = []string{"alias.example:5432"}
	if err := validateQualifications([]connectioncapacity.Qualification{q, alias}, now); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityObserverMeasuresSettingsWithoutInventingProviderCapacity(t *testing.T) {
	raw := os.Getenv("DATA_PIPELINE_TEST_DSN")
	if raw == "" {
		t.Skip("run make test-data-pipeline for PostgreSQL 16 capacity measurements")
	}
	conn, err := pgx.Connect(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	now := time.Now().UTC()
	q := connectioncapacity.Qualification{Scope: "local-native-fixture", Placement: "disposable-pg16", Generation: 1, ReportSHA256: strings.Repeat("b", 64), QualifiedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Allocation: connectioncapacity.Limits{Connections: 100000, Slots: 100000, Senders: 100000, Workers: 100000}, External: connectioncapacity.Limits{Connections: 5, Slots: 1, Senders: 1, Workers: 1}}
	e, err := observeCapacity(t.Context(), conn, q)
	if err != nil {
		t.Fatal(err)
	}
	var maximum int64
	if err = conn.QueryRow(t.Context(), `SELECT current_setting('max_connections')::bigint-current_setting('superuser_reserved_connections')::bigint-current_setting('reserved_connections')::bigint-5`).Scan(&maximum); err != nil {
		t.Fatal(err)
	}
	if e.Connections != maximum || e.Slots != 19 || e.Senders != 19 || e.Workers != 9 {
		t.Fatal("measurements exceeded physical settings or external allowance", e)
	}
	q.Allocation.Connections = 7
	e, err = observeCapacity(t.Context(), conn, q)
	if err != nil || e.Connections != 7 {
		t.Fatal("physical maximum replaced provider allocation", e, err)
	}
	q.ReportSHA256 = ""
	if _, err = observeCapacity(t.Context(), conn, q); !errors.Is(err, connectioncapacity.ErrUnknown) {
		t.Fatal("settings alone qualified the provider", err)
	}
}

func TestCapacityReportIncludesPeakAndRejectsExpiredEvidence(t *testing.T) {
	now := time.Now().UTC()
	s := connectioncapacity.Snapshot{Evidence: connectioncapacity.Evidence{Limits: connectioncapacity.Limits{Connections: 10, Slots: 6, Senders: 6}, ObservedAt: now, ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge)}, Reserved: connectioncapacity.PipelineSourceDemand()}
	r := capacityReport(s, connectioncapacity.PipelineSourceDemand(), nil)
	if r.State != "ready" || r.RequiredConnections != 5 || r.ReservedConnections != 5 {
		t.Fatal(r)
	}
	s.Reserved.Slots++
	if r = capacityReport(s, connectioncapacity.PipelineSourceDemand(), nil); r.State != "insufficient_capacity" || r.LimitingResource != "replication_slots" {
		t.Fatal(r)
	}
	s.ExpiresAt = now
	if r = capacityReport(s, connectioncapacity.PipelineSourceDemand(), nil); r.State != "needs_qualification" || r.UsableConnections != 0 {
		t.Fatal(r)
	}
}
