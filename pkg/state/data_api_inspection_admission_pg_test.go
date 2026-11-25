//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPIInspectionAdmissionFencePG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, deploymentID := seedLiveDeploy(t, store, ctx, uuid.NewString()[:8], uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	databaseID, bindingID, resourceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,'inspection','eu',17,'development','single_zone','native',$3)`, databaseID, accountID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='ready',provider_resource_id=$2,observed_generation=desired_generation WHERE id=$1`, databaseID, resourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'default','DATABASE_URL','data_api')`, bindingID, accountID, databaseID, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='ready',provider_identity_id='login',credential_ref='ref' WHERE id=$1`, bindingID); err != nil {
		t.Fatal(err)
	}
	// A newer deployment that never served traffic must not replace the live target.
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", TrafficPercentExplicit: true, ImageDigest: "sha256:" + strings.Repeat("2", 64), CreatedAt: time.Now().UTC().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, newer.ID, state.DeployFailed, ""); err != nil {
		t.Fatal(err)
	}
	var bindingTime, databaseTime time.Time
	if err := pool.QueryRow(ctx, `SELECT b.updated_at,d.updated_at FROM managed_postgres_bindings b JOIN managed_postgres_databases d ON d.id=b.database_id WHERE b.id=$1`, bindingID).Scan(&bindingTime, &databaseTime); err != nil {
		t.Fatal(err)
	}
	fence, err := store.ReadDataAPIInspectionFence(ctx, accountID, appID, deploymentID, databaseID, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	input := state.DataAPIInspectionAdmission{AccountID: accountID, AppID: appID, DeploymentID: deploymentID, DatabaseID: databaseID, BindingID: bindingID, Table: "notes", Nonce: strings.Repeat("a", 64), TargetDigest: strings.Repeat("b", 64), TrustDigest: strings.Repeat("c", 64), CredentialGeneration: 1, CredentialRef: "ref", ProviderIdentityID: "login", DatabaseResourceID: resourceID, BackendID: "native", BackendFingerprint: strings.Repeat("a", 64), BindingUpdatedAt: bindingTime, DatabaseUpdatedAt: databaseTime, DatabaseDesiredGeneration: 1, DatabaseObservedGeneration: 1, Fence: fence, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	// An update followed by a revert must still invalidate the captured fence,
	// even if a writer leaves updated_at unchanged.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET credential_ref='other' WHERE id=$1`, bindingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET credential_ref='ref' WHERE id=$1`, bindingID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitDataAPIInspection(ctx, input); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reverted binding accepted: %v", err)
	}
	input.Fence, err = store.ReadDataAPIInspectionFence(ctx, accountID, appID, deploymentID, databaseID, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.AdmitDataAPIInspection(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.ReadDataAPIInspectionRecord(ctx, accountID, appID, id)
	if err != nil || !record.Current || record.TaskID != id || record.BindingID != bindingID || record.ProtocolVersion != 0 {
		t.Fatalf("queued record: %+v %v", record, err)
	}
	if _, err := store.ReadDataAPIInspectionRecord(ctx, uuid.NewString(), appID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account record: %v", err)
	}
	now := input.CreatedAt
	task, err := store.ClaimNextAppTask(ctx, "inspection-admission-test", now, time.Minute)
	if err != nil || task.ID != id || task.LeaseToken == nil {
		t.Fatalf("claim: %+v %v", task, err)
	}
	if _, err = store.MarkAppTaskRunning(ctx, id, *task.LeaseToken, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err = store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: id, LeaseToken: *task.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, StdoutTail: "observation", FinishedAt: now.Add(2 * time.Second), DataAPIInspectionProtocol: apptaskproto.DataAPIInspectionVersion}); err != nil {
		t.Fatal(err)
	}
	record, err = store.ReadDataAPIInspectionRecord(ctx, accountID, appID, id)
	if err != nil || !record.Current || record.ProtocolVersion != apptaskproto.DataAPIInspectionVersion || len(record.StdoutSHA256) != 32 {
		t.Fatalf("completed record: %+v %v", record, err)
	}
	for _, statement := range []string{
		`UPDATE data_api_inspection_executions SET protocol_version=5 WHERE task_id=$1`,
		`UPDATE data_api_inspection_admissions SET target_digest=repeat('d',64) WHERE task_id=$1`,
	} {
		_, updateErr := pool.Exec(ctx, statement, id)
		var pgErr *pgconn.PgError
		if !errors.As(updateErr, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("evidence update was not rejected: %v", updateErr)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id='changed' WHERE id=$1`, databaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id=NULL WHERE id=$1`, databaseID); err != nil {
		t.Fatal(err)
	}
	record, err = store.ReadDataAPIInspectionRecord(ctx, accountID, appID, id)
	if err != nil || record.Current {
		t.Fatalf("reverted database accepted: %+v %v", record, err)
	}
}
