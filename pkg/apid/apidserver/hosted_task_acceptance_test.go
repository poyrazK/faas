//go:build !no_pg

package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/bindingtest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

const (
	acceptanceNamespace   = "hosted-task-acceptance"
	acceptanceOwnerKey    = "private-task-owner-acceptance-sentinel"
	acceptanceSource      = "private-task-source-acceptance-sentinel"
	acceptanceMigration   = "TASK_ACCEPTANCE_MIGRATION_URL"
	acceptanceDatabase    = "DATABASE_URL"
	acceptanceGetBindings = "gregale_get_uploaded_task_tool_bindings"
)

func TestHostedTaskNativeAcceptance(t *testing.T) {
	f := newHostedTaskAcceptance(t)
	session, bridge := f.connect(t, api.MCPProtocolVersion)
	catalog, err := session.ListTools(t.Context(), nil)
	mustHostedTaskAcceptance(t, err)
	visible := map[string]bool{}
	for _, tool := range catalog.Tools {
		visible[tool.Name] = true
	}
	if !visible[acceptanceGetBindings] {
		t.Fatal("agent catalog omitted the binding receipt tool")
	}
	for _, tool := range []string{
		"gregale_decide_uploaded_task_tool_deploy",
		"gregale_select_uploaded_task_tool_bindings",
		"gregale_check_uploaded_task_tool_bindings",
	} {
		if visible[tool] {
			t.Fatalf("owner operation in the agent catalog: %s", tool)
		}
	}

	first := f.prepare(t, session, bridge, "expiry")
	approved, passed := f.qualify(t, first)
	args := map[string]any{"intent_id": first.ID}
	getHostedTaskTool(t, session, "gregale_get_uploaded_task_tool_deploy", args, &first)
	assertHostedTaskEqual(t, approved.ReviewStatus, first.ReviewStatus)
	assertHostedTaskEqual(t, approved.DecidedAt, first.DecidedAt)

	mustHostedTaskAcceptance(t, session.Close())
	bridge.Close()
	// A new hosted process and a different supported MCP protocol must see the
	// same native receipt without any of the original grant's upload state.
	session, bridge = f.connect(t, api.MCPLegacyProtocolVersion)
	var got api.HostedTaskBindingVerification
	getHostedTaskTool(t, session, acceptanceGetBindings, args, &got)
	assertHostedTaskEqual(t, passed, got)
	if count := f.connections.Load(); count != 4 {
		t.Fatalf("reconnect repeated the probe: %d connections", count)
	}

	// Shorten the persisted observation's lifespan to exercise expiration
	// without sleeping through the production one-minute freshness window.
	_, err = f.pool.Exec(t.Context(), `UPDATE hosted_task_binding_qualifications
        SET payload=jsonb_set(payload,'{Public,valid_until}',to_jsonb($3::text))
        WHERE account_id=$1 AND id=$2`, f.account.ID, first.ID, passed.CheckedAt.Add(time.Microsecond).Format(time.RFC3339Nano))
	mustHostedTaskAcceptance(t, err)
	getHostedTaskTool(t, session, acceptanceGetBindings, args, &got)
	assertHostedTaskEqual(t, "expired", got.Status)
	assertHostedTaskEqual(t, passed.BindingSHA256, got.BindingSHA256)
	assertHostedTaskReceipt(t, got)

	second := f.prepare(t, session, bridge, "rotation")
	_, before := f.qualify(t, second)
	// Record a new ready catalog generation, then deliver it through the native secret writer.
	rotated := f.webSecret
	rotated.ManagedCredentialGeneration++
	rotated.ManagedCredentialRef = "binding:" + uuid.NewString()
	_, err = f.pool.Exec(t.Context(), `UPDATE managed_postgres_bindings SET credential_generation=$3, credential_ref=$4
        WHERE account_id=$1 AND id=$2`, f.account.ID, rotated.ManagedPostgresBindingID,
		rotated.ManagedCredentialGeneration, rotated.ManagedCredentialRef)
	mustHostedTaskAcceptance(t, err)
	rotated.Ciphertext = f.seal(t, rotated.Key, strings.Replace(f.webDSN, "binding-fixture-only", "rotated-credential-sentinel", 1))
	mustHostedTaskAcceptance(t, f.store.PutManagedPostgresSecret(t.Context(), rotated))
	getHostedTaskTool(t, session, acceptanceGetBindings, map[string]any{"intent_id": second.ID}, &got)
	assertHostedTaskEqual(t, "stale", got.Status)
	assertHostedTaskEqual(t, before.BindingSHA256, got.BindingSHA256)
	assertHostedTaskReceipt(t, got)
	staleReplay, err := f.owner.CheckHostedTaskBindings(t.Context(), second.ID, api.HostedTaskBindingCheck{BindingSHA256: before.BindingSHA256})
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, got, staleReplay)
	if count := f.connections.Load(); count != 8 {
		t.Fatalf("expected eight connections for two probes, got %d", count)
	}
	if f.cliCalls.Load() != 0 {
		t.Fatal("acceptance journey invoked the CLI")
	}
	for _, app := range f.apps {
		deployments, err := f.store.ListDeploymentsForApp(t.Context(), app.ID, 1, 0)
		mustHostedTaskAcceptance(t, err)
		if len(deployments) != 0 {
			t.Fatalf("unexpected deployment for %s", app.Slug)
		}
	}
}

