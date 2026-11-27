//go:build metal

package runlifecycle_test

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type reportSetupReceipt struct {
	DeploymentID string                 `json:"deployment_id"`
	EndpointID   string                 `json:"endpoint_id"`
	TaskPrefix   string                 `json:"task_prefix"`
	Tasks        []api.ExecutionTaskRef `json:"tasks"`
	ElapsedMS    int64                  `json:"elapsed_ms"`
	Verification struct {
		RequestID string `json:"request_id"`
		Orders    int    `json:"orders"`
		Units     int    `json:"units"`
		Cents     int    `json:"revenue_cents"`
		Currency  string `json:"currency"`
		ElapsedMS int64  `json:"elapsed_ms"`
	} `json:"verification"`
}

type reportSetupIdentity struct {
	DeploymentID string `json:"deployment_id"`
	ThreadID     string `json:"thread_id"`
	PipelineID   string `json:"pipeline_id"`
	RunID        string `json:"run_id"`
	DeliveryID   string `json:"delivery_id"`
	ReportSHA256 string `json:"report_sha256"`
	ReportBytes  int    `json:"report_bytes"`
}

type hostedReportStarter struct {
	node, token string
	env         []string
}

func configureHostedReportStarter(t *testing.T, f *runFixture, store *state.PgStore,
	edge *httptest.Server, node, dsn, guestDSN, guestIP string) hostedReportStarter {
	t.Helper()
	ctx := t.Context()
	// The fixture uses a socket on the host and relays it to the guest.
	requireReport(t, f.client.SetSecretWithScopeAndClass(ctx, hostedReportApp, "DATABASE_URL", guestDSN, "", api.SecretClassEphemeral))
	requireReport(t, f.client.PutAppsSlugEnvKey(ctx, hostedReportApp, "GREGALE_API_URL", "http://"+guestIP))
	scratch := t.TempDir()
	ca := filepath.Join(scratch, "storage-ca.pem")
	requireReport(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edge.Certificate().Raw}), 0600))
	token := reportBackendToken(t, store, f.accountID)
	env := append(os.Environ(), "DATABASE_URL="+dsn, "GREGALE_API_URL="+f.h.APIDURL, "GREGALE_TOKEN="+token,
		"NODE_EXTRA_CA_CERTS="+ca, "XDG_STATE_HOME="+scratch, "XDG_CONFIG_HOME="+scratch,
		"PATH="+requiredHostedEnv(t, "FAAS_TEST_REPORT_SETUP_BIN")+":"+os.Getenv("PATH"))
	return hostedReportStarter{node: node, token: token, env: env}
}

func (s hostedReportStarter) run(t *testing.T, requestID string) reportSetupReceipt {
	t.Helper()
	args := []string{"../../../examples/gregale-run/scripts/setup-report-agent.mjs", "--app", hostedReportApp, "--route", "reports"}
	if requestID != "" {
		args = append(args, "--verify", requestID)
	}
	cmd := exec.CommandContext(t.Context(), s.node, args...)
	cmd.Env = s.env
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	requireReport(t, err)
	var receipt reportSetupReceipt
	requireReport(t, json.Unmarshal(raw, &receipt))
	if receipt.DeploymentID == "" || receipt.EndpointID == "" || receipt.TaskPrefix == "" || len(receipt.Tasks) != 2 {
		t.Fatal("setup did not return the deployed Report Agent and pinned tasks")
	}
	if requestID != "" {
		v := receipt.Verification
		if v.RequestID != requestID || v.Orders != 4 || v.Units != 8 || v.Cents != 6996 || v.Currency != "USD" ||
			v.ElapsedMS <= 0 || receipt.ElapsedMS <= v.ElapsedMS {
			t.Fatal("setup did not verify the sample report")
		}
	}
	return receipt
}

func hostedReportTriggerID(t *testing.T, f *runFixture) string {
	t.Helper()
	ctx := t.Context()
	app, err := f.client.GetApp(ctx, hostedReportApp)
	requireReport(t, err)
	triggers, err := f.client.GetTriggers(ctx, app.ID, api.TriggerKindRealtime)
	requireReport(t, err)
	if len(triggers) != 1 {
		t.Fatal("expected exactly one Report Agent trigger")
	}
	return triggers[0].ID
}

