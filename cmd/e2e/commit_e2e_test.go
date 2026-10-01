package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// The real API, scheduler (including its managed relay), gateway, and two
// PostgreSQL databases are exercised. Customer connections require verified TLS.
// VMMD is the existing normal-path protocol fixture; native VM acceptance is
// a separate gate and cannot be inferred from this test.
func TestE2E_CommitProducerDeathReachesCompletedInvocation(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required")
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "commit-worker", api.PlanHobby, "FAAS_COMMIT_API_ENABLED=true")
	if f == nil {
		t.Fatal("commit acceptance requires the PostgreSQL harness")
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "commit-worker")
	f.vmmd.SetVersion(instance.ID, "commit-worker")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:commit-worker\n", 10*time.Second)
	runCommitProducerHandoff(t, f.h, f.store, f.key, f.app.ID, "orders")
}

func runCommitProducerHandoff(t *testing.T, h *e2etest.Harness, store *state.PgStore, key, appID, sourceName string) api.Invocation {
	t.Helper()
	before, err := store.ListInvocationsForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCount := len(before)
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
	src, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: account.AccountID, AppID: appID, Name: sourceName})
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
	body, status := doReq(t, h, key, http.MethodPatch, "/v1/commit-sources/"+src.ID, map[string]bool{"enabled": true})
	if status != http.StatusOK {
		t.Fatalf("resume source: %d %s", status, body)
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
	completed := pollUntilCompleted(t, h, key, receipt.InvocationID, 20*time.Second)
	if completed.State != string(state.InvocationCompleted) {
		t.Fatalf("state=%s error=%s", completed.State, completed.LastError)
	}
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
	if err != nil || len(invocations) != beforeCount+1 {
		t.Fatalf("logical invocations=%d err=%v", len(invocations), err)
	}
	if _, err := store.CommitReceiptByEvent(ctx, src.AccountID, src.ID, rolledBackID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("scheduler accepted a rolled-back event: %v", err)
	}
	body, status = doReq(t, h, key, http.MethodGet, "/v1/commit-sources/"+src.ID+"/events/"+eventID, nil)
	if status != http.StatusOK {
		t.Fatalf("receipt API: %d %s", status, body)
	}
	body, status = doReq(t, h, key, http.MethodGet, "/v1/operations/"+receipt.InvocationID, nil)
	var operation api.CommitOperationResponse
	if err := json.Unmarshal(body, &operation); err != nil || status != http.StatusOK || operation.State != "completed" || operation.CompletedAt == nil || operation.EventID != eventID {
		t.Fatalf("completed operation API: %d %+v %v", status, operation, err)
	}
	return completed
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