type hostedTaskAcceptance struct {
	pool                  *pgxpool.Pool
	store                 *state.PgStore
	account               state.Account
	apps                  map[string]state.App
	owner, agent          *api.Client
	apidURL, agentToken   string
	runtime               *age.X25519Identity
	webSecret             state.AppSecret
	webDSN                string
	connections, cliCalls atomic.Int32
}

func newHostedTaskAcceptance(t *testing.T) *hostedTaskAcceptance {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	f := &hostedTaskAcceptance{pool: pgtest.OpenMigrated(t), apps: make(map[string]state.App)}
	mustHostedTaskAcceptance(t, db.MigrateUp(ctx, f.pool))
	f.store = state.NewPgStore(f.pool)
	var err error
	f.account, err = f.store.CreateAccount(ctx, "hosted-task-"+uuid.NewString()+"@example.com", api.PlanPro)
	mustHostedTaskAcceptance(t, err)
	for _, role := range []string{"web", "worker", "observer"} {
		app, err := f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: role, Type: state.AppTypeApp,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		mustHostedTaskAcceptance(t, err)
		f.apps[role] = app
		mustHostedTaskAcceptance(t, f.store.UpsertAppEnv(ctx, f.account.ID, app.ID, "MCP_TASK_NAMESPACE", acceptanceNamespace))
	}
	f.runtime, err = age.GenerateX25519Identity()
	mustHostedTaskAcceptance(t, err)
	accountIdentity, err := age.GenerateX25519Identity()
	mustHostedTaskAcceptance(t, err)
	dsns, _, connect, _ := bindingtest.Postgres(t)
	f.seedBindings(t, dsns)
	executor := toolregistry.NewPrivateExecutor(f.store, toolregistry.PrivateExecutionOptions{
		SourceRetentionEnabled:     true,
		BindingVerificationEnabled: true,
		ConnectionRecipients: func() ([]*age.X25519Recipient, error) {
			return []*age.X25519Recipient{accountIdentity.Recipient()}, nil
		},
		ConnectionIdentities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{accountIdentity} },
		Identities:           func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{f.runtime} },
		BindingProbeConnect: func(ctx context.Context, config *pgx.ConnConfig) (*pgx.Conn, error) {
			f.connections.Add(1)
			return connect(ctx, config)
		},
	})
	t.Cleanup(executor.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := apidserver.NewServer(f.store, logger, "gregale.dev", apidserver.NoopNotifier{})
	srv.SetPrivateExecutorForTest(executor)
	apid := httptest.NewServer(srv.Handler())
	t.Cleanup(apid.Close)
	f.apidURL = apid.URL
	key := func(label string, scopes []string) string {
		plain, hash, err := api.GenerateAPIKey()
		mustHostedTaskAcceptance(t, err)
		_, err = f.store.CreateAPIKey(ctx, f.account.ID, hash, label, scopes)
		mustHostedTaskAcceptance(t, err)
		return plain
	}
	f.owner = api.NewClient(f.apidURL, key("acceptance-owner", api.ScopesAdminOnly))
	f.agentToken = key("acceptance-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	f.agent = api.NewClient(f.apidURL, f.agentToken)
	return f
}

func (f *hostedTaskAcceptance) seal(t *testing.T, key, value string) []byte {
	t.Helper()
	cipher, err := secretbox.SealOneBound(f.runtime.Recipient(), secretbox.Binding{AccountID: f.account.ID},
		key, value, api.HostedTaskBindingSecretMaxBytes)
	mustHostedTaskAcceptance(t, err)
	return cipher
}

func (f *hostedTaskAcceptance) seedBindings(t *testing.T, dsns [4]string) {
	t.Helper()
	dbID := uuid.NewString()
	_, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases
        (id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,state,provider_resource_id,observed_generation)
        VALUES ($1,$2,'task-acceptance','eu',16,'development','single_zone','test',$3,'ready','task-acceptance',1)`,
		dbID, f.account.ID, strings.Repeat("a", 64))
	mustHostedTaskAcceptance(t, err)
	for _, role := range []string{"web", "worker"} {
		const key = "MCP_TASK_OWNER_KEY"
		mustHostedTaskAcceptance(t, f.store.UpsertAppSecret(t.Context(), f.account.ID, f.apps[role].ID, key, f.seal(t, key, acceptanceOwnerKey)))
	}
	for i, binding := range []struct{ role, key, access string }{
		{"web", acceptanceDatabase, "read_write"},
		{"worker", acceptanceDatabase, "read_write"},
		{"worker", acceptanceMigration, "migration"},
		{"observer", acceptanceDatabase, "read_only"},
	} {
		id := uuid.NewString()
		ref := "binding:" + id
		app := f.apps[binding.role]
		_, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_bindings
            (id,account_id,database_account_id,database_id,app_id,scope,environment_key,provider_identity_id,credential_ref,state,access)
            VALUES ($1,$2,$2,$3,$4,'default',$5,'test',$6,'ready',$7)`,
			id, f.account.ID, dbID, app.ID, binding.key, ref, binding.access)
		mustHostedTaskAcceptance(t, err)
		secret := state.AppSecret{AccountID: f.account.ID, AppID: app.ID, Scope: api.DefaultEnvScope,
			Key: binding.key, Ciphertext: f.seal(t, binding.key, dsns[i]), ManagedPostgresBindingID: id,
			ManagedPostgresDatabaseID: dbID, ManagedCredentialRef: ref, ManagedCredentialGeneration: 1, ManagedPostgresAccess: binding.access}
		mustHostedTaskAcceptance(t, f.store.PutManagedPostgresSecret(t.Context(), secret))
		if i == 0 {
			f.webSecret, f.webDSN = secret, dsns[i]
		}
	}
}

