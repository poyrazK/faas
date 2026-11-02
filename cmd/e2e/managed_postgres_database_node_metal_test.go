//go:build metal

// adr: 1027 — a database node as real processes beside the control plane:
// vmmd in database mode, dbbackupd and pgproxyd, schedd's database
// scheduler from FAAS_SCHEDD_DATABASE_CONFIG, and apid with the native
// backend switched on behind the staging qualification gate. The customer
// path runs through the public API: provision, bind to an app, connect
// with the sealed DATABASE_URL through pgproxyd, idle park and wake on
// connect, point-in-time restore, delete.
//
// Every daemon runs on one host over unix sockets, whose permissions are
// the database RPCs' boundary there; the database node's mTLS over the
// overlay is not exercised. Nothing outside the workdir and the test's own
// cgroup is touched, so it may run on a shared metal host.
package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/firecracker"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// dbNodeImage is the PostgreSQL 17 image database-node vmmd builds its base
// from: the linux/amd64 manifest, as base staging refuses manifest lists.
const dbNodeImage = "docker.io/library/postgres@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"

const dbNodeSuffix = "db.e2e.test"

func TestManagedPostgresDatabaseNodeMetal(t *testing.T) {
	workdir, guestInit, bins := os.Getenv("FAAS_DBNODE_E2E_WORKDIR"), os.Getenv("FAAS_GUEST_INIT"), os.Getenv("FAAS_E2E_BIN_DIR")
	if workdir == "" || guestInit == "" || bins == "" || os.Geteuid() != 0 {
		t.Skip("set FAAS_DBNODE_E2E_WORKDIR, FAAS_GUEST_INIT and FAAS_E2E_BIN_DIR and run as root")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	// Jail relay sockets must fit in 107 bytes, so the run directory has a
	// short name (os.MkdirTemp adds up to ten digits).
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(workdir, "r"+hex.EncodeToString(suffix))
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	node := startDatabaseNode(t, run, filepath.Join(workdir, "bases"), guestInit, bins)

	// schedd reaches the node's vmmd; apid reaches schedd through a link
	// made once the harness has chosen schedd's socket. The idle timeout
	// outlasts pgproxyd's 30 s pool sweep, which touches a database while
	// it holds servers to it and closes idle ones, as production's does:
	// a shorter one parks a database under its pooled servers.
	scheddLink := filepath.Join(run, "schedd.sock")
	scheddDatabase := filepath.Join(run, "schedd-database.toml")
	writeDbNodeFile(t, scheddDatabase, fmt.Sprintf(`[database_scheduler]
idle_timeout = "45s"
sweep_interval = "2s"
drill_every = "-1s"

[[database_scheduler.nodes]]
id = "db-e2e"
target_url = "unix://%s"
admission_ceiling_mb = 4096
volume_capacity_bytes = %d
`, node.vmmdSock, int64(20)<<30))
	proxyAddr := freeTCPAddr(t)
	_, proxyPort, _ := net.SplitHostPort(proxyAddr)
	backend, apidEnv, fleet := dbNodeBackendEnv(t, run, scheddLink, proxyPort)
	env := append([]string{"FAAS_SCHEDD_DATABASE_CONFIG=" + scheddDatabase}, apidEnv...)
	h := e2etest.Start(t, pool, e2etest.APID|e2etest.Schedd, env...)
	if err := os.Symlink(h.ScheddSock, scheddLink); err != nil {
		t.Fatal(err)
	}
	startPgproxyd(t, run, bins, proxyAddr, e2etest.DatabaseURL(pool), scheddLink)

	key := h.SeedAccount(ctx, api.PlanPro, "dbnode")
	account, err := state.NewPgStore(pool).AccountByEmail(ctx, "e2e+pro+dbnode@test.example")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	app := seedApp(t, ctx, h, account.ID, "dbnode-app")

	started := time.Now()
	orders := createDbNodeDatabase(t, h, key, api.CreateManagedPostgresDatabaseRequest{
		Name: "orders", Region: backend.Region, PostgresMajor: 17, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 86400,
	})
	t.Logf("provisioned %s through the API in %v", orders.ID, time.Since(started).Round(time.Millisecond))
	url, runtimeBinding := bindDbNodeDatabase(t, h, key, fleet, account.ID, app.ID, orders.ID, "DATABASE_URL", managedpostgres.CredentialReadWrite)
	// Runtime roles cannot create objects (ADR-461); the release's
	// migration credential owns the schema.
	migrationURL, migrationBinding := bindDbNodeDatabase(t, h, key, fleet, account.ID, app.ID, orders.ID, "DATABASE_MIGRATION_URL", managedpostgres.CredentialMigration)
	if !strings.Contains(url, "."+dbNodeSuffix+":"+proxyPort+"/") {
		t.Fatalf("DATABASE_URL does not name the proxy endpoint: %s", redactDbNodeURL(url))
	}

	dbNodeExec(t, migrationURL, proxyAddr, "CREATE TABLE notes (body text NOT NULL)")
	dbNodeExec(t, url, proxyAddr, "INSERT INTO notes VALUES ('before')")
	// The restore target sits between the two writes, by the guest's clock.
	target := dbNodeQuery(t, url, proxyAddr, "SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')")
	time.Sleep(time.Second)
	dbNodeExec(t, url, proxyAddr, "INSERT INTO notes VALUES ('after')")

	// Nobody connected: schedd parks it, and a connection wakes it.
	vmID := dbNodeProviderID(t, pool, orders.ID)
	waitDbNodeVMState(t, pool, vmID, "parked", 2*time.Minute)
	woke := time.Now()
	if got := dbNodeQuery(t, url, proxyAddr, "SELECT string_agg(body, ',' ORDER BY body) FROM notes"); got != "after,before" {
		t.Fatalf("after a wake the database holds %q", got)
	}
	t.Logf("parked -> first query through pgproxyd %v", time.Since(woke).Round(time.Millisecond))

	raw, status := doReq(t, h, key, http.MethodPost, "/v1/postgres/databases/"+orders.ID+"/restore",
		api.RestoreManagedPostgresDatabaseRequest{Name: "orders-restore", PointInTime: target})
	if status != http.StatusAccepted && status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("restore = %d %s", status, raw)
	}
	var restored api.ManagedPostgresDatabase
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("restore response: %v", err)
	}
	restored = waitDbNodeDatabase(t, h, key, restored.ID, 10*time.Minute)
	restoreURL, restoreBinding := bindDbNodeDatabase(t, h, key, fleet, account.ID, app.ID, restored.ID, "RESTORE_URL", managedpostgres.CredentialReadWrite)
	if got := dbNodeQuery(t, restoreURL, proxyAddr, "SELECT string_agg(body, ',') FROM notes"); got != "before" {
		t.Fatalf("restored to %s the copy holds %q, want only the row before it", target, got)
	}

	// A database with bindings refuses deletion; the bindings go first.
	for _, id := range []string{restoreBinding, migrationBinding, runtimeBinding} {
		if raw, status := doReq(t, h, key, http.MethodDelete, "/v1/postgres/bindings/"+id, nil); status >= 300 {
			t.Fatalf("delete binding %s = %d %s", id, status, raw)
		}
	}
	// The copy is purged at once; the source cannot start its delete window
	// while a database restored from it exists. Unbinding finishes in the
	// background, so a delete conflicts until it has.
	deleteDbNodeDatabase(t, h, key, restored.ID, "?purge=true")
	deleted := deleteDbNodeDatabase(t, h, key, orders.ID, "")
	if deleted.State != "deleting" {
		t.Fatalf("scheduled delete left %s %q, want deleting in its grace window", orders.ID, deleted.State)
	}
	t.Logf("node logs: %s", node.logs)
}