func exerciseHostedReportSetup(t *testing.T, f *runFixture, store *state.PgStore, appDB *pgx.Conn,
	object *reportObjectFixture, edge *httptest.Server, node, dsn, guestDSN, guestIP string, triggerID *string) {
	t.Helper()
	starter := configureHostedReportStarter(t, f, store, edge, node, dsn, guestDSN, guestIP)
	requestID := uuid.NewString()
	first := starter.run(t, requestID)
	*triggerID = hostedReportTriggerID(t, f)
	firstIdentity := readReportSetupIdentity(t, f, store, appDB, object, requestID, first, 1)
	f.runs[firstIdentity.RunID] = true
	checkHostedSettlements(t, f, store, first.EndpointID, *triggerID, firstIdentity.ThreadID)
	t.Logf("first verified setup report in %d ms, verification phase %d ms", first.ElapsedMS, first.Verification.ElapsedMS)

	replay := starter.run(t, requestID)
	replayIdentity := readReportSetupIdentity(t, f, store, appDB, object, requestID, replay, 2)
	checkHostedSettlements(t, f, store, replay.EndpointID, *triggerID, replayIdentity.ThreadID)
	if firstIdentity != replayIdentity || first.EndpointID != replay.EndpointID ||
		first.Tasks[0] != replay.Tasks[0] || first.Tasks[1] != replay.Tasks[1] {
		t.Fatal("repeated setup changed the Report Agent's durable identities")
	}
	result := map[string]any{"schema": "gregale.report-agent-setup/v1", "request_id": requestID,
		"first_setup_to_verified_report_ms": first.ElapsedMS, "first_sample_verification_ms": first.Verification.ElapsedMS,
		"replay_setup_to_verified_report_ms": replay.ElapsedMS, "replay_sample_verification_ms": replay.Verification.ElapsedMS,
		"orders": 4, "units": 8, "revenue_cents": 6996, "deployment_count": 1, "request_count": 1,
		"physical_runs": 1, "provider_writes": 1, "provider_reads": 2, "reused_report": true,
		"identity": firstIdentity}
	encoded, err := json.Marshal(result)
	requireReport(t, err)
	requireReport(t, os.WriteFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_MEASUREMENTS"), append(encoded, '\n'), 0600))
	t.Logf("repeated setup verified the original report in %d ms", replay.ElapsedMS)
}

func readReportSetupIdentity(t *testing.T, f *runFixture, store *state.PgStore, appDB *pgx.Conn,
	object *reportObjectFixture, requestID string, receipt reportSetupReceipt, reads int) reportSetupIdentity {
	t.Helper()
	ctx := t.Context()
	var requestCount int
	requireReport(t, appDB.QueryRow(ctx, "SELECT count(*) FROM report_requests").Scan(&requestCount))
	if requestCount != 1 {
		t.Fatal("setup did not retain exactly one request")
	}
	identity := reportSetupIdentity{DeploymentID: receipt.DeploymentID}
	requireReport(t, appDB.QueryRow(ctx, `
		SELECT r.thread_id::text, m.content #>> '{report,delivery,id}'
		FROM report_requests r JOIN agent_messages m ON m.thread_id=r.thread_id::text
		WHERE r.endpoint_id=$1 AND r.request_id=$2 AND m.role='tool'`, receipt.EndpointID, requestID).
		Scan(&identity.ThreadID, &identity.DeliveryID))
	identity.PipelineID = hostedReportPipelineID(t, f, receipt.EndpointID, identity.ThreadID)
	runs, err := store.ListExecutions(ctx, f.accountID, 10, 0)
	requireReport(t, err)
	if len(runs) != 1 || runs[0].Status != api.ExecutionStatusSucceeded {
		t.Fatal("setup did not retain exactly one successful physical Run")
	}
	identity.RunID = runs[0].ID
	deployments, err := f.client.ListAppDeploymentsAll(ctx, hostedReportApp)
	requireReport(t, err)
	if len(deployments) != 1 || deployments[0].ID != receipt.DeploymentID {
		t.Fatal("setup did not retain exactly one deployment")
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.writes != 1 || object.reads != reads || len(object.body) == 0 {
		t.Fatalf("unexpected private object operations, writes=%d reads=%d", object.writes, object.reads)
	}
	identity.ReportSHA256 = fmt.Sprintf("%x", sha256.Sum256(object.body))
	identity.ReportBytes = len(object.body)
	return identity
}
