//go:build !no_pg

package state

// adr: 595 Ledger repair preserves the actual measured serving history.

import (
	"errors"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPgApplicationStandardLedgerRecoveryPreservesMeasuredPromotion(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("ledger recovery requires PostgreSQL 16 pg_dump")
	}
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	s, pool := runtimeCapturePGStore(t)
	f, parent := standardPausedRestoreFixture(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	r := standardMeasuredPromotionReceipt(t, p)
	actual, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || actual.State != string(StateRunning) {
		t.Fatal("publish measured serving receipt", err)
	}
	before := measuredPromotionRecoveryHistory(t, pool, actual.AppID)
	versions := measuredPromotionRecoveryVersions(t)
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=ANY($1)", versions); err != nil {
		t.Fatal(err)
	}
	var drift *db.SchemaDriftError
	if err := db.MigrateUp(t.Context(), pool); !errors.As(err, &drift) {
		t.Fatal("ordinary startup must refuse the missing frozen ledger", err)
	}
	plan, err := db.PreviewApplicationStandardLedgerRecovery(t.Context(), pool)
	if err != nil {
		t.Fatal("review expanded schema with measured runtime", err)
	}
	if len(plan.Remaining) != 0 || len(plan.Repair) != len(versions) {
		t.Fatal("review omitted a frozen standards migration")
	}
	if before != measuredPromotionRecoveryHistory(t, pool, actual.AppID) {
		t.Fatal("review changed the serving runtime or inherited configuration")
	}
	receipt, err := db.ApplyApplicationStandardLedgerRecovery(t.Context(), pool, plan.ApprovalHash)
	if err != nil || !slices.Equal(receipt.RepairedVersions, versions) {
		t.Fatal("reviewed repair did not restore the complete frozen ledger", err)
	}
	retry, err := db.ApplyApplicationStandardLedgerRecovery(t.Context(), pool, plan.ApprovalHash)
	if err != nil || !reflect.DeepEqual(retry, receipt) {
		t.Fatal("repair retry created another recovery event", err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("ordinary upgrade after reviewed repair", err)
	}
	if before != measuredPromotionRecoveryHistory(t, pool, actual.AppID) {
		t.Fatal("recovery rewrote native history, residency or inherited settings")
	}
	got, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), actual.ID)
	if err != nil || !got.Equal(r) {
		t.Fatal("recovery lost the actual promoted receipt", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), actual.State, StateRunning, r); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("ledger repair granted fresh boot authority to a promotion", err)
	}
	if before != measuredPromotionRecoveryHistory(t, pool, actual.AppID) {
		t.Fatal("refused boot changed the recovered serving history")
	}
	if !standardSnapshotGet(t, s, f.Grant).Acknowledgment.Capture.Equal(f.Evidence.Capture) {
		t.Fatal("recovery changed the original catalog lineage")
	}
}

func measuredPromotionRecoveryVersions(t *testing.T) []int64 {
	t.Helper()
	approved, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		t.Fatal(err)
	}
	all, err := migrations.Sources()
	if err != nil {
		t.Fatal(err)
	}
	wanted := make(map[int64]bool, len(approved))
	for version := range approved {
		wanted[version] = true
	}
	for _, source := range all {
		// Also remove newly added standards entries, even if the reviewed
		// manifest forgot them. The audit migration has its separate prepare
		// operation and must exist before recovery can record a receipt.
		if strings.Contains(source.Filename, "_application_standard_") && source.Version != 20261002015945515 {
			wanted[source.Version] = true
		}
	}
	versions := make([]int64, 0, len(wanted))
	for version := range wanted {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions
}

func measuredPromotionRecoveryHistory(t *testing.T, pool *pgxpool.Pool, appID string) string {
	t.Helper()
	var history string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'app',(SELECT to_jsonb(a) FROM apps a WHERE id=$1),
 'enrollment',(SELECT to_jsonb(e) FROM app_application_standards e WHERE app_id=$1),
 'instances',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM instances i WHERE app_id=$1),
 'admissions',(SELECT jsonb_agg(to_jsonb(a) ORDER BY instance_id) FROM instance_application_standard_admissions a JOIN instances i ON i.id=a.instance_id WHERE i.app_id=$1),
 'boots',(SELECT jsonb_agg(to_jsonb(b) ORDER BY token) FROM instance_application_standard_boots b JOIN instances i ON i.id=b.instance_id WHERE i.app_id=$1),
 'promotions',(SELECT jsonb_agg(to_jsonb(p) ORDER BY token) FROM instance_application_standard_promotions p JOIN instances i ON i.id=p.instance_id WHERE i.app_id=$1),
 'captures',(SELECT jsonb_agg(to_jsonb(c) ORDER BY token) FROM application_standard_snapshot_captures c WHERE app_id=$1),
 'snapshots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM snapshots s JOIN deployments d ON d.id=s.deployment_id WHERE d.app_id=$1))::text`, appID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	return history
}
