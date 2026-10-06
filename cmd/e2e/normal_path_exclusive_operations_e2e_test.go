package e2e_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestE2E_NormalPath_ExclusiveOperationsSerializeAcrossRealDaemons proves the
// customer API, PostgreSQL owner store, schedd lease/dispatch loop,
// gatewayd-internal bridge, and VMMD result path all observe one serialized
// owner for a shared operation key.
func TestE2E_NormalPath_ExclusiveOperationsSerializeAcrossRealDaemons(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate internal service key: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal internal service private key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal internal service public key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "schedd-internal-svc.pem")
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err := os.WriteFile(keyPath, privatePEM, 0o600); err != nil {
		t.Fatalf("write internal service private key: %v", err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	publicKeys, err := json.Marshal(map[string]string{"schedd": string(publicPEM)})
	if err != nil {
		t.Fatalf("encode internal service public keys: %v", err)
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "normal-exclusive-operations", api.PlanPro,
		"FAAS_INTERNAL_SVC_KEY_PATH="+keyPath,
		"FAAS_INTERNAL_SVC_PUBKEYS="+string(publicKeys),
	)
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "exclusive-operations")
	f.vmmd.SetVersion(instance.ID, "exclusive-operations")

	policy := api.ExclusiveOperationPolicy{
		Name: "customer-sync", Scope: "account", MemberAppIDs: []string{f.app.ID},
		Contention: "queue", LeaseSeconds: 15, MaxAttemptSeconds: 60,
	}
	if body, status := doReq(t, f.h, f.key, http.MethodPut,
		"/v1/account/operation-policies/"+policy.Name, policy); status != http.StatusOK {
		t.Fatalf("create managed operation policy: status=%d body=%s", status, body)
	}

	gate := f.vmmd.InstallRequestGate(instance.ID, 1)
	defer gate.Release()
	submit := func(path string) api.ExclusiveOperationAccepted {
		t.Helper()
		body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+f.app.Slug+"/operations",
			api.ExclusiveOperationRequest{
				Policy: policy.Name, Key: json.RawMessage(`"customer:acme:crm-sync"`),
				Invocation: api.InvokeRequest{Method: http.MethodPost, Path: path},
			})
		if status != http.StatusAccepted {
			t.Fatalf("submit managed operation %q: status=%d body=%s", path, status, body)
		}
		var accepted api.ExclusiveOperationAccepted
		if err := json.Unmarshal(body, &accepted); err != nil {
			t.Fatalf("decode managed operation receipt: %v body=%s", err, body)
		}
		if accepted.ID == "" {
			t.Fatalf("managed operation %q returned an empty ID", path)
		}
		return accepted
	}
	readOperation := func(id string) api.ExclusiveOperationRecord {
		t.Helper()
		body, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/operations/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("read managed operation %s: status=%d body=%s", id, status, body)
		}
		var operation api.ExclusiveOperationRecord
		if err := json.Unmarshal(body, &operation); err != nil {
			t.Fatalf("decode managed operation %s: %v body=%s", id, err, body)
		}
		return operation
	}
	waitForState := func(id, want string, timeout time.Duration) api.ExclusiveOperationRecord {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			operation := readOperation(id)
			if operation.State == want {
				return operation
			}
			if operation.State == "failed" || operation.State == "cancelled" || operation.State == "expired" {
				t.Fatalf("managed operation %s reached terminal state %q: %s", id, operation.State, operation.LastError)
			}
			time.Sleep(50 * time.Millisecond)
		}
		operation := readOperation(id)
		t.Fatalf("managed operation %s state=%q, want %q", id, operation.State, want)
		return operation
	}

	first := submit("/managed/first")
	if !gate.WaitArrived(15 * time.Second) {
		t.Fatalf("first managed invocation did not reach VMMD; operation=%+v", readOperation(first.ID))
	}
	firstRunning := waitForState(first.ID, "running", 5*time.Second)
	if firstRunning.Generation == 0 {
		t.Fatalf("running operation has no ownership generation: %+v", firstRunning)
	}

	second := submit("/managed/second")
	if second.ID == first.ID {
		t.Fatalf("distinct queued requests shared operation ID %q", first.ID)
	}
	secondPending := readOperation(second.ID)
	if secondPending.State != "pending" || secondPending.Generation != 0 {
		t.Fatalf("competing operation acquired while first owner was blocked: %+v", secondPending)
	}
	// Give schedd more than one dispatch tick to prove that the second request
	// stays behind the live owner instead of merely not having been scanned yet.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := readOperation(second.ID); got.State != "pending" || got.Generation != 0 {
			t.Fatalf("competing operation changed while first owner was blocked: %+v", got)
		}
		time.Sleep(75 * time.Millisecond)
	}
	if got := f.vmmd.ForwardCount(); got != 1 {
		t.Fatalf("VMMD received %d requests while one owner held the lane, want 1", got)
	}

	gate.Release()
	firstDone := waitForState(first.ID, "completed", 15*time.Second)
	secondDone := waitForState(second.ID, "completed", 20*time.Second)
	if secondDone.Generation <= firstDone.Generation {
		t.Fatalf("fencing generation did not advance: first=%d second=%d", firstDone.Generation, secondDone.Generation)
	}
	requests := f.vmmd.Requests()
	if len(requests) != 2 || requests[0].Init.GetRequestUri() != "/managed/first" || requests[1].Init.GetRequestUri() != "/managed/second" {
		t.Fatalf("managed invocation delivery order = %+v, want first then second", requests)
	}
}