func (f *hostedTaskAcceptance) connect(t *testing.T, protocol string) (*mcp.ClientSession, *httptest.Server) {
	t.Helper()
	uploadDir := t.TempDir()
	mustHostedTaskAcceptance(t, os.Chmod(uploadDir, 0o755))
	bridge := httptest.NewUnstartedServer(nil)
	resource := "https://" + bridge.Listener.Addr().String() + "/mcp"
	handler, err := mcpmanagement.NewHTTPHandler(mcpmanagement.HTTPOptions{
		APIBase: f.apidURL, ResourceURL: resource,
		RedirectURIs: []string{"http://127.0.0.1/callback"}, Hosted: true, AgentKeys: true,
		AgentKeyToolsetSelection: true, Toolsets: []string{mcpmanagement.ToolsetBuildTools},
		AllowWrites: true, AllowToolPublishing: true, UploadDir: uploadDir,
		CLIRunner: func(string) mcpmanagement.LocalCLI {
			return func(context.Context, []string, []byte) (mcpmanagement.LocalCLIResult, error) {
				f.cliCalls.Add(1)
				return mcpmanagement.LocalCLIResult{}, errors.New("unexpected CLI execution")
			}
		},
	})
	if err != nil {
		bridge.Close()
		t.Fatal(err)
	}
	info, err := os.Stat(uploadDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		bridge.Close()
		t.Fatalf("bridge did not make the upload directory private: %v, %v", info, err)
	}
	bridge.Config.Handler = handler
	bridge.StartTLS()
	t.Cleanup(bridge.Close)
	client := *bridge.Client()
	client.Transport = hostedTaskAgentTransport{base: client.Transport, token: f.agentToken, toolset: mcpmanagement.ToolsetBuildTools}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "hosted-task-acceptance", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: resource, HTTPClient: &client, DisableStandaloneSSE: true},
			&mcp.ClientSessionOptions{ProtocolVersion: protocol})
	mustHostedTaskAcceptance(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, bridge
}

type hostedTaskAgentTransport struct {
	base           http.RoundTripper
	token, toolset string
}

func (tr hostedTaskAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := req.Clone(req.Context())
	next.Header.Set("Authorization", "Bearer "+tr.token)
	next.Header.Set("Gregale-MCP-Toolsets", tr.toolset)
	return tr.base.RoundTrip(next)
}

