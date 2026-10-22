package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type drainPlanInstanceSpec struct {
	scope, mode, lifecycle string
}

// execLegacyInstanceShape fabricates an instance shape that the application
// standard runtime guard now refuses: a wake instance's kind and artifact are
// immutable. The drain plan must still classify such legacy rows, so the
// guard is disabled only inside this fixture transaction.
func execLegacyInstanceShape(t *testing.T, s *PostgresStore, sql string, args ...any) error {
	t.Helper()
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, statement := range []string{`ALTER TABLE instances DISABLE TRIGGER application_standard_a_runtime_guard`, sql,
		`ALTER TABLE instances ENABLE TRIGGER application_standard_a_runtime_guard`} {
		statementArgs := []any(nil)
		if statement == sql {
			statementArgs = args
		}
		if _, err := tx.Exec(t.Context(), statement, statementArgs...); err != nil {
			return err
		}
	}
	return tx.Commit(t.Context())
}

func cutoverDrainPlanFixture(t *testing.T, specs ...drainPlanInstanceSpec) (*PostgresStore, *state.PgStore, Cutover, state.ComputeNode, []state.Instance) {
	t.Helper()
	s, ps, c, node := admissionCutoverFixture(t)
	if _, err := s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, node.ID); err != nil {
		t.Fatal(err)
	}
	instances := make([]state.Instance, 0, len(specs))
	for _, spec := range specs {
		d, err := ps.CreateDeployment(t.Context(), state.Deployment{AppID: c.AppID, Scope: spec.scope, Kind: state.DeploymentKindImage, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		ins, err := ps.CreateInstanceWithMode(t.Context(), c.AppID, d.ID, spec.lifecycle, 256, node.ID, "", spec.mode)
		if err != nil {
			t.Fatal(err)
		}
		instances = append(instances, ins)
	}
	return s, ps, c, node, instances
}

func fenceDrainPlan(t *testing.T, s *PostgresStore, ps *state.PgStore, c Cutover, owner string) state.ManagedPostgresDrainPlan {
	t.Helper()
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPostgresCutoverDrainPlanCapturesEveryDeploymentScopeAndMode(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t,
		drainPlanInstanceSpec{"default", "normal", "running"},
		drainPlanInstanceSpec{"production", "worker", "running"},
		drainPlanInstanceSpec{"staging", "job", "cold_booting"},
		drainPlanInstanceSpec{"preview", "mirror", "warm"},
		drainPlanInstanceSpec{"service", "service", "waking"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	if plan.State != state.ManagedPostgresDrainPlanDraining || plan.InventoryCount != len(instances) || plan.Pending != len(instances) || len(plan.Blockers) != 0 {
		t.Fatal("partial inventory accepted", plan)
	}
	for _, ins := range instances {
		index := slices.IndexFunc(plan.Members, func(m state.ManagedPostgresDrainPlanMember) bool { return m.InstanceID == ins.ID })
		if index < 0 || plan.Members[index].Drain == nil || plan.Members[index].State != "pending" {
			t.Fatal("scope or mode omitted", ins, plan)
		}
	}
	// Restart/retry preserves the exact membership and existing native frames.
	restarted := state.NewPgStore(s.pool)
	again, err := restarted.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID)
	if err != nil || again.Frame.ID != plan.Frame.ID || len(again.Members) != len(plan.Members) {
		t.Fatal("retry recaptured membership", again, err)
	}
	for i, member := range again.Members {
		if !member.Drain.Frame.Equal(plan.Members[i].Drain.Frame) {
			t.Fatal("retry changed original drain", member)
		}
	}
	rows, err := restarted.ListPendingManagedPostgresDrains(t.Context(), node.ID, "", api.ManagedPostgresCutoverDrainBatchMax)
	if err != nil || len(rows) != len(instances) {
		t.Fatal("plan did not enter the recovery queue", rows, err)
	}
	for _, member := range plan.Members {
		if err := restarted.CompleteManagedPostgresDrain(t.Context(), member.Drain.Frame, cutoverDrainProof(member.Drain.Frame)); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := restarted.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || retired.State != state.ManagedPostgresDrainPlanCatalogRetired || retired.Retired != len(instances) || retired.Pending != 0 {
		t.Fatal("matching receipts did not retire the catalog", retired, err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_drain_plans SET inventory_count=1 WHERE id=$1`, plan.Frame.ID); err == nil {
		t.Fatal("plan rewritten")
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM managed_postgres_cutover_drain_plan_members WHERE plan_id=$1`, plan.Frame.ID); err == nil {
		t.Fatal("member evidence erased")
	}
}

func TestPostgresCutoverDrainPlanUnsupportedMembersStayVisible(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t,
		drainPlanInstanceSpec{"default", "normal", "parked"},
		drainPlanInstanceSpec{"pending", "job", "pending"},
		drainPlanInstanceSpec{"migrated", "worker", "running"},
		drainPlanInstanceSpec{"build", "normal", "running"},
		drainPlanInstanceSpec{"missing", "normal", "running"})
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET migrated_from_node_id=node_id WHERE id=$1`, instances[2].ID); err != nil {
		t.Fatal(err)
	}
	if err := execLegacyInstanceShape(t, s, `UPDATE instances SET kind='build' WHERE id=$1`, instances[3].ID); err != nil {
		t.Fatal(err)
	}
	if err := execLegacyInstanceShape(t, s, `UPDATE instances SET deployment_id=NULL WHERE id=$1`, instances[4].ID); err != nil {
		t.Fatal(err)
	}
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	want := map[string]string{instances[0].ID: "parked_instance_unproven", instances[1].ID: "undispatched_instance_unproven",
		instances[2].ID: "migration_lineage_unproven", instances[3].ID: "unsupported_instance_kind", instances[4].ID: "instance_identity_incomplete"}
	if plan.State != state.ManagedPostgresDrainPlanBlocked || plan.Blocked != len(instances) || plan.Pending != 0 || len(plan.Members) != len(instances) {
		t.Fatal("unsupported members omitted", plan)
	}
	for _, member := range plan.Members {
		if member.Drain != nil || member.BlockerCode != want[member.InstanceID] {
			t.Fatal("unsupported member queued or mislabeled", member)
		}
	}
	rows, err := ps.ListPendingManagedPostgresDrains(t.Context(), node.ID, "", api.ManagedPostgresCutoverDrainBatchMax)
	if err != nil || len(rows) != 0 {
		t.Fatal("unsupported members reached native recovery", rows, err)
	}
}

func TestPostgresCutoverDrainPlanTerminalRowsAndCatalogAbsenceAreNotReceipts(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	ins := instances[0]
	if _, err := s.pool.Exec(t.Context(), `UPDATE instances SET state='stopped',terminal_at=clock_timestamp() WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanDraining || current.Pending != 1 || current.Retired != 0 {
		t.Fatal("terminal row fabricated retirement", current, err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, ins.ID); err == nil {
		t.Fatal("unconfirmed terminal retention hid the member")
	}
	// Simulate a legacy retention gap that bypassed the new guard. The plan
	// still owns its original member; an empty live catalog is never green.
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE instances DISABLE TRIGGER managed_postgres_drain_plan_inventory_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE instances ENABLE TRIGGER managed_postgres_drain_plan_inventory_guard`); err != nil {
		t.Fatal(err)
	}
	current, err = ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanBlocked || current.Blocked != 1 || current.Members[0].BlockerCode != "instance_missing" {
		t.Fatal("missing row fabricated receipt", current, err)
	}
}

func TestPostgresCutoverDrainPlanFencesLateMembershipAndAllowsConfirmedRetention(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	for _, lifecycle := range []string{"parked", "failed", "stopped", "running"} {
		if _, err := ps.CreateInstance(t.Context(), c.AppID, instances[0].DeploymentID, lifecycle, 256, node.ID, ""); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
			t.Fatal("late instance membership did not return the typed fence", lifecycle, err)
		}
	}
	for _, mutation := range []string{
		`UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE instances SET deployment_id=NULL WHERE id=$1`,
		`UPDATE instances SET mode='job' WHERE id=$1`,
		`UPDATE instances SET state='migrating' WHERE id=$1`,
		`UPDATE instances SET migrated_from_node_id=node_id WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(t.Context(), mutation, instances[0].ID); err == nil {
			t.Fatal("planned incarnation changed", mutation)
		}
	}
	if _, err := s.pool.Exec(t.Context(), `TRUNCATE instances CASCADE`); err == nil {
		t.Fatal("truncate erased the planned inventory")
	}
	f := plan.Members[0].Drain.Frame
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, cutoverDrainProof(f)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, instances[0].ID); err != nil {
		t.Fatal("confirmed receipt blocked normal retention", err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanCatalogRetired || current.Retired != 1 {
		t.Fatal("retention erased confirmed evidence", current, err)
	}
}

func TestPostgresCutoverDrainPlanFailedCaptureCommitsNoPartialQueue(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION reject_test_plan_member() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected member write failure'; END $$; CREATE TRIGGER reject_test_plan_member BEFORE INSERT ON managed_postgres_cutover_drain_plan_members FOR EACH ROW EXECUTE FUNCTION reject_test_plan_member()`); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID); err == nil {
		t.Fatal("capture write failure ignored")
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM managed_postgres_cutover_drain_plans)+(SELECT count(*) FROM managed_postgres_cutover_drains)+(SELECT count(*) FROM managed_postgres_cutover_drain_plan_members)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial plan or native work survived rollback", count, err)
	}
	if _, err := s.pool.Exec(t.Context(), `DROP FUNCTION reject_test_plan_member() CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID); err != nil {
		t.Fatal("capture could not recover after rollback", err)
	}
}

func TestPostgresCutoverDrainPlanRecoveryPreservesLostReplyAndUnreachableMembers(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"}, drainPlanInstanceSpec{"staging", "worker", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	restarted := state.NewPgStore(s.pool)
	first, second := plan.Members[0].Drain.Frame, plan.Members[1].Drain.Frame
	if _, err := restarted.WithManagedPostgresDrainAuthority(t.Context(), first, func(context.Context) (state.ManagedPostgresDrainProof, error) {
		return state.ManagedPostgresDrainProof{}, context.DeadlineExceeded
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unreachable host accepted", err)
	}
	proof := cutoverDrainProof(second)
	if _, err := restarted.WithManagedPostgresDrainAuthority(t.Context(), second, func(context.Context) (state.ManagedPostgresDrainProof, error) { return proof, nil }); err != nil {
		t.Fatal(err)
	}
	// The native reply is lost before receipt commit. The original queue item
	// survives restart, and the other member cannot conceal that uncertainty.
	rows, err := restarted.ListPendingManagedPostgresDrains(t.Context(), node.ID, "", api.ManagedPostgresCutoverDrainBatchMax)
	if err != nil || len(rows) != 2 {
		t.Fatal("lost response erased work", rows, err)
	}
	if err := restarted.CompleteManagedPostgresDrain(t.Context(), second, proof); err != nil {
		t.Fatal(err)
	}
	current, err := restarted.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.Retired != 1 || current.Pending != 1 || current.State != state.ManagedPostgresDrainPlanDraining {
		t.Fatal("one receipt authorized the entire plan", current, err)
	}
	if err := restarted.CompleteManagedPostgresDrain(t.Context(), first, cutoverDrainProof(first)); err != nil {
		t.Fatal(err)
	}
	current, err = restarted.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanCatalogRetired {
		t.Fatal("original plan could not converge", current, err)
	}
}

func TestPostgresCutoverDrainPlanOwnershipABAInvalidatesEvenRetiredEvidence(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	if _, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unfenced plan accepted", err)
	}
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	f := plan.Members[0].Drain.Frame
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, cutoverDrainProof(f)); err != nil {
		t.Fatal(err)
	}
	other, err := ps.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, node.ID); err != nil {
		t.Fatal(err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanBlocked || !slices.Contains(current.Blockers, "owner_revision_changed") {
		t.Fatal("ownership ABA reused earlier completion", current, err)
	}
}

func TestPostgresCutoverDrainPlanCancellationReleasesCatalogFence(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	if _, err := s.CancelCutover(t.Context(), c.AccountID, c.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("new plan selected during cancellation", err)
	}
	for _, member := range c.Credentials {
		claim, err := s.ClaimCutover(t.Context(), c.AccountID, c.ID, uuid.NewString(), time.Now(), time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeCutoverCredential(t.Context(), claim, member, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ps.CreateInstance(t.Context(), c.AppID, instances[0].DeploymentID, "parked", 256, node.ID, ""); err != nil {
		t.Fatal("cancelled plan retained the catalog lock", err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanBlocked || !slices.Contains(current.Blockers, "fence_changed") {
		t.Fatal("cancelled plan retained completion authority", current, err)
	}
}

func TestPostgresCutoverDrainPlanEmptyAndOversizedCatalogsQueueNothing(t *testing.T) {
	for _, size := range []int{0, api.ManagedPostgresCutoverDrainPlanMembersMax + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			s, ps, c, node, _ := cutoverDrainPlanFixture(t)
			if size > 0 {
				d, err := ps.CreateDeployment(t.Context(), state.Deployment{AppID: c.AppID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.pool.Exec(t.Context(), `INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) SELECT $1,$2,'running',256,$3 FROM generate_series(1,$4::int)`, c.AppID, d.ID, node.ID, size); err != nil {
					t.Fatal(err)
				}
			}
			plan := fenceDrainPlan(t, s, ps, c, node.ID)
			code := "inventory_empty"
			if size > 0 {
				code = "inventory_limit_exceeded"
			}
			if plan.State != state.ManagedPostgresDrainPlanBlocked || plan.InventoryCount != size || len(plan.Members) != 0 || !slices.Contains(plan.Blockers, code) {
				t.Fatal("partial or absent inventory became green", plan)
			}
			rows, err := ps.ListPendingManagedPostgresDrains(t.Context(), node.ID, "", api.ManagedPostgresCutoverDrainBatchMax)
			if err != nil || len(rows) != 0 {
				t.Fatal("incomplete inventory queued partial effects", rows, err)
			}
		})
	}
}

func TestPostgresCutoverDrainPlanStaleTransactionCannotBypassMembershipGuard(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	var count int
	if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM managed_postgres_cutover_drain_plans`).Scan(&count); err != nil || count != 0 {
		t.Fatal("could not establish pre-plan snapshot", count, err)
	}
	if _, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	// Exercise this domain's own guard independently of other instance triggers.
	// The app row did not change during plan capture, so a row lock alone cannot
	// make a repeatable-read transaction see the newly committed plan.
	if _, err := tx.Exec(t.Context(), `SELECT current_managed_postgres_drain_plan($1)`, c.AppID); err == nil {
		t.Fatal("stale snapshot hid a committed drain plan")
	}
}

func TestPostgresCutoverDrainPlanStaleTruncateCannotBypassMembershipGuard(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
			if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
				t.Fatal(err)
			}
			tx, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			var count int
			if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM managed_postgres_cutover_drain_plans`).Scan(&count); err != nil || count != 0 {
				t.Fatal("could not establish pre-plan snapshot", count, err)
			}
			plan, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), `TRUNCATE instances CASCADE`)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "40001" {
				t.Fatal("stale truncate bypassed current-snapshot guard", err)
			}
			_ = tx.Rollback(t.Context())
			current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
			if err != nil || current.Pending != 1 || current.Members[0].BlockerCode != "native_retirement_unconfirmed" {
				t.Fatal("rejected truncate damaged the captured incarnation", current, err)
			}
		})
	}
}

func TestPostgresCutoverDrainPlanConcurrentCaptureRejectsWaitingLateInsert(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, c.AppID); err != nil {
		t.Fatal(err)
	}
	planned := make(chan error, 1)
	go func() {
		_, err := ps.ReserveManagedPostgresDrainPlan(ctx, c.ID, node.ID)
		planned <- err
	}()
	waitDrainPlanDatabaseLocks(t, ctx, s, 1)
	inserted := make(chan error, 1)
	go func() {
		_, err := ps.CreateInstance(ctx, c.AppID, instances[0].DeploymentID, "parked", 256, node.ID, "")
		inserted <- err
	}()
	waitDrainPlanDatabaseLocks(t, ctx, s, 2)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-planned; err != nil {
		t.Fatal("capture failed", err)
	}
	if err := <-inserted; err == nil {
		t.Fatal("late insert used its pre-capture statement snapshot")
	}
	plan, err := ps.ReserveManagedPostgresDrainPlan(ctx, c.ID, node.ID)
	if err != nil || plan.InventoryCount != 1 || len(plan.Members) != 1 || plan.Members[0].InstanceID != instances[0].ID {
		t.Fatal("captured membership changed", plan, err)
	}
}

func waitDrainPlanDatabaseLocks(t *testing.T, ctx context.Context, s *PostgresStore, want int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("did not observe the competing database lock", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresCutoverDrainPlanConcurrentRequestsKeepOneOriginalPlan(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		plan state.ManagedPostgresDrainPlan
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			plan, err := state.NewPgStore(s.pool).ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID)
			results <- result{plan, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.plan.Frame.ID != second.plan.Frame.ID ||
		!first.plan.Members[0].Drain.Frame.Equal(second.plan.Members[0].Drain.Frame) {
		t.Fatal("concurrent requests replaced the plan or native identity", first, second)
	}
	rows, err := ps.ListPendingManagedPostgresDrains(t.Context(), node.ID, "", api.ManagedPostgresCutoverDrainBatchMax)
	if err != nil || len(rows) != 1 {
		t.Fatal("duplicate native work queued", rows, err)
	}
}

func TestPostgresCutoverDrainPlanMissingMembershipNeverBecomesEmptySuccess(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	// Simulate legacy/corrupt evidence independently of the live retention guard.
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE managed_postgres_cutover_drain_plan_members DISABLE TRIGGER managed_postgres_drain_plan_member_evidence_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM managed_postgres_cutover_drain_plan_members WHERE plan_id=$1`, plan.Frame.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE managed_postgres_cutover_drain_plan_members ENABLE TRIGGER managed_postgres_drain_plan_member_evidence_guard`); err != nil {
		t.Fatal(err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanBlocked || !slices.Contains(current.Blockers, "membership_incomplete") || !slices.Contains(current.Blockers, "inventory_changed") {
		t.Fatal("absent membership became a successful empty loop", current, err)
	}
}

func TestPostgresCutoverDrainPlanMalformedReceiptRemainsVisibleBlocker(t *testing.T) {
	s, ps, c, node, _ := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	// Simulate a corrupt retained receipt whose timestamp cannot represent a
	// native authority frame. No physical effects are invoked by inspection.
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE managed_postgres_cutover_drains DISABLE TRIGGER managed_postgres_cutover_drain_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_drains SET fenced_at='infinity' WHERE id=$1`, plan.Members[0].Drain.Frame.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE managed_postgres_cutover_drains ENABLE TRIGGER managed_postgres_cutover_drain_guard`); err != nil {
		t.Fatal(err)
	}
	current, err := ps.ManagedPostgresDrainPlan(t.Context(), plan.Frame.ID)
	if err != nil || current.State != state.ManagedPostgresDrainPlanBlocked || current.Blocked != 1 || current.Members[0].BlockerCode != "receipt_invalid" || current.Members[0].Drain != nil {
		t.Fatal("malformed receipt concealed or accepted", current, err)
	}
}
