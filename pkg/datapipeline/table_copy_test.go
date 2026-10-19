package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestTableCopyStallRequiresObservedProgressNotWorkerRestarts(t *testing.T) {
	start := time.Now().UTC()
	base := copyProgress(TableCopyProgress{}, TableCopyProgress{Table: "customers", Phase: "copying", NativeState: "d", Rows: 100, Bytes: 1000, CountersKnown: true, WorkerActive: true}, start)
	for _, tc := range []struct {
		name    string
		next    TableCopyProgress
		elapsed time.Duration
		stalled bool
	}{
		{"before threshold", base, api.DataPipelineCopyStallTimeout - time.Second, false},
		{"no progress", base, api.DataPipelineCopyStallTimeout, true},
		{"worker restarted", TableCopyProgress{Phase: "waiting", NativeState: "i"}, api.DataPipelineCopyStallTimeout, true},
		{"unknown counters", TableCopyProgress{Phase: "copying", NativeState: "d", WorkerActive: true}, api.DataPipelineCopyStallTimeout, true},
		{"more rows", TableCopyProgress{Phase: "copying", NativeState: "d", Rows: 101}, api.DataPipelineCopyStallTimeout, false},
		{"more bytes", TableCopyProgress{Phase: "copying", NativeState: "d", Bytes: 1001}, api.DataPipelineCopyStallTimeout, false},
		{"catching up", TableCopyProgress{Phase: "catching_up", NativeState: "s"}, api.DataPipelineCopyStallTimeout, false},
		{"ready", TableCopyProgress{Phase: "ready", NativeState: "r"}, 2 * api.DataPipelineCopyStallTimeout, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := copyProgress(base, tc.next, start.Add(tc.elapsed))
			if got.Stalled != tc.stalled {
				t.Fatal("incorrect stall evidence", got)
			}
		})
	}
	retrying := copyProgress(base, TableCopyProgress{Phase: "waiting", NativeState: "i"}, start.Add(api.DataPipelineCopyStallTimeout))
	retried := copyProgress(retrying, TableCopyProgress{Phase: "copying", NativeState: "d", Rows: 100, Bytes: 1000}, start.Add(api.DataPipelineCopyStallTimeout+time.Second))
	if !retried.Stalled || !retried.ProgressAt.Equal(start) {
		t.Fatal("repeated failed COPY reset progress clock", retried)
	}
}

func TestCopyRecoveryRequestsAreScopedAndFreshWithoutVerification(t *testing.T) {
	s, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	s.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	plan.State = "verifying"
	plan.AddedTables = []string{"customers"}
	plan.RequestedTables = []string{"customers", "orders"}
	plan.Copies = []TableCopyProgress{{Table: "customers", Phase: "copying", NativeState: "d", Stalled: true}}
	encoded, _ := json.Marshal(plan)
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,schema_approval=$3,reason='table_copy_stalled',tables='["customers","orders"]',verification_progress='{}' WHERE id=$1`, p.ID, encoded, plan.ID); err != nil {
		t.Fatal(err)
	}
	p, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || !CanRecoverTable(p) {
		t.Fatal("COPY required a verification mismatch", err)
	}
	for _, table := range []string{"orders", "not_selected"} {
		if err = s.TableRecovery(ctx, p.AccountID, p.ID, p.Revision, "", table); !errors.Is(err, ErrConflict) {
			t.Fatal("unapproved table recovery accepted", err)
		}
	}
	if err = s.TableRecovery(ctx, p.AccountID, p.ID, p.Revision, "", "customers"); err != nil {
		t.Fatal(err)
	}
	requested, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || requested.SchemaChange.Recovery.Kind != "copy" || requested.SchemaChange.Recovery.Table != "customers" {
		t.Fatal("COPY preview intent missing", err)
	}
	// A stalled bit alone cannot authorize recopying a ready table, and a
	// missing tracking entry needs no invented row-comparison failure.
	for _, phase := range []string{"ready", "missing"} {
		copy := p
		copyPlan := *p.SchemaChange
		copy.SchemaChange = &copyPlan
		copyPlan.Copies = []TableCopyProgress{{Table: "customers", Phase: phase, Stalled: false}}
		if phase == "ready" {
			copyPlan.Copies[0].NativeState = "r"
		} else {
			copy.Reason = "table_addition_tracking_lost"
		}
		if CanRecoverTable(copy) != (phase == "missing") {
			t.Fatal("incorrect copy recovery eligibility", phase)
		}
		copy.ObservationFresh = false
		if CanRecoverTable(copy) {
			t.Fatal("stale COPY recovery offered")
		}
	}
	copyApproval := TableRecovery{Table: "customers", Kind: "copy", Attempt: 1, TableOID: 10, SubscriptionOID: 11, PublicationOID: 12}
	legacy := copyApproval
	legacy.Kind = ""
	if tableRecoveryID(p, copyApproval) == tableRecoveryID(p, legacy) {
		t.Fatal("COPY and mismatch approvals shared an identity")
	}
	copyP := p
	copyPlan := *p.SchemaChange
	copyP.SchemaChange = &copyPlan
	copyP.Reason = "table_addition_tracking_lost"
	copyPlan.AddedTables = []string{"customers", "accounts"}
	copyPlan.Copies = []TableCopyProgress{{Table: "customers", Phase: "missing"}, {Table: "accounts", Phase: "missing"}}
	if CanRecoverTable(copyP) {
		t.Fatal("isolated REFRESH could adopt another untracked table")
	}
	copyPlan.Copies[1].NativeState = "d"
	copyPlan.Copies[1].Phase = "copying"
	if choices := RecoverableTables(copyP); len(choices) != 1 || choices[0] != "customers" {
		t.Fatal("another tracked COPY blocked isolated recovery", choices)
	}
}

func TestCopyProgressReceiptsBindGenerationAndSchemaAndResetAttempts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString(), SchemaApproval: strings.Repeat("a", 64), SchemaChange: &SchemaChange{}}
	p.ID, p.Generation = uuid.NewString(), 1
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline; CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_change text,retiring boolean)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,$2,false)`, p.ID, p.SchemaApproval); err != nil {
		t.Fatal(err)
	}
	if err := ensureTableCopyReceipt(ctx, pool); err != nil {
		t.Fatal(err)
	}
	copies := []TableCopyProgress{{Table: "customers", Phase: "copying", NativeState: "d", PeakRows: 100, PeakBytes: 1000, PeakState: "d", Stalled: true, ProgressAt: time.Now().Add(-time.Hour)}}
	if err := saveTableCopies(ctx, p, pool, copies); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadTableCopies(ctx, p, pool)
	if err != nil || len(reloaded) != 1 || !reloaded[0].Stalled {
		t.Fatal("private progress did not survive catalog loss", err)
	}
	for _, mutate := range []func(*Pipeline){func(p *Pipeline) { p.Generation++ }, func(p *Pipeline) { p.SchemaApproval = strings.Repeat("b", 64) }} {
		stale := p
		mutate(&stale)
		if err = saveTableCopies(ctx, stale, pool, nil); !errors.Is(err, ErrConflict) {
			t.Fatal("stale copy observation replaced progress", err)
		}
	}
	if err = resetTableCopy(ctx, p, pool, "customers"); err != nil {
		t.Fatal(err)
	}
	reloaded, err = loadTableCopies(ctx, p, pool)
	if err != nil || reloaded[0].Stalled || reloaded[0].PeakRows != 0 || time.Since(reloaded[0].ProgressAt) > time.Minute {
		t.Fatal("new copy reused the old stall clock", err)
	}
}
