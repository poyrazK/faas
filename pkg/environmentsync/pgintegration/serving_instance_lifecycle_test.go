// adr: 20261103210000
package pgintegration_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func servingInstancePublication(t *testing.T, basic gitOpsTestStore, app state.App, deployment state.Deployment, initial state.State) state.RuntimeInstancePublication {
	t.Helper()
	ctx := t.Context()
	placement := qualificationPlacement(t, basic, 4096)
	instance, err := basic.CreateInstanceWithMode(ctx, app.ID, deployment.ID, string(initial), app.RAMMB,
		placement.NodeID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if instance.Mode != string(state.InstanceModeNormal) {
		t.Fatalf("unexpected serving mode: %q", instance.Mode)
	}
	if _, err := basic.(state.EnvironmentQualificationExecutionStore).EnvironmentQualificationExecution(ctx, instance.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ordinary serving instance has a qualification execution: %v", err)
	}
	boundary := time.Now().UTC().Truncate(time.Microsecond)
	snapshot, err := basic.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: snapshot.Scope, Boundary: boundary, Variables: map[string]string{},
		SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
	for _, row := range snapshot.Values {
		inputs.Variables[row.Key] = row.Value
	}
	eligible, err := state.SelectAppSecretsForDelivery(snapshot.Secrets, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range eligible {
		inputs.SecretVersions[row.Scope+"/"+row.Key] = row.DeliveryVersion
		inputs.SecretRefs[row.Key] = "secret:" + row.Key
	}
	return state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: instance.ID, NodeID: instance.NodeID,
		WakeID: instance.WakeID, ExpectedState: instance.State, Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008,
		Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs}
}

func TestEnvironmentGitOpsServingInstancesPublishRetireAndCollect(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, desired := serveRuntimeConvergenceGraph(t, basic)
		ctx := t.Context()
		release, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil || len(release.Members) == 0 {
			t.Fatal("active release unavailable", err)
		}
		deployment, err := basic.DeploymentByID(ctx, release.Members[0].DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status != state.DeployLive || deployment.EnvironmentWorkloadHeld() || !deployment.EnvironmentWorkloadManaged() {
			t.Fatal("expected an activated deployment with retained frozen inputs")
		}
		app, err := basic.AppByID(ctx, deployment.AppID)
		if err != nil {
			t.Fatal(err)
		}
		for _, initial := range []state.State{state.StateColdBooting, state.StateWaking} {
			for _, terminal := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
				t.Run(string(initial)+"/"+string(terminal), func(t *testing.T) {
					publication := servingInstancePublication(t, basic, app, deployment, initial)
					instance, err := basic.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(t.Context(), publication)
					if err != nil || instance.State != string(state.StateRunning) {
						t.Fatalf("ordinary serving runtime publication: %s %v", instance.State, err)
					}
					receipts := basic.(state.RuntimeConfigReceiptStore)
					inputs, exists, err := receipts.InstanceRuntimeConfigReceipt(t.Context(), instance.ID)
					if err != nil || !exists || inputs.Scope != publication.Inputs.Scope || !inputs.Boundary.Equal(publication.Inputs.Boundary) {
						t.Fatalf("publication lost its runtime receipt: %t %v", exists, err)
					}
					if fresh, err := receipts.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs); err != nil || !fresh {
						t.Fatalf("published runtime inputs are not current: %t %v", fresh, err)
					}
					assertWorkloadRuntimeConvergence(t, basic, lease, desired, true)
					if terminal == state.StateParked {
						err = basic.UpdateInstanceStateIf(t.Context(), instance.ID, string(state.StateRunning), string(terminal))
					} else {
						err = basic.UpdateInstanceStateToTerminal(t.Context(), instance.ID, string(terminal), time.Now().UTC())
					}
					if err != nil {
						t.Fatalf("ordinary serving %s: %v", terminal, err)
					}
					if current, err := basic.InstanceByID(t.Context(), instance.ID); err != nil || current.State != string(terminal) {
						t.Fatalf("retirement did not persist: %s %v", current.State, err)
					}
					if err := basic.DeleteInstance(t.Context(), instance.ID); err != nil {
						t.Fatalf("ordinary serving collection: %v", err)
					}
					assertWorkloadRuntimeConvergence(t, basic, lease, desired, true)
				})
			}
		}
	})
}