// dbNode is the database node's processes and sockets.
type dbNode struct {
	vmmdSock string
	logs     string
}

// startDatabaseNode starts dbbackupd and vmmd in database mode, in a cgroup
// of their own that cleanup kills, and waits until vmmd has built its base.
func startDatabaseNode(t *testing.T, run, bases, guestInit, bins string) dbNode {
	t.Helper()
	cgroup := "dbnode-e2e-" + filepath.Base(run)
	if err := os.Mkdir(filepath.Join("/sys/fs/cgroup", cgroup), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killDbNodeCgroup(t, cgroup) })
	if err := os.MkdirAll(bases, 0o755); err != nil {
		t.Fatal(err)
	}
	archiveSock := filepath.Join(run, "a.sock")
	writeDbNodeFile(t, filepath.Join(run, "dbbackupd.toml"), fmt.Sprintf(`socket_path = %q
store_dir = %q
ops_addr = %q
min_retention = "24h"
prune_interval = "1h"
`, archiveSock, filepath.Join(run, "archive"), freeTCPAddr(t)))
	startDbNodeProc(t, run, "dbbackupd", filepath.Join(bins, "dbbackupd"), []string{"-config", filepath.Join(run, "dbbackupd.toml")}, nil)
	waitDbNodeFile(t, archiveSock, 30*time.Second)

	sock := filepath.Join(run, "vmmd.sock")
	metrics := freeTCPAddr(t)
	writeDbNodeFile(t, filepath.Join(run, "vmmd.toml"), fmt.Sprintf(`role = "database"
listen_addr = "unix://%s"
metrics_addr = %q

[database_vms]
jail_base = %q
volume_root = %q
kernel_path = "/srv/fc/kernel/1.7.0"
base_dir = %q
guest_init = %q
parent_cgroup = %q
archive_socket = %q

[database_vms.images]
"17" = %q
`, sock, metrics, filepath.Join(run, "j"), filepath.Join(run, "v"), bases, guestInit, cgroup, archiveSock, dbNodeImage))
	startDbNodeProc(t, run, "vmmd", filepath.Join(bins, "vmmd"), nil, []string{"FAAS_VMMD_CONFIG=" + filepath.Join(run, "vmmd.toml"), "FAAS_VMMD_ROLE=database"})
	// A fresh node builds its base before it serves.
	deadline := time.Now().Add(10 * time.Minute)
	for {
		resp, err := http.Get("http://" + metrics + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("database-node vmmd never became ready; see %s", run)
		}
		time.Sleep(time.Second)
	}
	return dbNode{vmmdSock: sock, logs: run}
}

