package datapipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func servingFixture() *api.DataPipelineServing {
	return &api.DataPipelineServing{Version: 1, Columns: map[string][]string{"orders": {"id", "tenant_id", "status"}}, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", Memberships: []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}, {Subject: "bob", Tenant: "b"}}}}
}

func servingTable() api.DataSourceTable {
	return api.DataSourceTable{Name: "orders", Supported: true, Columns: []api.DataSourceColumn{{Name: "id", Type: "int4", PrimaryKey: true}, {Name: "tenant_id", Type: "text"}, {Name: "status", Type: "text"}, {Name: "secret", Type: "text"}}}
}

func TestServingContractRejectsAmbiguousAccess(t *testing.T) {
	req := api.CreateDataPipelineRequest{Name: "tenant-orders", SourceConnectionID: uuid.NewString(), DatabaseID: uuid.NewString(), Tables: []string{"orders"}, Serving: servingFixture()}
	if err := ValidateCreate(req); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*api.CreateDataPipelineRequest){
		func(r *api.CreateDataPipelineRequest) { r.ReadSubjects = []string{"admin"} },
		func(r *api.CreateDataPipelineRequest) { r.Serving.Version = 2 },
		func(r *api.CreateDataPipelineRequest) { r.Serving.Columns["orders"] = []string{"id", "status"} },
		func(r *api.CreateDataPipelineRequest) {
			r.Serving.Columns["orders"] = []string{"id", "tenant_id", "id"}
		},
		func(r *api.CreateDataPipelineRequest) {
			r.Serving.TenantAccess.Memberships = []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}, {Subject: "alice", Tenant: "a"}}
		},
	} {
		bad := req
		bad.Serving = normalizeServing(req.Serving)
		mutate(&bad)
		if !errors.Is(ValidateCreate(bad), ErrInvalid) {
			t.Fatal("ambiguous access contract accepted", bad)
		}
	}
	if got := normalize(req); !reflect.DeepEqual(got.ReadSubjects, []string{"alice", "bob"}) {
		t.Fatal("subject envelope did not derive from authoritative membership", got)
	}
}

func TestServingProjectionPreservesKeysAndExcludesUnsupportedColumns(t *testing.T) {
	profile, table := servingFixture(), servingTable()
	table.Supported = false
	table.Issues = []api.DataConnectionIssue{{Code: "source_column_unsupported", Column: "secret", State: "blocked"}}
	projected, err := projectTable(table, profile)
	if err != nil || !projected.Supported || len(projected.Columns) != 3 {
		t.Fatal("excluded column still affects projected compatibility", projected, err)
	}
	profile.Columns["orders"] = []string{"tenant_id", "status"}
	if _, err = projectTable(table, profile); !errors.Is(err, ErrInvalid) {
		t.Fatal("projection omitted the replication key", err)
	}
	profile.Columns["orders"] = []string{"id", "tenant_id", "missing"}
	if _, err = projectTable(table, profile); !errors.Is(err, ErrInvalid) {
		t.Fatal("projection accepted undiscovered columns", err)
	}
	profile.Columns["orders"] = []string{"id", "tenant_id", "status"}
	table.Issues = []api.DataConnectionIssue{{Code: "source_table_unsupported", State: "blocked"}}
	if projected, err = projectTable(table, profile); err != nil || projected.Supported {
		t.Fatal("column selection bypassed table-level compatibility", projected, err)
	}
}

func TestServingLegacyReaderApprovalHashPreserved(t *testing.T) {
	p := Pipeline{AccountID: "account", DataPipeline: api.DataPipeline{ID: "pipeline"}}
	c := ReaderChange{State: "approved", Generation: 1, PreviewRevision: 3, Before: []string{"alice"}, After: []string{"bob"}, Tables: []string{"orders"}}
	old := struct {
		Account, Pipeline string
		Change            struct {
			ID, State                   string
			Generation, PreviewRevision int64
			Before, After, Tables       []string
		}
	}{Account: p.AccountID, Pipeline: p.ID}
	old.Change.Generation, old.Change.PreviewRevision = c.Generation, c.PreviewRevision
	old.Change.Before, old.Change.After, old.Change.Tables = c.Before, c.After, c.Tables
	b, _ := json.Marshal(old)
	sum := sha256.Sum256(b)
	if readerChangeID(p, c) != hex.EncodeToString(sum[:]) {
		t.Fatal("upgrade invalidated an already approved legacy reader change")
	}
}