func (f *hostedTaskAcceptance) prepare(t *testing.T, session *mcp.ClientSession, bridge *httptest.Server, purpose string) api.HostedTaskSourceIntent {
	t.Helper()
	archive := hostedTaskArchive(t)
	sources := func() map[string]any {
		return map[string]any{
			"web":                 uploadHostedTaskSource(t, session, bridge, f.apps["web"].Slug, archive),
			"worker":              uploadHostedTaskSource(t, session, bridge, f.apps["worker"].Slug, archive),
			"observer_app":        f.apps["observer"].Slug,
			"observer_metric_app": f.apps["worker"].Slug,
		}
	}
	var review api.HostedTaskSourceReview
	getHostedTaskTool(t, session, "gregale_plan_uploaded_task_tool_deploy", sources(), &review)
	assertHostedTaskEqual(t, f.account.ID, review.AccountID)
	if len(review.Apps) != 3 {
		t.Fatalf("review has %d application pins, want three", len(review.Apps))
	}
	params := sources()
	id := toolpublication.Digest([]byte(f.account.ID + ":" + purpose))
	params["intent_id"], params["expected_review_sha256"] = id, review.Fingerprint
	var retained api.HostedTaskSourceIntent
	getHostedTaskTool(t, session, "gregale_prepare_uploaded_task_tool_deploy", params, &retained)
	assertHostedTaskEqual(t, id, retained.ID)
	assertHostedTaskEqual(t, review.Fingerprint, retained.ReviewSHA256)
	assertHostedTaskEqual(t, "retained", retained.SourceState)
	assertHostedTaskEqual(t, 15*time.Minute, retained.ExpiresAt.Sub(retained.CreatedAt))
	if retained.ReadyToApply || retained.BindingsVerified || !retained.OwnerAuthorizationRequired {
		t.Fatal("source retention receipt granted execution authority")
	}
	var replay api.HostedTaskSourceIntent
	// A lost response can be recovered after the one-use uploads are gone.
	getHostedTaskTool(t, session, "gregale_prepare_uploaded_task_tool_deploy", params, &replay)
	assertHostedTaskEqual(t, retained, replay)
	return retained
}

func (f *hostedTaskAcceptance) qualify(t *testing.T, source api.HostedTaskSourceIntent) (api.HostedTaskSourceIntent, api.HostedTaskBindingVerification) {
	t.Helper()
	ctx := t.Context()
	decision := api.HostedTaskSourceDecision{IntentSHA256: source.IntentSHA256, ReviewSHA256: source.ReviewSHA256, Decision: "approve"}
	_, err := f.agent.DecideHostedTaskSourceIntent(ctx, source.ID, decision)
	var forbidden *api.APIError
	if !errors.As(err, &forbidden) || forbidden.Problem.Status != http.StatusForbidden {
		t.Fatalf("agent decision should be forbidden: %v", err)
	}
	approved, err := f.owner.DecideHostedTaskSourceIntent(ctx, source.ID, decision)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, "approved", approved.ReviewStatus)
	assertHostedTaskEqual(t, source.ExpiresAt, approved.ExpiresAt)
	if approved.ReadyToApply || approved.BindingsVerified || !approved.OwnerAuthorizationRequired {
		t.Fatal("source approval receipt granted execution authority")
	}
	approvedReplay, err := f.owner.DecideHostedTaskSourceIntent(ctx, source.ID, decision)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, approved, approvedReplay)

	selected, err := f.owner.SelectHostedTaskBindings(ctx, source.ID, api.HostedTaskBindingSelection{
		IntentSHA256: approved.IntentSHA256, ReviewSHA256: approved.ReviewSHA256, Namespace: acceptanceNamespace,
		WebDatabaseRef: api.SecretRefPrefix + acceptanceDatabase, WorkerDatabaseRef: api.SecretRefPrefix + acceptanceDatabase,
		MigrationDatabaseRef: api.SecretRefPrefix + acceptanceMigration, ObserverDatabaseRef: api.SecretRefPrefix + acceptanceDatabase,
	})
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, "selected", selected.Status)
	assertHostedTaskReceipt(t, selected)
	check := api.HostedTaskBindingCheck{BindingSHA256: selected.BindingSHA256}
	countBefore := f.connections.Load()
	passed, err := f.owner.CheckHostedTaskBindings(ctx, source.ID, check)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, "passed", passed.Status)
	if !tasktoolbinding.Passed(passed.Checks) {
		t.Fatal("passing receipt omitted catalog checks")
	}
	assertHostedTaskEqual(t, source.ExpiresAt, passed.ExpiresAt)
	if passed.CheckedAt == nil || passed.ValidUntil == nil {
		t.Fatal("passing evidence omitted its validity window")
	}
	if validity := passed.ValidUntil.Sub(*passed.CheckedAt); validity <= 0 || validity > api.HostedTaskBindingEvidenceTTL {
		t.Fatalf("unexpected evidence lifetime: %s", validity)
	}
	assertHostedTaskReceipt(t, passed)
	replay, err := f.owner.CheckHostedTaskBindings(ctx, source.ID, check)
	mustHostedTaskAcceptance(t, err)
	assertHostedTaskEqual(t, passed, replay)
	if count := f.connections.Load() - countBefore; count != 4 {
		t.Fatalf("expected one four-role probe, got %d connections", count)
	}
	return approved, passed
}

