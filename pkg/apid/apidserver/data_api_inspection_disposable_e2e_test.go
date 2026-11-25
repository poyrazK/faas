//go:build !no_pg

package apidserver

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspect"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// This uses the real CLI, authenticated control plane, durable state,
// scheduler and TLS PostgreSQL inspector. The vmmd/guest boundary is a fake;
// it does not claim Firecracker or real runtime credential delivery evidence.
type inspectionAcceptanceProvider struct {
	managedpostgres.Provider
	port       uint16
	trust      string
	resourceID string
}

func (p *inspectionAcceptanceProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{PostgresMajors: []int{17}, ServiceClasses: []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment}, Availability: []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone}, CredentialAccess: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialDataAPI}, PooledConnections: true}
}
func (p *inspectionAcceptanceProvider) DataAPIBindingTarget(_ context.Context, req managedpostgres.CredentialRequest, identity string, generation int64) (managedpostgres.DataAPIBindingTarget, error) {
	if !managedpostgres.ValidDataAPIBindingCredentialRequest(req, generation) || identity != "write_fixture" || req.ProviderResourceID != p.resourceID {
		return managedpostgres.DataAPIBindingTarget{}, managedpostgres.ErrInvalid
	}
	return managedpostgres.DataAPIBindingTarget{Host: "127.0.0.1", Port: p.port, Database: "inspection_data", Login: "write_fixture", TrustBundleSHA256: p.trust}, nil
}

type inspectionAcceptanceSink struct{ managedpostgres.CredentialSink }

type inspectionAcceptanceRouter struct {
	mu                      sync.Mutex
	mode, dsn, otherDSN     string
	ca, extraCA             []byte
	pool                    *pgxpool.Pool
	bindingID, deploymentID string
}

func (r *inspectionAcceptanceRouter) setMode(mode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = mode
}
func (r *inspectionAcceptanceRouter) RestoreAppTask(_ context.Context, _ string, spec sched.AppTaskRestoreSpec) (*sched.AppTaskRestoreOutcome, error) {
	return &sched.AppTaskRestoreOutcome{Instance: spec.Instance}, nil
}
func (r *inspectionAcceptanceRouter) Destroy(context.Context, string, string) error { return nil }
func (r *inspectionAcceptanceRouter) ExecuteAppTask(ctx context.Context, _, _ string, request apptaskproto.Request) (apptaskproto.Result, error) {
	r.mu.Lock()
	mode := r.mode
	r.mu.Unlock()
	failed := func() (apptaskproto.Result, error) {
		code := 1
		return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &code, FailureCode: "inspection_unavailable", FailureMessage: "Inspection unavailable"}, nil
	}
	if request.Version != apptaskproto.DataAPIInspectionVersion || mode == "older_guest" {
		return failed()
	}
	dsn, bundle := r.dsn, r.ca
	if mode == "wrong_target" {
		dsn = r.otherDSN
	}
	if mode == "wrong_ca" {
		bundle = append(append([]byte(nil), r.ca...), r.extraCA...)
	}
	observation, err := dataapiinspect.Run(ctx, dsn, request.Command[1], request.Command[2], bundle)
	if err != nil {
		return failed()
	}
	if mode == "rotate" {
		if _, err := r.pool.Exec(ctx, `UPDATE managed_postgres_bindings SET credential_generation=2 WHERE id=$1`, r.bindingID); err != nil {
			return apptaskproto.Result{}, err
		}
	}
	if mode == "deployment_change" {
		if _, err := r.pool.Exec(ctx, `UPDATE deployments SET traffic_percent=0 WHERE id=$1`, r.deploymentID); err != nil {
			return apptaskproto.Result{}, err
		}
	}
	output, err := json.Marshal(observation)
	if err != nil {
		return apptaskproto.Result{}, err
	}
	code := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &code, Stdout: output}, nil
}

func inspectionAcceptanceCA(t *testing.T, serial int64) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
}

