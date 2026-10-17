package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1641 — old queue writers may retain intent, but a legacy owner cannot
// receive a capped physical claim, even through the driver's bulk-copy path.
func TestFinancialComputeAccountLegacyJobWriter(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newAccountComputeFixture(t, store, 0, 1000)
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"jobs"}, []string{"account_id", "kind", "name", "image_ref", "ram_mb", "task_timeout_s", "max_parallelism", "retry_max", "env_overrides", "command"}, pgx.CopyFromRows([][]any{{f.account.ID, "batch", "legacy-job", "example/image", int32(128), int32(30), int32(1), int32(0), []byte("{}"), []string{"/job"}}}))
	if err != nil {
		t.Fatalf("legacy definition should remain safe: %v", err)
	}
	job, err := store.JobGetByName(ctx, f.account.ID, "legacy-job")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, f.account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "legacy-job-owner", TargetURL: "unix:///unused-job.sock", VPCPUs: 2, MemMB: 4096, MaxConcurrency: 2, AdmissionCeilingMB: 3000, VCPUBudget: 2, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	owner := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	nodeID := pgtype.UUID{Bytes: uuid.MustParse(node.ID), Valid: true}
	inc := pgtype.UUID{Bytes: uuid.MustParse(owner.Incarnation), Valid: true}
	// ADR-1640 knows account apps but omits the new job attestation.
	if err := sqlc.New().FinancialComputeOwnerPut(ctx, pool, sqlc.FinancialComputeOwnerPutParams{NodeID: nodeID, Incarnation: inc, AccountComputeIncarnation: inc}); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateJobInstance(ctx, uuid.NewString(), job.ID, run.ID, 0, string(state.StateColdBooting), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	// Insert a second task directly so application admission cannot mask the DB guard.
	_, err = pool.CopyFrom(ctx, pgx.Identifier{"job_tasks"}, []string{"run_id", "task_index", "status", "instance_id", "lease_token", "lease_expires_at"}, pgx.CopyFromRows([][]any{{run.ID, int32(1), "claimed", instance.ID, token, time.Now().Add(time.Hour)}}))
	var check *pgconn.PgError
	if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "financial_compute_job_owner_capable" {
		t.Fatalf("legacy owner received physical work: %v", err)
	}
	if _, err := store.JobTaskGet(ctx, run.ID, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected claim persisted: %v", err)
	}
	if b := f.balance(t); b.AllowanceReserved != 0 || b.ReservedMillicents != 0 || b.UnconfirmedLeases != 0 {
		t.Fatalf("legacy rejection mutated funding: %+v", b)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, owner.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, instance.ID, token, time.Now().Add(time.Hour), node.ID); err != nil {
		t.Fatalf("current owner claim: %v", err)
	}
	// A legacy restart preserves the stored proof but changes incarnation. The
	// CHECK must fail before the downgraded owner can open native admission.
	next := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	// RegisterComputeNodeRuntimeIdentity must advance first, as real startup does.
	owner.Incarnation = uuid.UUID(next.Bytes).String()
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	err = sqlc.New().FinancialComputeOwnerPut(ctx, pool, sqlc.FinancialComputeOwnerPutParams{NodeID: nodeID, Incarnation: next, AccountComputeIncarnation: next})
	check = nil
	if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "financial_compute_job_owner_incarnation" {
		t.Fatalf("downgrade bypassed job attestation: %v", err)
	}
}