// startPgproxyd starts the proxy with a self-signed certificate for the
// backend's host suffix. Its pooled servers close after 5 s, before schedd's
// 15 s idle park, as production's 60 s pool idle precedes its 5 min park.
func startPgproxyd(t *testing.T, run, bins, listen, dsn, schedd string) {
	t.Helper()
	certPath, keyPath := writeDbNodeCert(t, run)
	decoy := make([]byte, 32)
	if _, err := rand.Read(decoy); err != nil {
		t.Fatal(err)
	}
	writeDbNodeFile(t, filepath.Join(run, "decoy"), hex.EncodeToString(decoy))
	writeDbNodeFile(t, filepath.Join(run, "db-url"), dsn)
	ops := freeTCPAddr(t)
	writeDbNodeFile(t, filepath.Join(run, "pgproxyd.toml"), fmt.Sprintf(`listen_addr = %q
host_suffix = %q
tls_cert_path = %q
tls_key_path = %q
decoy_secret_path = %q
db_url_path = %q
schedd_target = "unix://%s"
ops_addr = %q
pool_server_idle = "5s"
`, listen, dbNodeSuffix, certPath, keyPath, filepath.Join(run, "decoy"), filepath.Join(run, "db-url"), schedd, ops))
	startDbNodeProc(t, run, "pgproxyd", filepath.Join(bins, "pgproxyd"), []string{"-config", filepath.Join(run, "pgproxyd.toml")}, nil)
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := http.Get("http://" + ops + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pgproxyd never became ready; see %s", run)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// dbNodeBackendEnv is apid's native backend configuration and the staging
// gate that admits it: an approval naming the backend's fingerprint.
func dbNodeBackendEnv(t *testing.T, run, schedd, proxyPort string) (managedpostgres.Backend, []string, *age.X25519Identity) {
	t.Helper()
	config := managedpostgres.Config{
		DefaultRegion: "eu-central", Defaults: map[string]string{"eu-central": "native-e2e"},
		MaxDatabasesPerAccount: 3, ProvisioningEnabled: true,
		Backends: []managedpostgres.BackendConfig{{
			ID: "native-e2e", Driver: firecracker.Driver, Region: "eu-central", Namespace: "native",
			Settings: map[string]string{
				"host_suffix": dbNodeSuffix, "port": proxyPort, "tls_mode": "require", "postgres_majors": "17",
				"schedd_target": "unix://" + schedd, "max_storage_bytes": strconv.FormatInt(2<<30, 10),
				"max_restore_window_seconds": "604800",
			},
			SecretEnv: map[string]string{"credential-key": "FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY"},
		}},
	}
	registry, err := managedpostgres.NewRegistry(config, func(string) string { return "" },
		map[string]managedpostgres.Factory{firecracker.Driver: firecracker.DescribeFactory()})
	if err != nil {
		t.Fatalf("native backend registry: %v", err)
	}
	backend, err := registry.Default("eu-central")
	if err != nil {
		t.Fatalf("native backend: %v", err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(run, "managed-postgres.json")
	writeDbNodeFile(t, configPath, string(encoded))
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipientPath := filepath.Join(run, "fleet.age.pub")
	if err := secretbox.WriteRecipientFile(recipientPath, identity); err != nil {
		t.Fatal(err)
	}
	credentialKey := make([]byte, 32)
	if _, err := rand.Read(credentialKey); err != nil {
		t.Fatal(err)
	}
	return backend, []string{
		"FAAS_ENVIRONMENT=" + managedpostgres.QualificationStagingEnvironment,
		"FAAS_FLEET_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_MANAGED_POSTGRES_CONFIG=" + configPath,
		"FAAS_MANAGED_POSTGRES_NATIVE_ENABLED=1",
		"FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY=" + hex.EncodeToString(credentialKey),
		managedpostgres.QualificationEnv + "=true",
		managedpostgres.QualificationVersionEnv + "=" + strconv.Itoa(managedpostgres.QualificationArtifactVersion),
		managedpostgres.QualificationBackendEnv + "=" + backend.ID,
		managedpostgres.QualificationFingerprintEnv + "=" + backend.Fingerprint,
		managedpostgres.QualificationUntilEnv + "=" + time.Now().UTC().Add(6*time.Hour).Format(time.RFC3339),
	}, identity
}

func createDbNodeDatabase(t *testing.T, h *e2etest.Harness, key string, req api.CreateManagedPostgresDatabaseRequest) api.ManagedPostgresDatabase {
	t.Helper()
	// schedd bootstraps the VM in the background, so the create answers
	// within apid's request budget with the database provisioning.
	started := time.Now()
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/postgres/databases", req)
	var created api.ManagedPostgresDatabase
	if status != http.StatusCreated || json.Unmarshal(raw, &created) != nil || created.State != "provisioning" {
		t.Fatalf("create database = %d %s; want 201 provisioning", status, raw)
	}
	t.Logf("create %s answered in %v", req.Name, time.Since(started).Round(time.Millisecond))
	return waitDbNodeDatabase(t, h, key, created.ID, 5*time.Minute)
}

func waitDbNodeDatabase(t *testing.T, h *e2etest.Harness, key, id string, within time.Duration) api.ManagedPostgresDatabase {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		raw, status := doReq(t, h, key, http.MethodGet, "/v1/postgres/databases/"+id, nil)
		var database api.ManagedPostgresDatabase
		if status == http.StatusOK && json.Unmarshal(raw, &database) == nil {
			switch database.State {
			case "ready":
				return database
			case "failed":
				t.Fatalf("database %s failed: %s", id, raw)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("database %s not ready after %v: %d %s", id, within, status, raw)
		}
		time.Sleep(time.Second)
	}
}

// bindDbNodeDatabase binds the database to the app under envKey and opens
// the DATABASE_URL apid sealed for it, as the app's runtime would.
func bindDbNodeDatabase(t *testing.T, h *e2etest.Harness, key string, fleet *age.X25519Identity, accountID, appID, databaseID, envKey string, access managedpostgres.CredentialAccess) (string, string) {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/postgres/databases/"+databaseID+"/bindings", api.CreateManagedPostgresBindingRequest{
		AppID: appID, Scope: "production", EnvironmentKey: envKey, Access: string(access),
	})
	if status != http.StatusAccepted && status != http.StatusCreated {
		t.Fatalf("bind %s = %d %s", databaseID, status, raw)
	}
	var binding api.ManagedPostgresBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		t.Fatalf("binding response: %v", err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for binding.State != "ready" {
		if binding.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("binding %s: %+v", binding.ID, binding)
		}
		time.Sleep(time.Second)
		raw, status = doReq(t, h, key, http.MethodGet, "/v1/postgres/bindings/"+binding.ID, nil)
		if status != http.StatusOK || json.Unmarshal(raw, &binding) != nil {
			t.Fatalf("get binding = %d %s", status, raw)
		}
	}
	secret, err := state.NewPgStore(h.Pool).GetAppSecretInScope(context.Background(), accountID, appID, "production", envKey)
	if err != nil || secret == nil {
		t.Fatalf("sealed %s: %v", envKey, err)
	}
	envelope, err := secretbox.Open(fleet, secret.Ciphertext)
	if err != nil {
		t.Fatalf("open sealed %s: %v", envKey, err)
	}
	url := envelope[envKey]
	if url == "" {
		t.Fatalf("sealed envelope lacks %s (keys %d)", envKey, len(envelope))
	}
	return url, binding.ID
}

// deleteDbNodeDatabase deletes a database, retrying while its bindings
// finish unbinding.
func deleteDbNodeDatabase(t *testing.T, h *e2etest.Harness, key, id, query string) api.ManagedPostgresDatabase {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		raw, status := doReq(t, h, key, http.MethodDelete, "/v1/postgres/databases/"+id+query, nil)
		if status < 300 {
			var database api.ManagedPostgresDatabase
			_ = json.Unmarshal(raw, &database)
			return database
		}
		if status != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("delete %s%s = %d %s", id, query, status, raw)
		}
		time.Sleep(2 * time.Second)
	}
}