func TestServingClaimsRequireNewWorkerProtocol(t *testing.T) {
	s, store, _, account := testService(t)
	c, err := s.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "tenant-source", ConnectionURL: "postgres://reader:password@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c, servingTable())
	p, err := s.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: "tenant-orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, Serving: servingFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.q.PipelineClaim(t.Context(), store.pool, "legacy-worker"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("N-1 claim SQL acquired a contract it cannot enforce", err)
	}
	claimed, err := store.Claim(t.Context(), "serving-worker")
	if err != nil || claimed.ID != p.ID || claimed.ServingInvalid || claimed.Serving == nil {
		t.Fatal("capable worker could not claim the serving contract", claimed, err)
	}
	var marker string
	if err = store.pool.QueryRow(t.Context(), `SELECT coalesce(current_setting('gregale.pipeline_worker_protocol',true),'')`).Scan(&marker); err != nil || marker != "" {
		t.Fatal("worker capability leaked to a later pool borrower", marker, err)
	}
	if err = s.Readers(t.Context(), account, p.ID, p.Revision, "preview", "", []string{"mallory"}); !errors.Is(err, ErrConflict) {
		t.Fatal("whole-table reader API widened tenant access", err)
	}
}

func TestServingSavedSetupClaimsRequireNewWorkerProtocol(t *testing.T) {
	s, store, account, c, req := setupFixture(t)
	req.Serving, req.ReadSubjects = servingFixture(), nil
	metadata, _ := json.Marshal([]api.DataSourceTable{servingTable()})
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET result=jsonb_set(result,'{tables}',$2::jsonb) WHERE connection_id=$1`, c.ID, metadata); err != nil {
		t.Fatal(err)
	}
	job, err := s.StartSetup(t.Context(), account, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.q.PipelineSetupClaim(t.Context(), store.pool, "old-worker"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("N-1 setup worker acquired a serving contract", err)
	}
	claimed, err := store.ClaimSetup(t.Context(), "new-worker")
	if err != nil || claimed.ID != job.ID || claimed.Request.Serving == nil {
		t.Fatal("new setup worker could not claim", claimed, err)
	}
}

func TestServingMaximumEscapedMembershipsFitDurableLimits(t *testing.T) {
	members := make([]api.DataPipelineTenantMembership, api.DataPipelineTenantMembershipsMax)
	for i := range members {
		suffix := strconv.Itoa(i / 2)
		members[i] = api.DataPipelineTenantMembership{Subject: strings.Repeat("\x01", api.DataPipelineSubjectMaxBytes-len(suffix)) + suffix, Tenant: strings.Repeat(string(rune(i%2+2)), api.DataPipelineTenantMaxBytes)}
	}
	if !validMemberships(members) {
		t.Fatal("fixture exceeds documented limits")
	}
	profile := &api.DataPipelineServing{Version: 1, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", Memberships: members}}
	encoded, _ := json.Marshal(profile)
	if len(encoded) > api.DataPipelineRequestMaxBytes || len(encoded) > api.DataPipelineServingMaxBytes {
		t.Fatal("valid escaped membership cannot be submitted", len(encoded))
	}
	change := ReaderChange{Tenant: true, BeforeMemberships: members, AfterMemberships: members, Before: membershipSubjects(members), After: membershipSubjects(members)}
	encoded, _ = json.Marshal(change)
	if len(encoded) > api.DataPipelineReaderChangeMaxBytes {
		t.Fatal("valid escaped preview cannot be saved", len(encoded))
	}
}
