// adr:438
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSecretProcessGenerationMem(t *testing.T) {
	secretProcessGenerationSuite(t, state.NewMemStore())
}
func TestSecretProcessGenerationPG(t *testing.T) {
	store, _ := pgStore(t)
	secretProcessGenerationSuite(t, store)
}

func secretProcessGenerationSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	credential := seedInventoryStorageBinding(t, store, acct.ID, app.ID, "default")
	keys := api.BindingCredentialSecretKeys(api.BindingTypeObjectStorage, "GREGALE_S3_ASSETS")
	grants := map[string]string{}
	for _, key := range keys {
		grants[key] = "secret:" + key
	}
	grantJSON, _ := json.Marshal(grants)
	sidecars, _ := json.Marshal([]map[string]any{{"name": "worker", "type": "sidecar", "env_secrets": grants}})
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", OverrideEnvSecrets: grantJSON, Sidecars: sidecars})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, deployment.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, deployment.ID, "worker", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, bindingAdoptionNode(t, store, ctx), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	selector := state.BindingAdoptionSelector{Type: api.BindingTypeObjectStorage, BindingID: credential.ID, Scope: "default", Keys: keys}
	read := func() []state.BindingApplicationAdoptionRow {
		t.Helper()
		rows, err := store.(state.BindingApplicationAdoptionStore).ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, []state.BindingAdoptionSelector{selector})
		if err != nil || len(rows) != 12 {
			t.Fatalf("generation roster: rows=%d err=%v", len(rows), err)
		}
		return rows
	}
	candidates := []state.AppSecretDeliveryCandidate{}
	for _, key := range keys {
		candidates = append(candidates, state.AppSecretDeliveryCandidate{Scope: "default", Key: key, Version: 1})
	}
	for _, workload := range []string{"", "worker"} {
		if _, err := store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload,
			Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent, Candidates: candidates}); err != nil {
			t.Fatal(err)
		}
	}
	process := func(workload, generation, previous string) state.AppSecretRuntimeProcess {
		return state.AppSecretRuntimeProcess{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload, Generation: generation, PreviousGeneration: previous}
	}
	ack := func(workload, generation string) error {
		_, err := store.RecordAppSecretRuntimeReloadAck(ctx, state.AppSecretRuntimeReloadAckResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, WorkloadName: workload,
			Generation: generation, Revision: strings.Repeat("a", 64), Status: state.SecretApplicationReloadAckApplied, Candidates: candidates})
		return err
	}
	a, b, worker := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	if err := ack("", ""); err != nil {
		t.Fatalf("legacy ACK: %v", err)
	}
	for _, row := range read() {
		if row.ProcessGeneration != "" || row.ApplicationAckGeneration != "" {
			t.Fatal("invented legacy coverage")
		}
	}
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("", a, "")); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("worker", worker, worker)); err != nil {
		t.Fatal(err)
	}
	if err := ack("", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted missing generation: %v", err)
	}
	if err := ack("", a); err != nil {
		t.Fatal(err)
	}
	if err := ack("worker", worker); err != nil {
		t.Fatal(err)
	}
	guarded := store.(state.BindingPromotionStore)
	revision, err := guarded.ReadBindingPromotionRevision(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("", a, "")); err != nil {
		t.Fatal(err)
	}
	again, err := guarded.ReadBindingPromotionRevision(ctx, acct.ID, app.ID)
	if err != nil || again != revision {
		t.Fatal("duplicate registration invalidated the current receipt or promotion revision")
	}
	for _, row := range read() {
		if row.ApplicationAckVersion != 1 || row.ProcessGeneration != row.ApplicationAckGeneration {
			t.Fatalf("current receipt lost: %+v", row)
		}
	}
	fence := bindingPromotionFence(t, guarded, acct, app, candidate)
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("", b, a)); err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("restart did not fence promotion: %v", err)
	}
	assertRestart := func(current string) {
		t.Helper()
		for _, row := range read() {
			if row.WorkloadName == "" && (row.ProcessGeneration != current || row.ApplicationAckVersion != 0 || row.ApplicationAckGeneration != "") {
				t.Fatalf("previous process still current: %+v", row)
			}
			if row.WorkloadName == "worker" && (row.ProcessGeneration != worker || row.ApplicationAckGeneration != worker || row.ApplicationAckVersion != 1) {
				t.Fatalf("main restart affected sidecar: %+v", row)
			}
		}
	}
	assertRestart(b)
	for _, operation := range []func() error{
		func() error { return ack("", a) },
		func() error { return store.BeginAppSecretRuntimeProcess(ctx, process("", a, "")) },
		func() error { return store.RetireAppSecretRuntimeProcess(ctx, process("", a, "")) },
	} {
		if err := operation(); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted late old-generation operation: %v", err)
		}
	}
	assertRestart(b)
	if err := ack("", b); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireAppSecretRuntimeProcess(ctx, process("", b, "")); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireAppSecretRuntimeProcess(ctx, process("", b, "")); err != nil {
		t.Fatal(err)
	}
	assertRestart("")
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("", b, b)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("reactivated a retired generation")
	}
	if err := ack("", b); !errors.Is(err, state.ErrConflict) {
		t.Fatal("accepted a retired process ACK")
	}
	current := b
	for index := 1; index <= 10; index++ {
		next := fmt.Sprintf("%032x", index)
		var wg sync.WaitGroup
		var startErr, ackErr, projectionErr error
		wg.Add(3)
		go func() {
			defer wg.Done()
			startErr = store.BeginAppSecretRuntimeProcess(ctx, process("", next, current))
		}()
		go func() { defer wg.Done(); ackErr = ack("", current) }()
		go func() {
			defer wg.Done()
			_, projectionErr = store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{AccountID: acct.ID, AppID: app.ID, InstanceID: runtime.ID, Revision: strings.Repeat("a", 64), Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalNotAttempted, Candidates: candidates})
		}()
		wg.Wait()
		if projectionErr != nil || startErr != nil || ackErr != nil && !errors.Is(ackErr, state.ErrConflict) {
			t.Fatalf("ACK/projection/start race: %v %v %v", startErr, ackErr, projectionErr)
		}
		assertRestart(next)
		if err := ack("", next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	other := process("", strings.Repeat("d", 32), current)
	other.AccountID = uuid.NewString()
	if err := store.BeginAppSecretRuntimeProcess(ctx, other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-account generation: %v", err)
	}
	if err := store.DeleteInstance(ctx, runtime.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginAppSecretRuntimeProcess(ctx, process("", strings.Repeat("d", 32), current)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generation survived instance deletion: %v", err)
	}
}

func TestSecretProcessGenerationValidation(t *testing.T) {
	for _, generation := range []string{"", "short", strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		if state.ValidSecretProcessGeneration(generation) {
			t.Fatalf("accepted malformed generation: %q", generation)
		}
	}
	if !state.ValidSecretProcessGeneration(strings.Repeat("a", 32)) {
		t.Fatal("valid generation rejected")
	}
}