// dbNodeConnect dials pgproxyd at proxyAddr for the URL's host, as DNS
// for the backend's host suffix would.
func dbNodeConnect(t *testing.T, url, proxyAddr string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	proxyHost, _, _ := net.SplitHostPort(proxyAddr)
	cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{proxyHost}, nil }
	cfg.ConnectTimeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect through pgproxyd: %v", err)
	}
	return conn
}

func dbNodeExec(t *testing.T, url, proxyAddr, sql string) {
	t.Helper()
	conn := dbNodeConnect(t, url, proxyAddr)
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func dbNodeQuery(t *testing.T, url, proxyAddr, sql string) string {
	t.Helper()
	conn := dbNodeConnect(t, url, proxyAddr)
	defer func() { _ = conn.Close(context.Background()) }()
	var out string
	if err := conn.QueryRow(context.Background(), sql).Scan(&out); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

func dbNodeProviderID(t *testing.T, pool *pgxpool.Pool, databaseID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT provider_resource_id FROM managed_postgres_databases WHERE id = $1`, databaseID).Scan(&id); err != nil {
		t.Fatalf("provider resource of %s: %v", databaseID, err)
	}
	return id
}

func waitDbNodeVMState(t *testing.T, pool *pgxpool.Pool, vmID, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var got string
	for {
		if err := pool.QueryRow(context.Background(), `SELECT state FROM database_vms WHERE database_id::text = $1`, vmID).Scan(&got); err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("database VM %s is %q after %v, want %q", vmID, got, within, want)
		}
		time.Sleep(time.Second)
	}
}

// startDbNodeProc runs a daemon with its output in run/<name>.log and stops
// it at cleanup.
func startDbNodeProc(t *testing.T, run, name, bin string, args, env []string) {
	t.Helper()
	log, err := os.Create(filepath.Join(run, name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append(os.Environ(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"), env...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = log.Close()
		if t.Failed() {
			if out, err := os.ReadFile(log.Name()); err == nil {
				t.Logf("%s log (tail):\n%s", name, tailDbNodeLog(out, 4000))
			}
		}
	})
}

// killDbNodeCgroup ends every database VM the node left in its cgroup and
// removes the cgroup tree.
func killDbNodeCgroup(t *testing.T, cgroup string) {
	root := filepath.Join("/sys/fs/cgroup", cgroup)
	_ = os.WriteFile(filepath.Join(root, "cgroup.kill"), []byte("1"), 0o644)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var dirs []string
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				dirs = append(dirs, path)
			}
			return nil
		})
		for i := len(dirs) - 1; i >= 0; i-- {
			_ = os.Remove(dirs[i])
		}
		if _, err := os.Stat(root); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("cgroup %s survived cleanup", root)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func writeDbNodeCert(t *testing.T, run string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + dbNodeSuffix},
		DNSNames:  []string{"*." + dbNodeSuffix, dbNodeSuffix},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(run, "proxy.crt"), filepath.Join(run, "proxy.key")
	writeDbNodeFile(t, certPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	writeDbNodeFile(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})))
	return certPath, keyPath
}

// writeFile writes an owner-only file: pgproxyd refuses readable secrets.
func writeDbNodeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitDbNodeFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tailDbNodeLog(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// redactDbNodeURL drops the password from a connection URL for messages.
func redactDbNodeURL(url string) string {
	if at := strings.LastIndex(url, "@"); at > 0 {
		if scheme := strings.Index(url, "://"); scheme > 0 && scheme < at {
			return url[:scheme+3] + "<redacted>" + url[at:]
		}
	}
	return "<unparseable>"
}
