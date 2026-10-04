package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingApplicationAdoptionMem(t *testing.T) {
	bindingApplicationAdoptionSuite(t, state.NewMemStore())
}
func TestBindingApplicationAdoptionPG(t *testing.T) {
	store, _ := pgStore(t)
	bindingApplicationAdoptionSuite(t, store)
}

func bindingApplicationAdoptionSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	nodeID := bindingAdoptionNode(t, store, ctx)
	credential := seedInventoryStorageBinding(t, store, acct.ID, app.ID, "default")
	seedInventoryStorageBinding(t, store, acct.ID, app.ID, "staging")
	keys := api.BindingCredentialSecretKeys(api.BindingTypeObjectStorage, "GREGALE_S3_ASSETS")
	selector := state.BindingAdoptionSelector{Type: api.BindingTypeObjectStorage, BindingID: credential.ID, Scope: "default", Keys: keys}
	adoptionStore := store.(state.BindingApplicationAdoptionStore)
	read := func() []state.BindingApplicationAdoptionRow {
		t.Helper()
		rows, err := adoptionStore.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{selector})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if rows := read(); len(rows) != 6 || rows[0].InstanceID != "" {
		t.Fatalf("inactive metadata: %+v", rows)
	}
	if rows, err := adoptionStore.ReadBindingApplicationAdoption(ctx, uuid.NewString(), app.ID, []state.BindingAdoptionSelector{selector}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account: %+v %v", rows, err)
	}
	narrowed := selector
	narrowed.Keys = keys[:1]
	if rows, err := adoptionStore.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{narrowed}); err != nil || len(rows) != 1 || rows[0].Key != keys[0] {
		t.Fatalf("selector keys: %+v %v", rows, err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, candidate.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.CreateInstance(ctx, app.ID, candidate.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 512, nodeID, uuid.NewString(), string(state.InstanceModeMirror))
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := store.CreateInstance(ctx, app.ID, candidate.ID, "stopped", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	sidecarDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("2", 64), OverrideEnvSecrets: json.RawMessage(`{"GREGALE_S3_ASSETS_ACCESS_KEY_ID":"secret:GREGALE_S3_ASSETS_ACCESS_KEY_ID"}`), Sidecars: json.RawMessage(`[{"name":"worker","type":"sidecar","env_secrets":{"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY":"secret:GREGALE_S3_ASSETS_SECRET_ACCESS_KEY","GREGALE_S3_ASSETS_REGION":"literal"}},{"name":"setup","type":"init","env_secrets":{"GREGALE_S3_ASSETS_BUCKET":"secret:GREGALE_S3_ASSETS_BUCKET"}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, sidecarDeployment.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, sidecarDeployment.ID, "worker", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	sidecarRuntime, err := store.CreateInstance(ctx, app.ID, sidecarDeployment.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	otherDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "staging", ImageDigest: "sha256:" + strings.Repeat("3", 64)})
	if err != nil {
		t.Fatal(err)
	}
	otherRuntime, err := store.CreateInstance(ctx, app.ID, otherDeployment.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	rows := read()
	if len(rows) != 8 {
		t.Fatalf("authorized roster: %+v", rows)
	}
	for _, row := range rows {
		if row.InstanceID == mirror.ID || row.InstanceID == stopped.ID || row.InstanceID == otherRuntime.ID {
			t.Fatalf("ineligible resident: %+v", row)
		}
		if row.InstanceID == sidecarRuntime.ID {
			if row.WorkloadName == "worker" && (row.Key != keys[4] || row.ReloadSupport != "enabled") || row.WorkloadName == "" && (row.Key != keys[3] || row.ReloadSupport != "enabled") {
				t.Fatalf("sidecar grants: %+v", row)
			}
		} else if row.InstanceID != runtime.ID || row.ReloadSupport != "enabled" {
			t.Fatalf("main target: %+v", row)
		}
		if row.ApplicationAckVersion != 0 || row.ApplicationAckAt != nil {
			t.Fatalf("fabricated receipt: %+v", row)
		}
	}
	candidates := []state.AppSecretDeliveryCandidate{}
	for _, key := range keys {
		candidates = append(candidates, state.AppSecretDeliveryCandidate{Scope: "default", Key: key, Version: 1})
	}
	reload := state.AppSecretRuntimeReloadResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, AttemptedAt: time.Now().UTC(), Candidates: candidates}
	guarded := store.(state.BindingPromotionStore)
	fence := bindingPromotionFence(t, guarded, acct, app, candidate)
	if count, err := store.RecordAppSecretRuntimeReload(ctx, reload); err != nil || count != 6 {
		t.Fatalf("reload: %d %v", count, err)
	}
	rejectChanged := func(fence state.BindingPromotionFence) {
		t.Helper()
		if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("accepted changed observation: %v", err)
		}
	}
	rejectChanged(fence)
	ack := state.AppSecretRuntimeReloadAckResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, Revision: reload.Revision, Status: state.SecretApplicationReloadAckApplied, AttemptedAt: time.Now().UTC(), Candidates: candidates}
	fence = bindingPromotionFence(t, guarded, acct, app, candidate)
	if count, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil || count != 6 {
		t.Fatalf("ack: %d %v", count, err)
	}
	rejectChanged(fence)
	for _, row := range read() {
		if row.InstanceID == runtime.ID && (row.ApplicationAckVersion != 1 || row.ReloadVersion != 1 || row.ApplicationAck != "applied" || row.ReloadAt == nil || row.ApplicationAckAt == nil) {
			t.Fatalf("independent observations: %+v", row)
		}
	}
	fence = bindingPromotionFence(t, guarded, acct, app, candidate)
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, sidecarDeployment.ID, "worker", ""); err != nil {
		t.Fatal(err)
	}
	rejectChanged(fence)
	rotation := state.ObjectS3CredentialRotationRequest{AccountID: acct.ID, BucketID: credential.BucketID, BindingID: credential.ID, WakeID: uuid.NewString(), AccessKeyID: "GRGABBBBBBBBBBBBBBBB", KID: "age1new", SecretSealed: []byte("PRIVATE_SEALED")}
	for _, key := range keys[3:5] {
		rotation.Secrets = append(rotation.Secrets, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: key, Ciphertext: []byte("PRIVATE_NEW"), Kid: "age1new", ValueHash: "fedcba9876543210", ManagedObjectStorageCredentialID: credential.ID})
	}
	fence = bindingPromotionFence(t, guarded, acct, app, candidate)
	if _, err := store.(state.ObjectS3CredentialRotationStore).StageObjectS3CredentialRotation(ctx, rotation); err != nil {
		t.Fatal(err)
	}
	rejectChanged(fence)
	for _, row := range read() {
		if row.InstanceID == runtime.ID && (row.Key == keys[3] || row.Key == keys[4]) && (row.CurrentVersion != 2 || row.ApplicationAckVersion != 1) {
			t.Fatalf("rotation erased stale evidence: %+v", row)
		}
	}
}

func TestBindingApplicationAdoptionPostgresOwnershipPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	nodeID := bindingAdoptionNode(t, store, ctx)
	databaseID, bindingID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,'primary','eu',17,'development','single_zone','test',$3)`, databaseID, acct.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key) VALUES($1,$2,$3,$4,'default','DATABASE_URL')`, bindingID, acct.ID, databaseID, app.ID); err != nil {
		t.Fatal(err)
	}
	secret := state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: "DATABASE_URL", ManagedPostgresBindingID: bindingID, ManagedCredentialRef: "private-ref", ManagedCredentialGeneration: 1, Kid: "kid", Ciphertext: []byte("private-ciphertext"), ValueHash: "0123456789abcdef"}
	if err := store.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, candidate.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.CreateInstance(ctx, app.ID, candidate.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	selector := state.BindingAdoptionSelector{Type: api.BindingTypePostgres, BindingID: bindingID, Scope: "default", Keys: []string{"DATABASE_URL"}}
	rows, err := store.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{selector})
	if err != nil || len(rows) != 1 || rows[0].CurrentVersion != 1 || rows[0].InstanceID != runtime.ID {
		t.Fatalf("postgres metadata: %+v %v", rows, err)
	}
	selector.Type = api.BindingTypeObjectStorage
	rows, err = store.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{selector})
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-type owner: %+v %v", rows, err)
	}
	selector.Type = api.BindingTypePostgres
	reload := state.AppSecretRuntimeReloadResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, AttemptedAt: time.Now().UTC(), Candidates: []state.AppSecretDeliveryCandidate{{Scope: "default", Key: "DATABASE_URL", Version: 1}}}
	if n, err := store.RecordAppSecretRuntimeReload(ctx, reload); err != nil || n != 1 {
		t.Fatalf("reload: %d %v", n, err)
	}
	ack := state.AppSecretRuntimeReloadAckResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, Revision: reload.Revision, Status: state.SecretApplicationReloadAckApplied, AttemptedAt: time.Now().UTC(), Candidates: reload.Candidates}
	if n, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil || n != 1 {
		t.Fatalf("ack: %d %v", n, err)
	}
	fence := bindingPromotionFence(t, store, acct, app, candidate)
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET credential_generation=2 WHERE id=$1`, bindingID); err != nil {
		t.Fatal(err)
	}
	secret.ManagedCredentialGeneration, secret.ValueHash = 2, "fedcba9876543210"
	if err := store.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("rotation race: %v", err)
	}
	rows, err = store.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{selector})
	if err != nil || len(rows) != 1 || rows[0].CurrentVersion != 2 || rows[0].ApplicationAckVersion != 1 {
		t.Fatalf("rotated metadata: %+v %v", rows, err)
	}
	// Uncommitted receipt changes hold the revision row until commit. The
	// promotion must wait and then reject the changed observation.
	fence = bindingPromotionFence(t, store, acct, app, candidate)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `UPDATE app_secret_runtime_reload_observations SET application_ack_status='failed',application_ack_error_code='application_reload_failed' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := store.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("promotion did not wait for receipt commit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("receipt commit race: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promotion did not release")
	}
}

func bindingAdoptionNode(t *testing.T, store state.Store, ctx context.Context) string {
	t.Helper()
	if _, postgres := store.(*state.PgStore); postgres {
		node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		return node.ID
	}
	return state.DefaultLocalNodeName
}

func TestBindingApplicationAdoptionEmptySidecarsMem(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, app, _, _ := bindingPromotionFixture(t, store)
	credential := seedInventoryStorageBinding(t, store, acct.ID, app.ID, "default")
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", Sidecars: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, deployment.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	selectors := []state.BindingAdoptionSelector{{Type: api.BindingTypeObjectStorage, BindingID: credential.ID, Scope: "default", Keys: api.BindingCredentialSecretKeys(api.BindingTypeObjectStorage, "GREGALE_S3_ASSETS")}}
	rows, err := store.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, selectors)
	if err != nil || len(rows) != 6 {
		t.Fatalf("empty sidecars: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.InstanceID != instance.ID || row.WorkloadName != "" || row.ReloadSupport != "enabled" {
			t.Fatalf("main-only support: %+v", row)
		}
	}
	targets, err := store.ListAppSecretRuntimeReloadTargets(ctx, acct.ID, app.ID, "default")
	if err != nil || len(targets) != 6 {
		t.Fatalf("delivery targets: %+v %v", targets, err)
	}
	for _, target := range targets {
		if target.ReloadSupport != "enabled" {
			t.Fatalf("delivery/adoption support mismatch: %+v", target)
		}
	}
}
