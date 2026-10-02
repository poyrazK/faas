package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Run the shipped CLI against real API and scheduler processes. In particular,
// credential registration must seal to a key the scheduler can actually open.
func TestE2E_CommitCLISourceLifecycle(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required")
	}
	cluster := pgtest.OpenTLSCluster(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fleetIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	identityPath, recipientPath := filepath.Join(tmp, "host.age"), filepath.Join(tmp, "fleet.age.pub")
	if err := secretbox.WriteHostKeyAtPath(identityPath, identity); err != nil {
		t.Fatal(err)
	}
	if err := secretbox.WriteHostKeyAtPath(filepath.Join(tmp, "fleet.age"), fleetIdentity); err != nil {
		t.Fatal(err)
	}
	if err := secretbox.WriteRecipientFile(recipientPath, fleetIdentity); err != nil {
		t.Fatal(err)
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "commit-cli", api.PlanHobby, commitOperationEnvironment(t,
		"FAAS_COMMIT_API_ENABLED=true", "FAAS_COMMIT_RELAY_ENABLED=true",
		"FAAS_FLEET_AGE_RECIPIENT_PATH="+recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH="+identityPath,
		"FAAS_COMMIT_DATABASE_HOSTS=localhost", "FAAS_COMMIT_DATABASE_CIDRS=127.0.0.1/32",
		"PGSSLROOTCERT="+cluster.CAPath)...)
	if f == nil {
		t.Fatal("Commit CLI acceptance requires the PostgreSQL harness")
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "commit-cli")
	f.vmmd.SetVersion(instance.ID, "commit-cli")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:commit-cli\n", 10*time.Second)
	cliPath := filepath.Join(tmp, "gregale")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", cliPath, "github.com/onebox-faas/faas/cmd/gregale")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Gregale CLI: %v\n%s", err, output)
	}
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, cliPath, append([]string{"--json", "commit"}, args...)...)
		command.Env = append(os.Environ(), "FAAS_API="+f.h.APIDURL, "FAAS_TOKEN="+f.key)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Commit CLI %s failed: %v\n%s", args[0], err, output)
		}
		if strings.Contains(string(output), "cli-private-password") || strings.Contains(string(output), "postgres://") {
			t.Fatal("Commit CLI exposed a database credential")
		}
		return output
	}
	var source api.CommitSourceResponse
	ctx := t.Context()
	app, err := f.store.AppByID(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{
		Name: "cli-orders", Scope: "account", Contention: "queue", MemberAppIDs: []string{f.app.ID}, LeaseSeconds: 15, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(run("add", "commit-cli", "--name", "cli-orders", "--operation-policy", "cli-orders"), &source); err != nil || source.ID == "" || source.OperationPolicy != "cli-orders" {
		t.Fatalf("CLI source registration: %+v (%v)", source, err)
	}
	run("pause", source.ID)
	if _, err := cluster.Admin.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `CREATE ROLE relay LOGIN PASSWORD 'cli-private-password'; GRANT SELECT,INSERT,UPDATE,DELETE ON public.gregale_outbox TO relay; GRANT SELECT ON public.gregale_commit_binding TO relay`); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, source.ID); err != nil {
		t.Fatal(err)
	}
	connectionFile := filepath.Join(tmp, "connection")
	if err := os.WriteFile(connectionFile, []byte(cluster.URL("relay", "cli-private-password")), 0600); err != nil {
		t.Fatal(err)
	}
	run("connection", source.ID, "--file", connectionFile)
	var repeated api.CommitSourceResponse
	if err := json.Unmarshal(run("add", "commit-cli", "--name", "cli-orders", "--operation-policy", "cli-orders"), &repeated); err != nil || repeated.ID != source.ID || repeated.Enabled {
		t.Fatalf("CLI source retry changed identity or paused state: %+v (%v)", repeated, err)
	}
	event := commitwork.Event{ID: uuid.NewString(), Type: "order.created", Data: json.RawMessage(`{"order_id":"cli"}`)}
	tx, err := cluster.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := commitwork.Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitReceiptByEvent(ctx, app.AccountID, source.ID, event.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paused CLI source accepted work: %v", err)
	}
	run("resume", source.ID)
	var receipt state.CommitReceipt
	deadline := time.Now().Add(35 * time.Second)
	for {
		receipt, err = f.store.CommitReceiptByEvent(ctx, app.AccountID, source.ID, event.ID)
		if err == nil {
			break
		}
		if !errors.Is(err, state.ErrNotFound) || time.Now().After(deadline) {
			t.Fatalf("CLI-configured managed relay did not accept event: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	completed := waitCommitOperationCompleted(t, f.h, f.store, f.key, app.AccountID, receipt.OperationID, 30*time.Second)
	if completed.State != "completed" || receipt.OperationID == "" || receipt.InvocationID != "" {
		t.Fatalf("CLI operation did not complete: %+v", completed)
	}
	var recovered api.CommitReceiptResponse
	if err := json.Unmarshal(run("receipt", source.ID, event.ID), &recovered); err != nil || recovered.ID != receipt.ID || recovered.OperationID != receipt.OperationID || recovered.InvocationID != "" {
		t.Fatalf("CLI receipt identity: %+v (%v)", recovered, err)
	}
	var operation api.CommitOperationResponse
	if err := json.Unmarshal(run("operation", receipt.OperationID), &operation); err != nil || operation.State != "completed" || operation.EventID != event.ID || operation.CompletedAt == nil {
		t.Fatalf("CLI completed operation: %+v (%v)", operation, err)
	}
	var info api.CommitSourceResponse
	if err := json.Unmarshal(run("info", source.ID), &info); err != nil || info.ID != source.ID || !info.Enabled {
		t.Fatalf("CLI source info: %+v (%v)", info, err)
	}
}
