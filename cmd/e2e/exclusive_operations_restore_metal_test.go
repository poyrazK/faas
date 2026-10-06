//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestExclusiveOperationFencesRestoredKVMOwnerMetal proves on a real
// Firecracker guest that a cancelled owner's identity can survive in guest
// memory and a snapshot, while its platform authority does not. A successor
// runs after restoring that snapshot with a newer generation; the old claim
// cannot renew or commit a result/effect.
func TestExclusiveOperationFencesRestoredKVMOwnerMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping exclusive-operation KVM acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "exclusive-operation-restore")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro, "exclusive-restore-"+randHexSuffix())
	accountID := accountIDFromKey(t, ctx, pool, key)
	slug := "exclusive-restore-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: &falsy,
	}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	buildBody, status := postMultipartDeployment(t, h, key, slug, exclusiveRestoreNodeFixture(t), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	depID, _ := parseQueuedDeployment(t, buildBody)
	deployCtx, deployCancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer deployCancel()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("source deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("deployment did not prime a parked snapshot: %v", err)
	}

	store := state.NewPgStore(pool)
	initialSnapshot, err := store.LatestSnapshotForTier(ctx, depID, state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("read initial snapshot: %v", err)
	}
	client := h.HTTPClient()
	url := gatewayAppURL(h, slug)
	host := slug + ".apps.test.example"

	policy := api.ExclusiveOperationPolicy{
		Name: "restore-fence", Scope: "account", MemberAppIDs: []string{appID},
		Contention: "queue", LeaseSeconds: 15, MaxAttemptSeconds: 120, MaxAttempts: 3,
	}
	if body, status := doReq(t, h, key, http.MethodPut, "/v1/account/operation-policies/"+policy.Name, policy); status != http.StatusOK {
		t.Fatalf("create exclusive-operation policy: status=%d body=%s", status, body)
	}

	submit := func(payload string) api.ExclusiveOperationAccepted {
		t.Helper()
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/operations", api.ExclusiveOperationRequest{
			Policy: policy.Name,
			Key:    json.RawMessage(`"customer:acme:crm-sync"`),
			Invocation: api.InvokeRequest{
				Method: http.MethodPost, Path: "/sync", Payload: json.RawMessage(payload),
			},
		})
		if status != http.StatusAccepted {
			t.Fatalf("submit exclusive operation: status=%d body=%s", status, body)
		}
		var accepted api.ExclusiveOperationAccepted
		if err := json.Unmarshal(body, &accepted); err != nil {
			t.Fatalf("decode accepted operation: %v body=%s", err, body)
		}
		if accepted.ID == "" {
			t.Fatal("accepted operation has no ID")
		}
		return accepted
	}

	first := submit(`{"hold":true}`)
	firstOwner := waitExclusiveOperationState(t, store, accountID, first.ID, "running", 60*time.Second)
	if firstOwner.Generation == 0 || firstOwner.ClaimToken == "" || firstOwner.LeaseExpiresAt == nil || firstOwner.AttemptDeadline == nil {
		t.Fatalf("running operation omitted its internal owner claim: %+v", firstOwner)
	}
	oldClaim := exclusivework.Claim{
		OperationID: firstOwner.ID, AccountID: firstOwner.AccountID, Generation: firstOwner.Generation,
		Token: firstOwner.ClaimToken, IncarnationID: firstOwner.IncarnationID,
		ExpiresAt: *firstOwner.LeaseExpiresAt, Deadline: *firstOwner.AttemptDeadline,
	}
	parts := strings.SplitN(firstOwner.IncarnationID, "/", 3)
	if len(parts) != 3 {
		t.Fatalf("malformed owner incarnation %q", firstOwner.IncarnationID)
	}

	waitForExclusiveRestoreGuestMemory(t, client, url, host, first.ID, firstOwner.Generation)
	if err := store.BeginExclusiveSnapshot(ctx, parts[0]); !errors.Is(err, exclusivework.ErrBusy) {
		t.Fatalf("snapshot barrier accepted an active KVM owner: %v", err)
	}
	if err := store.UpdateInstanceStateWithTimestamp(ctx, parts[0], string(state.StateSnapshotting), time.Now()); err == nil {
		t.Fatal("direct snapshot transition bypassed the active KVM owner guard")
	}
	if got, err := store.InstanceByID(ctx, parts[0]); err != nil || got.State != string(state.StateRunning) {
		t.Fatalf("blocked snapshot changed runtime state: instance=%+v err=%v", got, err)
	}

	if body, status := doReq(t, h, key, http.MethodPost, "/v1/operations/"+first.ID+"/cancel", nil); status != http.StatusNoContent {
		t.Fatalf("cancel stale owner: status=%d body=%s", status, body)
	}
	waitExclusiveOperationState(t, store, accountID, first.ID, "cancelled", 10*time.Second)
	waitForExclusiveRestoreGuestFinished(t, client, url, host, first.ID, 30*time.Second)
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
		t.Fatalf("park after owner cancellation: status=%d body=%s", status, body)
	}
	snapshotID := waitAfterRestoreSnapshot(t, store, depID, initialSnapshot.ID)
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, appID, state.StateParked, 60*time.Second); err != nil {
		t.Fatalf("cancelled guest did not park: %v", err)
	}

	second := submit(`{"hold":false}`)
	secondDone := waitExclusiveOperationState(t, store, accountID, second.ID, "completed", 90*time.Second)
	if secondDone.Generation <= oldClaim.Generation {
		t.Fatalf("successor generation did not advance: old=%d new=%d", oldClaim.Generation, secondDone.Generation)
	}
	var result struct {
		PriorOperationID    string `json:"prior_operation_id"`
		PriorGeneration     string `json:"prior_generation"`
		CurrentOperationID  string `json:"current_operation_id"`
		CurrentGeneration   string `json:"current_generation"`
		FinishedBeforeOwner bool   `json:"finished_before_owner"`
	}
	if err := json.Unmarshal(secondDone.Result, &result); err != nil {
		t.Fatalf("decode restored guest result %q: %v", secondDone.Result, err)
	}
	if result.PriorOperationID != first.ID || result.PriorGeneration != jsonNumber(oldClaim.Generation) || !result.FinishedBeforeOwner {
		t.Fatalf("restored guest did not retain the old operation context: %+v", result)
	}
	if result.CurrentOperationID != second.ID || result.CurrentGeneration != jsonNumber(secondDone.Generation) {
		t.Fatalf("restored guest did not receive the successor context: %+v", result)
	}
	newParts := strings.SplitN(secondDone.IncarnationID, "/", 3)
	if len(newParts) != 3 || newParts[1] == parts[1] {
		t.Fatalf("successor reused the pre-snapshot VM incarnation: old=%q new=%q", firstOwner.IncarnationID, secondDone.IncarnationID)
	}
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, newParts[1], "restore", 15*time.Second); err != nil {
		t.Fatalf("successor did not run after a real snapshot restore %s: %v", snapshotID, err)
	}

	if _, err := store.RenewExclusiveOperation(ctx, oldClaim); !errors.Is(err, exclusivework.ErrStaleOwner) {
		t.Fatalf("restored stale owner renewed after successor completion: %v", err)
	}
	staleEffects := []exclusivework.Effect{{Name: "stale-restored-owner", Payload: json.RawMessage(`{"must_not_commit":true}`)}}
	if err := store.CommitExclusiveOperation(ctx, oldClaim, json.RawMessage(`{"must_not_commit":true}`), staleEffects); !errors.Is(err, exclusivework.ErrStaleOwner) {
		t.Fatalf("restored stale owner committed a result/effect: %v", err)
	}
	var effectCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_effects WHERE operation_id=$1::uuid AND generation=$2 AND name='stale-restored-owner'`, first.ID, oldClaim.Generation).Scan(&effectCount); err != nil {
		t.Fatalf("check stale effect insertion: %v", err)
	}
	if effectCount != 0 {
		t.Fatalf("stale owner inserted %d platform effects", effectCount)
	}
}

func waitExclusiveOperationState(t *testing.T, store *state.PgStore, accountID, operationID, want string, timeout time.Duration) state.ExclusiveOperation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := store.ExclusiveOperationByID(ctx, accountID, operationID)
		if err != nil {
			t.Fatalf("read operation %s: %v", operationID, err)
		}
		if operation.State == want {
			return operation
		}
		if operation.State == "failed" || operation.State == "expired" || operation.State == "cancelled" {
			t.Fatalf("operation %s reached %q before %q: %s", operationID, operation.State, want, operation.LastError)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("operation %s remained %q, want %q: %v", operationID, operation.State, want, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForExclusiveRestoreGuestMemory(t *testing.T, client *http.Client, url, host, operationID string, generation int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		body, status := doGetWithHost(t, client, url+"/status", host, 5*time.Second)
		if status == http.StatusOK {
			var got struct {
				OperationID string `json:"operation_id"`
				Generation  string `json:"generation"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode guest owner status %q: %v", body, err)
			}
			if got.OperationID == operationID && got.Generation == jsonNumber(generation) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("real guest never observed operation %s generation %d", operationID, generation)
}