func TestDataAPIInspectionDisposableCLIIntegration(t *testing.T) {
	if os.Getenv("GREGALE_DATA_API_HOSTED_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_DATA_API_HOSTED_ACCEPTANCE=1 for the disposable integration test")
	}
	bin, cli := os.Getenv("GREGALE_TEST_POSTGRES_BIN"), os.Getenv("GREGALE_INSPECTION_CLI_BIN")
	if !filepath.IsAbs(bin) || !filepath.IsAbs(cli) {
		t.Fatal("GREGALE_TEST_POSTGRES_BIN and GREGALE_INSPECTION_CLI_BIN must be absolute paths")
	}
	for _, key := range []string{"PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGOPTIONS", "PGSERVICE", "PGSERVICEFILE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	command := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(filepath.Join(bin, name), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
	}
	command("initdb", "-D", data, "-U", "postgres", "--auth=trust", "--no-locale", "--encoding=UTF8")
	ca, key := inspectionAcceptanceCA(t, 1)
	otherCA, _ := inspectionAcceptanceCA(t, 2)
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	options := fmt.Sprintf("-h 127.0.0.1 -p %d -c unix_socket_directories='' -c ssl=on -c ssl_cert_file=%s -c ssl_key_file=%s -c max_connections=30 -c shared_buffers=16MB", port, certPath, keyPath)
	command("pg_ctl", "-D", data, "-l", filepath.Join(dir, "postgres.log"), "-o", options, "-w", "start")
	t.Cleanup(func() {
		_ = exec.Command(filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "immediate", "-w", "stop").Run()
	})
	command("createdb", "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "inspection_data")
	fixtureSQL := `CREATE ROLE write_fixture LOGIN; CREATE ROLE other_fixture LOGIN; CREATE SCHEMA api; CREATE TABLE api.notes(id integer PRIMARY KEY, body text); ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY; CREATE POLICY fixture_write ON api.notes FOR ALL TO write_fixture,other_fixture USING (true) WITH CHECK (true); GRANT USAGE ON SCHEMA api TO write_fixture,other_fixture; GRANT SELECT,INSERT,UPDATE,DELETE ON api.notes TO write_fixture,other_fixture; INSERT INTO api.notes VALUES (1,'synthetic-row');`
	command("psql", "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "-d", "inspection_data", "-v", "ON_ERROR_STOP=1", "-c", fixtureSQL)
	t.Setenv("DATABASE_URL", fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port))
	t.Setenv("FAAS_SKIP_PG_TESTS", "")
	t.Setenv(pgtest.UseTemplateDatabase, "")
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err = db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@inspection-e2e.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.MarkAccountEmailVerified(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(ctx, account.ID, hash, "inspection-e2e", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	slug := "inspection-" + uuid.NewString()[:8]
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("1", 64), TrafficPercent: 100, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	if err = st.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	trust, err := dataapiinspectioncommand.TrustBundleFingerprint(ca)
	if err != nil {
		t.Fatal(err)
	}
	provider := &inspectionAcceptanceProvider{port: uint16(port), trust: trust}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, ProvisioningEnabled: true, Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "inspection"}}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("eu")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, catalog, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, catalog, catalog, inspectionAcceptanceSink{}, managedpostgres.BindingServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	databaseID, bindingID, resourceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	provider.resourceID = resourceID
	if _, err = pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,'inspection','eu',17,'development','single_zone','test',$3)`, databaseID, account.ID, backend.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='ready',provider_resource_id=$2,observed_generation=desired_generation WHERE id=$1`, databaseID, resourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'default','DATABASE_URL','data_api')`, bindingID, account.ID, databaseID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='ready',provider_identity_id='write_fixture',credential_ref='test-private-ref' WHERE id=$1`, bindingID); err != nil {
		t.Fatal(err)
	}
	router := &inspectionAcceptanceRouter{dsn: fmt.Sprintf("postgresql://write_fixture:test-secret@127.0.0.1:%d/inspection_data?sslmode=verify-full", port), otherDSN: fmt.Sprintf("postgresql://other_fixture:test-secret@127.0.0.1:%d/inspection_data?sslmode=verify-full", port), ca: ca, extraCA: otherCA, pool: pool, bindingID: bindingID, deploymentID: dep.ID}
	taskBackend := sched.NewRoutedVmmdAppTaskBackend(router, func(_ context.Context, request sched.AppTaskRestoreRequest) (sched.ResolvedAppTaskRuntime, error) {
		return sched.ResolvedAppTaskRuntime{NodeID: "fixture", Spec: sched.AppTaskRestoreSpec{Instance: request.ID, DeploymentID: request.DeploymentID}}, nil
	})
	coordinator := sched.NewAppTaskCoordinator(st, taskBackend, sched.AppTaskCoordinatorConfig{Enabled: true, Owner: "inspection-e2e", PollInterval: 25 * time.Millisecond, SweepInterval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = coordinator.Run(workerCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	srv := newServer(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithAppTaskAPIEnabled(true).WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	httpServer := httptest.NewServer(srv.handler())
	defer httpServer.Close()
	t.Setenv("FAAS_API", httpServer.URL)
	t.Setenv("FAAS_TOKEN", token)
	var verifiedTask string
	for _, tc := range []struct{ mode, want, reason string }{
		{"verified", "verified", "provider_target_and_trust_matched"},
		{"wrong_target", "mismatch", "observed_target_differs"},
		{"wrong_ca", "unknown", "inspection_trust_mismatch"},
		{"rotate", "unknown", "binding_or_deployment_changed"},
		{"deployment_change", "unknown", "binding_or_deployment_changed"},
		{"older_guest", "unknown", "inspection_task_failed_or_incomplete"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			router.setMode(tc.mode)
			cliCtx, stopCLI := context.WithTimeout(ctx, 45*time.Second)
			defer stopCLI()
			cmd := exec.CommandContext(cliCtx, cli, "--json", "data-api", "write-readiness", "--database", databaseID, "--app", slug, "--binding", bindingID, "--schema", "api", "--table", "notes", "--operations", "insert")
			cmd.Env = os.Environ()
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("expected review exit 1, got %v: %s %s", err, stdout.String(), stderr.String())
			}
			var report struct {
				Status          string `json:"status"`
				TaskID          string `json:"task_id"`
				BindingIdentity struct {
					Status string `json:"status"`
					Reason string `json:"reason"`
				} `json:"binding_identity"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("invalid report %s: %v", stdout.String(), err)
			}
			if report.BindingIdentity.Status != tc.want || report.BindingIdentity.Reason != tc.reason {
				t.Fatalf("mode=%s report=%s stderr=%s", tc.mode, stdout.String(), stderr.String())
			}
			if tc.want != "unknown" && report.Status != "review_required" {
				t.Fatalf("expected review: %s", stdout.String())
			}
			for _, secret := range []string{"test-secret", "test-private-ref", "target_digest", "trust_bundle_sha256", "synthetic-row"} {
				if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
					t.Fatalf("private value leaked: %s", secret)
				}
			}
			if tc.mode == "verified" {
				verifiedTask = report.TaskID
			}
			if tc.mode == "rotate" {
				if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET credential_generation=1 WHERE id=$1`, bindingID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mode == "deployment_change" {
				if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent=100 WHERE id=$1`, dep.ID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	if verifiedTask == "" {
		t.Fatal("verified task was not returned")
	}
	readToken, readHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(ctx, account.ID, readHash, "inspection-read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	readClient := api.NewClient(httpServer.URL, readToken)
	generic, err := readClient.GetAppTask(ctx, slug, verifiedTask)
	if err != nil {
		t.Fatal(err)
	}
	if generic.StdoutTail != "" || generic.StderrTail != "" {
		t.Fatal("generic task leaked inspection output")
	}
	_, err = readClient.GetDataAPIInspection(ctx, slug, verifiedTask)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Status != http.StatusForbidden {
		t.Fatalf("missing postgres read scope: %v", err)
	}
	dataPool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://postgres@127.0.0.1:%d/inspection_data?sslmode=disable", port))
	if err != nil {
		t.Fatal(err)
	}
	defer dataPool.Close()
	var count int
	if err = dataPool.QueryRow(ctx, `SELECT count(*) FROM api.notes`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("inspection changed data: count=%d err=%v", count, err)
	}
}