func TestPgEnvironmentGitOpsServingLifecycleMigrationRepairsExistingInstances(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, _ := serveRuntimeConvergenceGraph(t, store)
	ctx := t.Context()
	release, err := store.ActiveProjectReleaseSet(ctx, lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil || len(release.Members) == 0 {
		t.Fatal("active release unavailable", err)
	}
	deployment, err := store.DeploymentByID(ctx, release.Members[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.AppByID(ctx, deployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	publication := servingInstancePublication(t, store, app, deployment, state.StateWaking)
	// Reconstruct the two old guards around an already admitted ordinary VM.
	for _, old := range []struct{ file, function string }{
		{"20261006173000001_environment_qualification_restore_runtime_receipts.sql", "guard_environment_qualification_runtime_receipt"},
		{"20261003214107904_environment_qualification_execution_retirement.sql", "guard_environment_qualification_physical_retirement"},
	} {
		raw, err := migrations.FS.ReadFile(old.file)
		if err != nil {
			t.Fatal(err)
		}
		definition := strings.SplitN(string(raw), "CREATE OR REPLACE FUNCTION "+old.function+"()", 2)[1]
		definition = "CREATE OR REPLACE FUNCTION " + old.function + "()" + strings.SplitN(definition, "-- +goose StatementEnd", 2)[0]
		if _, err := pool.Exec(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, publication); err == nil {
		t.Fatal("old receipt guard did not reproduce the wake blocker")
	}
	if err := store.UpdateInstanceStateToTerminal(ctx, publication.InstanceID, string(state.StateFailed), time.Now()); err == nil {
		t.Fatal("old retirement guard did not reproduce the cleanup blocker")
	}
	raw, err := migrations.FS.ReadFile("20261103210000001_gitops_serving_instance_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Replay against populated instances and retained qualification history.
	for range 2 {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[0]); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, publication); err != nil {
		t.Fatal("migration did not repair the existing wake", err)
	}
	if err := store.UpdateInstanceStateToTerminal(ctx, publication.InstanceID, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal("migration did not repair existing instance retirement", err)
	}
	if err := store.DeleteInstance(ctx, publication.InstanceID); err != nil {
		t.Fatal("migration did not repair existing instance collection", err)
	}
}

func TestPgEnvironmentGitOpsLegacyQualificationWithoutExecutionStaysFenced(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	claimed, _, runtime := qualificationRuntimeFixture(t, store)
	instance, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct an uncertain legacy VM with neither its frame nor request.
	// This is owner-only fixture setup; ordinary writers cannot discard that
	// identity or its physical debt. The held candidate must still fence it.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, statement := range []string{
		`ALTER TABLE environment_qualification_executions DISABLE TRIGGER guard_environment_qualification_execution`,
		`DELETE FROM environment_qualification_executions WHERE instance_id=$1`,
		`ALTER TABLE environment_qualification_executions ENABLE TRIGGER guard_environment_qualification_execution`,
		`ALTER TABLE environment_workload_qualification_requests DISABLE TRIGGER guard_environment_workload_qualification_request`,
		`DELETE FROM environment_workload_qualification_requests WHERE reserved_instance_id=$1`,
		`ALTER TABLE environment_workload_qualification_requests ENABLE TRIGGER guard_environment_workload_qualification_request`,
	} {
		var args []any
		if strings.Contains(statement, "$1") {
			args = append(args, instance.ID)
		}
		if _, err := tx.Exec(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), instance.ID, instance.WakeID, runtime.Inputs); err == nil {
		t.Fatal("legacy qualification accepted an ordinary runtime acknowledgement")
	}
	for _, terminal := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
		if err := store.UpdateInstanceStateToTerminal(t.Context(), instance.ID, string(terminal), time.Now()); err == nil || !strings.Contains(err.Error(), "legacy qualification") {
			t.Fatalf("legacy reservation lost its retirement fence for %s: %v", terminal, err)
		}
	}
	if err := store.DeleteInstance(t.Context(), instance.ID); err == nil || !strings.Contains(err.Error(), "legacy qualification") {
		t.Fatalf("legacy reservation lost its collection fence: %v", err)
	}
}