func waitForExclusiveRestoreGuestFinished(t *testing.T, client *http.Client, url, host, operationID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, status := doGetWithHost(t, client, url+"/status", host, 5*time.Second)
		if status == http.StatusOK {
			var got struct {
				OperationID string `json:"operation_id"`
				Finished    bool   `json:"finished"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode guest finish status %q: %v", body, err)
			}
			if got.OperationID == operationID && got.Finished {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("guest operation %s did not finish after cancellation", operationID)
}

func jsonNumber(value int64) string { return strconv.FormatInt(value, 10) }

func exclusiveRestoreNodeFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"exclusive-operation-restore-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
let lastOperationId = '';
let lastGeneration = '';
let finished = true;
http.createServer((req, res) => {
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  if (req.url === '/status') {
    res.writeHead(200, {'content-type': 'application/json'});
    res.end(JSON.stringify({operation_id: lastOperationId, generation: lastGeneration, finished}));
    return;
  }
  if (req.url !== '/sync') { res.writeHead(404); res.end(); return; }
  const chunks = [];
  req.on('data', chunk => chunks.push(chunk));
  req.on('end', () => {
    let payload = {};
    try { payload = JSON.parse(Buffer.concat(chunks).toString() || '{}'); } catch (_) {}
    const priorOperationId = lastOperationId;
    const priorGeneration = lastGeneration;
    lastOperationId = req.headers['x-gregale-operation-id'] || '';
    lastGeneration = req.headers['x-gregale-operation-generation'] || '';
    if (payload.hold) {
      finished = false;
      setTimeout(() => {
        finished = true;
        if (!res.destroyed) { res.writeHead(200); res.end('old owner finished'); }
      }, 3000);
      return;
    }
    res.writeHead(200, {'content-type': 'application/json'});
    res.end(JSON.stringify({
      prior_operation_id: priorOperationId,
      prior_generation: priorGeneration,
      current_operation_id: lastOperationId,
      current_generation: lastGeneration,
      finished_before_owner: finished
    }));
  });
}).listen(8080, '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