func assertHostedTaskReceipt(t *testing.T, receipt api.HostedTaskBindingVerification) {
	t.Helper()
	if receipt.ReadyToApply || receipt.BindingsVerified || !receipt.OwnerAuthorizationRequired {
		t.Fatal("binding receipt granted execution authority")
	}
}

func getHostedTaskTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	getHostedAcceptanceTool(t, session, name, args, out, acceptanceSource, acceptanceOwnerKey, acceptanceMigration,
		"binding-fixture-only", "rotated-credential-sentinel", "fixture.example.com", "postgres://",
		"\"ciphertext\"", "\"pins\"", "\"claim\"", "\"selection\"", "\"sources\"")
}

func getHostedAcceptanceTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any, privateFields ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, err := json.Marshal(result)
	mustHostedTaskAcceptance(t, err)
	for _, private := range privateFields {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("%s returned private source or binding data", name)
		}
	}
	if result.IsError {
		t.Fatalf("%s: %s", name, raw)
	}
	structured, err := json.Marshal(result.StructuredContent)
	mustHostedTaskAcceptance(t, err)
	mustHostedTaskAcceptance(t, json.Unmarshal(structured, out))
}

func uploadHostedTaskSource(t *testing.T, session *mcp.ClientSession, bridge *httptest.Server, app string, archive []byte) map[string]string {
	t.Helper()
	var upload struct {
		ID  string `json:"upload_id"`
		URL string `json:"upload_url"`
	}
	getHostedTaskTool(t, session, "gregale_create_source_upload", map[string]any{"app": app}, &upload)
	if upload.ID == "" || !strings.HasPrefix(upload.URL, bridge.URL+"/mcp/uploads/") {
		t.Fatal("invalid upload receipt")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, upload.URL, bytes.NewReader(archive))
	mustHostedTaskAcceptance(t, err)
	req.Header.Set("Content-Type", "application/gzip")
	res, err := bridge.Client().Do(req)
	mustHostedTaskAcceptance(t, err)
	defer res.Body.Close()
	assertHostedTaskEqual(t, http.StatusCreated, res.StatusCode)
	return map[string]string{"app": app, "upload_id": upload.ID}
}

func hostedTaskArchive(t *testing.T) []byte {
	t.Helper()
	enabled := true
	cfg := mcphosting.Config{
		Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{},
		Auth: mcphosting.AuthConfig{Mode: "open", ToolScopes: mcphosting.ScopePolicy{"report": {}}},
		Tasks: &mcphosting.TasksConfig{Enabled: &enabled, DatabaseURLEnv: acceptanceDatabase,
			OwnerKeyEnv: "MCP_TASK_OWNER_KEY", NamespaceEnv: "MCP_TASK_NAMESPACE", TTLSeconds: 3600, WorkerConcurrency: 2},
	}
	mustHostedTaskAcceptance(t, cfg.Validate())
	config, err := json.Marshal(cfg)
	mustHostedTaskAcceptance(t, err)
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gzipWriter)
	for _, file := range []struct{ name, body string }{
		{mcphosting.ConfigFile, string(config)},
		{"package.json", `{"scripts":{"start":"node server.js","preinstall":"exit 93"}}`},
		{"server.js", fmt.Sprintf("throw new Error(%q);", acceptanceSource)},
		{"tasks-release.js", "throw new Error('fixture migrations must never execute');"},
	} {
		mustHostedTaskAcceptance(t, archive.WriteHeader(&tar.Header{Name: file.name, Mode: 0600, Size: int64(len(file.body)), Typeflag: tar.TypeReg}))
		_, err := io.WriteString(archive, file.body)
		mustHostedTaskAcceptance(t, err)
	}
	mustHostedTaskAcceptance(t, archive.Close())
	mustHostedTaskAcceptance(t, gzipWriter.Close())
	return buffer.Bytes()
}

func mustHostedTaskAcceptance(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertHostedTaskEqual(t *testing.T, want, got any) {
	t.Helper()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("hosted Task mismatch (-want +got):\n%s", diff)
	}
}
