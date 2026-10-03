package e2e_test

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/commitmanaged"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// The real API, scheduler (including its managed relay), gateway, and two
// PostgreSQL databases are exercised. Customer connections require verified TLS.
// VMMD is the existing normal-path protocol fixture; native VM acceptance is
// a separate gate and cannot be inferred from this test.
func TestE2E_CommitProducerDeathReachesCompletedOperation(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required")
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "commit-worker", api.PlanHobby, commitOperationEnvironment(t, "FAAS_COMMIT_API_ENABLED=true")...)
	if f == nil {
		t.Fatal("commit acceptance requires the PostgreSQL harness")
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "commit-worker")
	f.vmmd.SetVersion(instance.ID, "commit-worker")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:commit-worker\n", 10*time.Second)
	runCommitProducerHandoff(t, f.h, f.store, f.key, f.app.ID, "orders")
}

type commitHandoffOptions struct {
	SourceRecovery, RelayFaults bool
	RepairPoison                *func(context.Context, string) error
}

func runCommitProducerHandoff(t *testing.T, h *e2etest.Harness, store *state.PgStore, key, appID, sourceName string, options ...commitHandoffOptions) state.ExclusiveOperation {
	t.Helper()
	before, err := store.ListInvocationsForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCount := len(before)
	var beforeOperations int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1::uuid`, appID).Scan(&beforeOperations); err != nil {
		t.Fatal(err)
	}
	cluster := pgtest.OpenTLSCluster(t)
	customer := cluster.Admin
	ctx := context.Background()
	if _, err := customer.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `CREATE TABLE orders(id uuid PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	account, err := store.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, account.AccountID, exclusivework.Policy{
		Name: sourceName, Scope: "account", Contention: "queue", MemberAppIDs: []string{appID},
		LeaseSeconds: 15, MaxAttemptSeconds: 60, MaxAttempts: 5, RetryAfterSeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	src, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: account.AccountID, AppID: appID, Name: sourceName, OperationPolicy: sourceName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, src.AccountID, src.ID, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.SetCommitSourceEnabled(context.Background(), src.AccountID, src.ID, false) })
	if _, err := customer.Exec(ctx, `CREATE ROLE relay LOGIN PASSWORD 'producer-death-fixture'; GRANT SELECT,INSERT,UPDATE,DELETE ON public.gregale_outbox TO relay; GRANT SELECT ON public.gregale_commit_binding TO relay`); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, src.ID); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := commitwork.SealConnection(identity.Recipient(), src.ID, cluster.URL("relay", "producer-death-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCommitSourceConnection(ctx, src.AccountID, src.ID, blob); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(t.TempDir(), "host.age")
	if err := secretbox.WriteHostKeyAtPath(identityPath, identity); err != nil {
		t.Fatal(err)
	}
	previousEnv := map[string]string{}
	for name, value := range map[string]string{
		"FAAS_COMMIT_RELAY_ENABLED":   "true",
		"FAAS_COMMIT_DATABASE_HOSTS":  "localhost",
		"FAAS_COMMIT_DATABASE_CIDRS":  "127.0.0.1/32",
		"FAAS_HOST_AGE_IDENTITY_PATH": identityPath,
		"PGSSLROOTCERT":               cluster.CAPath,
	} {
		previousEnv[name] = h.ScheddEnvValue(name)
		if err := h.SetScheddEnv(name, value); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for name, value := range previousEnv {
			if err := h.SetScheddEnv(name, value); err != nil {
				t.Error(err)
			}
		}
		if err := h.RestartSchedd(); err != nil {
			t.Error(err)
		}
	})
	if err := h.RestartSchedd(); err != nil {
		t.Fatal(err)
	}
	relayFaults := len(options) > 0 && options[0].RelayFaults
	var releaseCheckpoint func()
	holdCheckpoint := func() {
		// Hold the acceptance checkpoint after durable platform admission.
		guard, err := customer.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Exec(ctx, `SELECT pg_advisory_lock(7420366)`); err != nil {
			guard.Release()
			t.Fatal(err)
		}
		releaseCheckpoint = func() {
			if guard != nil {
				_, _ = guard.Exec(ctx, `SELECT pg_advisory_unlock(7420366)`)
				guard.Release()
				guard = nil
			}
		}
		t.Cleanup(releaseCheckpoint)
		if _, err := customer.Exec(ctx, `CREATE FUNCTION hold_commit_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.accepted_at IS NOT NULL AND OLD.accepted_at IS NULL THEN
 PERFORM set_config('lock_timeout','0',true); PERFORM pg_advisory_xact_lock(7420366); END IF;
 RETURN NEW; END $$; CREATE TRIGGER hold_commit_checkpoint BEFORE UPDATE ON public.gregale_outbox FOR EACH ROW EXECUTE FUNCTION hold_commit_checkpoint()`); err != nil {
			t.Fatal(err)
		}
	}
	if relayFaults {
		if options[0].RepairPoison != nil {
			*options[0].RepairPoison = func(repairCtx context.Context, event string) error {
				_, err := customer.Exec(repairCtx, `UPDATE public.gregale_outbox SET event_type='order.created' WHERE event_id=$1::uuid AND accepted_at IS NULL AND blocked_code IS NOT NULL`, event)
				return err
			}
		}
		// An invalid event must be blocked without starving the healthy one.
		if _, err := customer.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES($1::uuid,' ','{}')`, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	var schema string
	if err := customer.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	rolledBackID := uuid.NewString()
	rolledBack, err := customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rolledBack.Rollback(context.Background()) }()
	if _, err := rolledBack.Exec(ctx, `INSERT INTO orders(id) VALUES($1::uuid)`, rolledBackID); err != nil {
		t.Fatal(err)
	}
	if err := commitwork.Insert(ctx, rolledBack, commitwork.Event{ID: rolledBackID, Type: "order.created", Data: []byte(`{"rollback":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var rolledBackRows int
	if err := customer.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders WHERE id=$1::uuid)+(SELECT count(*) FROM public.gregale_outbox WHERE event_id=$1::uuid)`, rolledBackID).Scan(&rolledBackRows); err != nil || rolledBackRows != 0 {
		t.Fatalf("rollback retained business/outbox rows: %d (%v)", rolledBackRows, err)
	}
	eventID := uuid.NewString()
	producer := exec.Command(os.Args[0], "-test.run=^TestCommitProducerProcess$")
	producer.Env = append(os.Environ(), "GREGALE_COMMIT_PRODUCER=1", "GREGALE_COMMIT_SCHEMA="+schema, "GREGALE_COMMIT_EVENT="+eventID, "DATABASE_URL="+cluster.AdminURL)
	stdout, err := producer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if producer.ProcessState == nil {
			_ = producer.Process.Kill()
			_ = producer.Wait()
		}
	})
	barrier := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			barrier <- scanner.Text()
		} else {
			barrier <- ""
		}
	}()
	select {
	case line := <-barrier:
		if line != "committed" {
			t.Fatal("producer did not commit")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("producer commit timed out")
	}
	if err := producer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = producer.Wait()
	// Keep the source paused until after producer death. Only the independently
	// running schedd may accept the committed event when the source resumes.
	if _, err := store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, eventID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paused source accepted before producer death: %v", err)
	}
	recoverSource := len(options) > 0 && options[0].SourceRecovery
	if relayFaults && !recoverSource {
		holdCheckpoint()
	}
	if recoverSource {
		if _, err := customer.Exec(ctx, `ALTER ROLE relay PASSWORD 'rotated-producer-death-fixture'`); err != nil {
			t.Fatal(err)
		}
		cluster.Stop()
	}
	body, status := doReq(t, h, key, http.MethodPatch, "/v1/commit-sources/"+src.ID, map[string]bool{"enabled": true})
	if status != http.StatusOK {
		t.Fatalf("resume source: %d %s", status, body)
	}
	if recoverSource {
		first := waitCommitSourceHealth(t, h, key, src.ID, "database_unavailable", time.Time{})
		second := waitCommitSourceHealth(t, h, key, src.ID, "database_unavailable", *first.LastCheckedAt)
		if second.PendingEvents != nil || second.BlockedEvents != nil || second.OldestPendingAt != nil {
			t.Fatalf("source outage reported a known backlog: %+v", second)
		}
		if _, err := store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, eventID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("source outage accepted unavailable work: %v", err)
		}
		cluster.Start()
		customer.Reset()
		// The old sealed password remains configured. A fresh source pass must
		// still fail after the database recovers, rather than skipping TLS/auth.
		waitCommitSourceHealth(t, h, key, src.ID, "database_unavailable", *second.LastCheckedAt)
		if _, err := store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, eventID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("retired source credential accepted work: %v", err)
		}
		rotated, err := commitwork.SealConnection(identity.Recipient(), src.ID, cluster.URL("relay", "rotated-producer-death-fixture"))
		if err != nil {
			t.Fatal(err)
		}
		if relayFaults {
			holdCheckpoint()
		}
		if err := store.SetCommitSourceConnection(ctx, src.AccountID, src.ID, rotated); err != nil {
			t.Fatal(err)
		}
		t.Log("managed relay reported unknown backlog through repeated source-outage passes, rejected its retired credential, and received the rotated credential")
	}
	var receipt state.CommitReceipt
	deadline := time.Now().Add(30 * time.Second)
	for {
		receipt, err = store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, eventID)
		if err == nil {
			break
		}
		if !errors.Is(err, state.ErrNotFound) || time.Now().After(deadline) {
			t.Fatalf("scheduler did not accept committed event: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if receipt.OperationID == "" || receipt.InvocationID != "" {
		t.Fatalf("Commit did not accept managed work: %+v", receipt)
	}
	if relayFaults {
		var checkpointPID int
		deadline = time.Now().Add(10 * time.Second)
		for {
			err := customer.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name='gregale-commit' AND wait_event='advisory' AND query LIKE 'UPDATE public.gregale_outbox%' LIMIT 1`).Scan(&checkpointPID)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("relay never reached the held checkpoint: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		// Abort the customer session while the lock is still held. PostgreSQL
		// must not finish the checkpoint after discovering the killed client.
		if _, err := customer.Exec(ctx, `SELECT pg_terminate_backend($1)`, checkpointPID); err != nil {
			t.Fatal(err)
		}
		releaseCheckpoint()
		if _, err := customer.Exec(ctx, `DROP TRIGGER hold_commit_checkpoint ON public.gregale_outbox; DROP FUNCTION hold_commit_checkpoint()`); err != nil {
			t.Fatal(err)
		}
		var missingCheckpoint bool
		if err := customer.QueryRow(ctx, `SELECT accepted_at IS NULL AND receipt_id IS NULL FROM public.gregale_outbox WHERE event_id=$1::uuid`, eventID).Scan(&missingCheckpoint); err != nil || !missingCheckpoint {
			t.Fatalf("killed relay checkpoint survived: %v %v", missingCheckpoint, err)
		}
		// Advance only the fixture's expired lease, avoiding a minute-long
		// sleep. Independent tests qualify natural expiry and stale fencing.
		if _, err := customer.Exec(ctx, `UPDATE public.gregale_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE accepted_at IS NULL`); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PGSSLROOTCERT", cluster.CAPath)
		peer := &commitmanaged.Manager{Store: store, Identities: []*age.X25519Identity{identity}, Policy: commitwork.NetworkPolicy{
			Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		}}
		peerCtx, cancelPeer := context.WithCancel(t.Context())
		peerDone := make(chan error, 1)
		go func() { peerDone <- peer.Run(peerCtx) }()
		t.Cleanup(func() { cancelPeer(); <-peerDone })
		if err := h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		t.Log("terminated actual scheduler after durable acceptance before checkpoint; competing managed relays recover the expired lease with the original receipt")
	}
	completed := waitCommitOperationCompleted(t, h, store, key, src.AccountID, receipt.OperationID, 30*time.Second)
	// Wait for the source checkpoint, then restart the real scheduler. A
	// post-restart source health update proves it scanned the accepted event.
	deadline = time.Now().Add(30 * time.Second)
	for {
		var checkpoint string
		err = customer.QueryRow(ctx, `SELECT COALESCE(receipt_id::text,'') FROM gregale_outbox WHERE event_id=$1`, eventID).Scan(&checkpoint)
		if err == nil && checkpoint == receipt.ID {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("scheduler source checkpoint: %s %v", checkpoint, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var prior state.CommitSource
	deadline = time.Now().Add(30 * time.Second)
	for {
		prior, err = store.CommitSourceByID(ctx, src.AccountID, src.ID)
		if err != nil {
			t.Fatal(err)
		}
		if prior.LastCheckedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not record source health")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := h.RestartSchedd(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		health, err := store.CommitSourceByID(ctx, src.AccountID, src.ID)
		if err != nil {
			t.Fatal(err)
		}
		if health.LastCheckedAt != nil && health.LastCheckedAt.After(*prior.LastCheckedAt) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted scheduler did not scan Commit source")
		}
		time.Sleep(100 * time.Millisecond)
	}
	invocations, err := store.ListInvocationsForApp(ctx, appID)
	if err != nil || len(invocations) != beforeCount {
		t.Fatalf("logical invocations=%d err=%v", len(invocations), err)
	}
	var afterOperations int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1::uuid`, appID).Scan(&afterOperations); err != nil || afterOperations != beforeOperations+1 {
		t.Fatalf("logical managed operations=%d before=%d err=%v", afterOperations, beforeOperations, err)
	}
	if _, err := store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, rolledBackID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("scheduler accepted a rolled-back event: %v", err)
	}
	body, status = doReq(t, h, key, http.MethodGet, "/v1/commit-sources/"+src.ID+"/events/"+eventID, nil)
	if status != http.StatusOK {
		t.Fatalf("receipt API: %d %s", status, body)
	}
	body, status = doReq(t, h, key, http.MethodGet, "/v1/operations/"+receipt.OperationID, nil)
	var operation api.CommitOperationResponse
	if err := json.Unmarshal(body, &operation); err != nil || status != http.StatusOK || operation.State != "completed" || operation.CompletedAt == nil || operation.EventID != eventID {
		t.Fatalf("completed operation API: %d %+v %v", status, operation, err)
	}
	return completed
}

func waitCommitOperationCompleted(t *testing.T, h *e2etest.Harness, store *state.PgStore, key, account, id string, timeout time.Duration) state.ExclusiveOperation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		body, code := doReq(t, h, key, http.MethodGet, "/v1/operations/"+id, nil)
		var operation api.CommitOperationResponse
		if err := json.Unmarshal(body, &operation); err != nil || code != http.StatusOK {
			t.Fatalf("managed Commit operation API: %d %s (%v)", code, body, err)
		}
		if operation.State == "completed" {
			owned, err := store.ExclusiveOperationByID(t.Context(), account, id)
			if err != nil || owned.State != "completed" || owned.CompletedAt == nil || owned.IncarnationID == "" {
				t.Fatalf("managed Commit completion lacks owner evidence: %+v (%v)", owned, err)
			}
			return owned
		}
		if operation.State == "failed" || operation.State == "cancelled" || operation.State == "expired" || time.Now().After(deadline) {
			h.DumpLogs(t)
			t.Fatalf("managed Commit operation did not complete: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func commitOperationEnvironment(t *testing.T, env ...string) []string {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "commit-schedd.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := json.Marshal(map[string]string{"schedd": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))})
	if err != nil {
		t.Fatal(err)
	}
	return append(env, "FAAS_INTERNAL_SVC_KEY_PATH="+keyPath, "FAAS_INTERNAL_SVC_PUBKEYS="+string(keys))
}

func commitOperationInstance(t *testing.T, operation state.ExclusiveOperation) string {
	t.Helper()
	parts := strings.Split(operation.IncarnationID, "/")
	if len(parts) != 3 || parts[0] == "" {
		t.Fatalf("invalid managed operation incarnation: %q", operation.IncarnationID)
	}
	return parts[0]
}

func waitCommitSourceHealth(t *testing.T, h *e2etest.Harness, key, source, want string, after time.Time) api.CommitSourceResponse {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, code := doReq(t, h, key, http.MethodGet, "/v1/commit-sources/"+source, nil)
		var health api.CommitSourceResponse
		if code != http.StatusOK || json.Unmarshal(body, &health) != nil {
			t.Fatalf("source health API: %d %s", code, body)
		}
		if health.RelayStatus == want && health.LastCheckedAt != nil && health.LastCheckedAt.After(after) {
			return health
		}
		if time.Now().After(deadline) {
			t.Fatalf("source health did not reach fresh %q: %+v", want, health)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCommitProducerProcess(t *testing.T) {
	if os.Getenv("GREGALE_COMMIT_PRODUCER") != "1" {
		return
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = pgx.Identifier{os.Getenv("GREGALE_COMMIT_SCHEMA")}.Sanitize()
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := os.Getenv("GREGALE_COMMIT_EVENT")
	if _, err := tx.Exec(ctx, `INSERT INTO orders(id) VALUES($1::uuid)`, id); err != nil {
		t.Fatal(err)
	}
	if err := commitwork.Insert(ctx, tx, commitwork.Event{ID: id, Type: "order.created", Data: []byte(`{"order_id":"` + id + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("committed")
	for {
		time.Sleep(time.Hour)
	}
}
