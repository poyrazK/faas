//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/state"
)

func runNativeCommitConsumerCrashRecovery(t *testing.T, h *e2etest.Harness, sqlIP string) {
	t.Helper()
	defer h.DumpLogs(t)
	cluster := pgtest.OpenTLSCluster(t)
	const slug = "commit-consumer-crash"
	const consumerRole = "commit_consumer"
	password := uuid.NewString()
	if _, err := cluster.Admin.Exec(t.Context(), `CREATE ROLE commit_consumer LOGIN PASSWORD '`+password+`';
		CREATE TABLE processed_events(source text NOT NULL, event_id uuid NOT NULL, PRIMARY KEY(source, event_id));
		CREATE TABLE business_effects(total integer NOT NULL);
		INSERT INTO business_effects VALUES (0);
		CREATE TABLE deliveries(source text NOT NULL, event_id uuid NOT NULL, operation_id uuid NOT NULL,
			generation bigint NOT NULL, process_id uuid NOT NULL, applied boolean NOT NULL);
		GRANT SELECT, INSERT ON processed_events TO commit_consumer;
		GRANT INSERT ON deliveries TO commit_consumer;
		GRANT SELECT, UPDATE ON business_effects TO commit_consumer;`); err != nil {
		t.Fatal("initialize disposable consumer schema")
	}
	// The host permits TCP 443 from guests to the dedicated fixture address.
	// The proxy forwards the PostgreSQL TLS handshake unchanged.
	proxy := postgresfixture.OpenProxy(t, cluster.Admin, sqlIP, 443)
	dsn, err := url.Parse(cluster.URL(consumerRole, password))
	if err != nil {
		t.Fatal("build consumer URL")
	}
	dsn.Host = net.JoinHostPort(proxy.Host, "443")
	ca, err := os.ReadFile(cluster.CAPath)
	if err != nil {
		t.Fatal("read disposable consumer CA")
	}

	executable := filepath.Join(t.TempDir(), "commit-consumer")
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-p", "1", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", executable, "../../pkg/e2etest/testdata/commitconsumer")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile Firecracker consumer: %v\n%s", err, output)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, map[string]string{
		"Dockerfile":      "FROM scratch\nCOPY --chmod=0755 commit-consumer /commit-consumer\nCOPY --chmod=0644 consumer-ca.pem /consumer-ca.pem\nEXPOSE 8080\nCMD [\"/commit-consumer\"]\n",
		"commit-consumer": string(binary),
		"consumer-ca.pem": string(ca),
		"gregale.yaml":    "hosting:\n  port: 8080\n  health: /healthz\n",
	})

	key := h.SeedAccount(t.Context(), api.PlanPro, slug)
	requireAuthn := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RAMMB: 256, RequireAuthn: &requireAuthn}); status != http.StatusCreated {
		t.Fatalf("create consumer app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	appURL := "/v1/apps/" + slug
	t.Cleanup(func() {
		if status := statusOnly(t, h, key, http.MethodPost, appURL+"/park", nil); status != http.StatusNoContent {
			t.Errorf("park consumer app: status=%d", status)
		}
	})
	destinations := []string{sqlIP + "/32"}
	policy := api.UpdateAppRequest{EgressAllowlist: &destinations, PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}
	if status := statusOnly(t, h, key, http.MethodPatch, appURL, policy); status != http.StatusOK {
		t.Fatalf("configure consumer app SQL egress: status=%d", status)
	}
	if status := statusOnly(t, h, key, http.MethodPut, appURL+"/secrets/COMMIT_CONSUMER_DATABASE_URL", api.PutAppSecretRequest{Value: dsn.String()}); status != http.StatusOK {
		t.Fatalf("seal consumer database URL: status=%d", status)
	}
	setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
	body, status := postMultipartDeployment(t, h, key, slug, archive, true, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy consumer: status=%d body=%s", status, body)
	}
	deploymentID, _ := parseQueuedDeployment(t, body)
	deployCtx, cancelDeploy := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancelDeploy()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, h.Pool, deploymentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal(err)
	}
	if _, err := waitForSourceAppParked(t.Context(), h, appID, 25*time.Second); err != nil {
		t.Fatal(err)
	}

	store := state.NewPgStore(h.Pool)
	var producer commitConsumerLoadFixture
	completed := runCommitProducerHandoff(t, h, store, key, appID, slug, commitHandoffOptions{LoadFixture: &producer})
	var result struct {
		Recovered bool `json:"recovered"`
	}
	if err := json.Unmarshal(completed.Result, &result); err != nil || !result.Recovered || completed.Attempts < 2 {
		t.Fatalf("consumer retry did not complete: attempts=%d recovered=%t decode=%v", completed.Attempts, result.Recovered, err)
	}
	operation, err := store.CommitOperationByID(t.Context(), completed.AccountID, completed.ID)
	if err != nil || operation.ReceiptID == "" || operation.SourceID != producer.source.ID || operation.CompletedAt == nil {
		t.Fatalf("Commit operation identity: %+v (%v)", operation, err)
	}
	receiptBody, status := doReq(t, h, key, http.MethodGet, "/v1/commit-sources/"+operation.SourceID+"/events/"+operation.EventID, nil)
	var receipt api.CommitReceiptResponse
	if err := json.Unmarshal(receiptBody, &receipt); err != nil || status != http.StatusOK ||
		receipt.ID != operation.ReceiptID || receipt.SourceID != operation.SourceID || receipt.EventID != operation.EventID || receipt.OperationID != completed.ID {
		t.Fatalf("receipt identity after consumer recovery: status=%d receipt=%+v decode=%v", status, receipt, err)
	}
	instanceID := commitOperationInstance(t, completed)
	var instanceOwned bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM instances WHERE id=$1 AND app_id=$2 AND deployment_id=$3)`,
		instanceID, appID, deploymentID).Scan(&instanceOwned); err != nil || !instanceOwned {
		t.Fatalf("completed Operation instance %s does not belong to the consumer deployment: %v", instanceID, err)
	}
	var deliveryCount, matchingCount, processCount, markers, effects int
	var firstApplied, replayApplied bool
	var firstGeneration, replayGeneration int64
	if err := cluster.Admin.QueryRow(t.Context(), `
		SELECT count(*), count(*) FILTER (WHERE source=$1 AND event_id=$2 AND operation_id=$3),
			count(DISTINCT process_id), (SELECT count(*) FROM processed_events WHERE source=$1 AND event_id=$2),
			(SELECT total FROM business_effects),
			(array_agg(applied ORDER BY generation))[1], (array_agg(applied ORDER BY generation))[2],
			min(generation), max(generation)
		FROM deliveries`, "gregale.commit."+operation.SourceID, operation.EventID, completed.ID).
		Scan(&deliveryCount, &matchingCount, &processCount, &markers, &effects, &firstApplied, &replayApplied, &firstGeneration, &replayGeneration); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 2 || matchingCount != 2 || processCount != 2 || markers != 1 || effects != 1 ||
		!firstApplied || replayApplied || firstGeneration >= replayGeneration || replayGeneration != completed.Generation {
		t.Fatalf("consumer recovery: deliveries=%d matching=%d processes=%d markers=%d effects=%d applied=%t/%t generations=%d/%d completed_generation=%d",
			deliveryCount, matchingCount, processCount, markers, effects, firstApplied, replayApplied, firstGeneration, replayGeneration, completed.Generation)
	}
	t.Logf("consumer recovered after commit-before-reply exit: 2 guest processes, 2 deliveries, 1 receipt, 1 Operation, 1 business effect (attempts=%d)", completed.Attempts)
}
