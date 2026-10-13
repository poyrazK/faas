//go:build metal && linux

// adr: 1027 — pgproxyd under its generated systemd unit, as the
// pgproxyd_service role installs it: its own user, owner-only secrets, the
// unit's sandbox, Type=notify readiness, and role verifiers read through the
// least-privilege faas_pgproxyd database role.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

const (
	smokePGBin    = "/usr/lib/postgresql/16/bin"
	smokePGPort   = 55473
	smokeListen   = "127.0.0.1:55474"
	smokeOps      = "127.0.0.1:55475"
	smokeSuffix   = "db.smoke.test"
	smokeUnit     = "faas-pgproxyd-smoke.service"
	smokeUser     = "faas-pgproxyd"
	smokeRolePass = "smoke-faas-pgproxyd-password"
)

func must(t *testing.T, argv ...string) string {
	t.Helper()
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
	return string(out)
}

func writeOwned(t *testing.T, path string, body []byte, mode os.FileMode, uid, gid int) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + smokeSuffix},
		DNSNames: []string{"*." + smokeSuffix}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// startPostgres runs a throwaway cluster as the postgres OS user with the
// production schema and the faas_pgproxyd role database_node.yml creates.
func startPostgres(t *testing.T, work string) *pgxpool.Pool {
	t.Helper()
	pgUser, err := user.Lookup("postgres")
	if err != nil {
		t.Skip("no postgres OS user")
	}
	uid, _ := strconv.Atoi(pgUser.Uid)
	gid, _ := strconv.Atoi(pgUser.Gid)
	data := filepath.Join(work, "pg")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(data, uid, gid); err != nil {
		t.Fatal(err)
	}
	must(t, "runuser", "-u", "postgres", "--", smokePGBin+"/initdb", "-D", data, "-U", "postgres", "--auth-local=trust", "--auth-host=scram-sha-256")
	hba := "local all postgres trust\nhost all postgres 127.0.0.1/32 trust\nhost faas faas_pgproxyd 127.0.0.1/32 scram-sha-256\n"
	writeOwned(t, filepath.Join(data, "pg_hba.conf"), []byte(hba), 0o600, uid, gid)
	must(t, "runuser", "-u", "postgres", "--", smokePGBin+"/pg_ctl", "-D", data, "-l", filepath.Join(data, "log"), "-w",
		"-o", fmt.Sprintf("-p %d -h 127.0.0.1 -k %s", smokePGPort, data), "start")
	t.Cleanup(func() {
		_ = exec.Command("runuser", "-u", "postgres", "--", smokePGBin+"/pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
	})
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", smokePGPort))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE faas"); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://postgres@127.0.0.1:%d/faas?sslmode=disable", smokePGPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, stmt := range []string{
		"CREATE ROLE faas_pgproxyd LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '" + smokeRolePass + "'",
		"GRANT CONNECT ON DATABASE faas TO faas_pgproxyd",
		"GRANT USAGE ON SCHEMA public TO faas_pgproxyd",
		"GRANT SELECT ON database_role_verifiers TO faas_pgproxyd",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return pool
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestMetalPgproxydUnderSystemd(t *testing.T) {
	bin, workdir := os.Getenv("FAAS_PGPROXYD_BIN"), os.Getenv("FAAS_DBVM_WORKDIR")
	if bin == "" || workdir == "" || os.Geteuid() != 0 {
		t.Skip("set FAAS_PGPROXYD_BIN and FAAS_DBVM_WORKDIR and run as root")
	}
	if _, err := os.Stat(smokePGBin + "/initdb"); err != nil {
		t.Skip("PostgreSQL 16 server binaries not installed")
	}
	// PrivateTmp hides /tmp and /var/tmp from the unit, so everything it
	// reads lives under the work directory.
	work, err := os.MkdirTemp(workdir, "pgproxyd-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	if err := os.Chmod(work, 0o755); err != nil {
		t.Fatal(err)
	}
	pool := startPostgres(t, work)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// A role with a verifier, written as the native driver does.
	databaseID, password := uuid.NewString(), "app-"+uuid.NewString()
	v, err := pgscram.NewVerifier(password, pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO database_role_verifiers (database_id, role_name, verifier) VALUES ($1, 'app', $2)", databaseID, v.String()); err != nil {
		t.Fatal(err)
	}

	// schedd stand-in: every wake fails, so an authenticated client gets
	// 57P03 and a wrong password 28P01.
	scheddSock := filepath.Join(work, "schedd.sock")
	ln, err := net.Listen("unix", scheddSock)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(scheddSock, 0o777)
	gsrv := grpc.NewServer()
	scheddpb.RegisterScheddServer(gsrv, scheddpb.UnimplementedScheddServer{})
	go func() { _ = gsrv.Serve(ln) }()
	t.Cleanup(gsrv.Stop)

	// The service user and its owner-only secrets, as the role installs them.
	created := false
	if _, err := user.Lookup(smokeUser); err != nil {
		must(t, "useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", smokeUser)
		created = true
	}
	t.Cleanup(func() {
		if created {
			_ = exec.Command("userdel", smokeUser).Run()
		}
	})
	svc, err := user.Lookup(smokeUser)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(svc.Uid)
	gid, _ := strconv.Atoi(svc.Gid)
	secrets := filepath.Join(work, "pgproxyd")
	if err := os.Mkdir(secrets, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(secrets, 0, gid); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := selfSigned(t)
	writeOwned(t, filepath.Join(secrets, "tls.crt"), certPEM, 0o444, uid, gid)
	writeOwned(t, filepath.Join(secrets, "tls.key"), keyPEM, 0o400, uid, gid)
	writeOwned(t, filepath.Join(secrets, "decoy"), []byte(strings.Repeat("d", 64)+"\n"), 0o400, uid, gid)
	dbURL := filepath.Join(secrets, "db-url")
	writeOwned(t, dbURL, []byte(fmt.Sprintf("postgres://faas_pgproxyd:%s@127.0.0.1:%d/faas?sslmode=disable\n", smokeRolePass, smokePGPort)), 0o400, uid, gid)
	config := fmt.Sprintf(`listen_addr = %q
host_suffix = %q
tls_cert_path = %q
tls_key_path = %q
decoy_secret_path = %q
db_url_path = %q
schedd_target = %q
ops_addr = %q
`, smokeListen, smokeSuffix, filepath.Join(secrets, "tls.crt"), filepath.Join(secrets, "tls.key"), filepath.Join(secrets, "decoy"), dbURL, "unix://"+scheddSock, smokeOps)
	configPath := filepath.Join(work, "pgproxyd.toml")
	writeOwned(t, configPath, []byte(config), 0o640, 0, gid)

	// The generated unit, pointed at the test binary and config.
	unit := daemonunitspec.UnitPgproxyd()
	unit.ExecStart = bin + " -config " + configPath
	unitPath := filepath.Join("/run/systemd/system", smokeUnit)
	if err := os.WriteFile(unitPath, unit.Render(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", smokeUnit).Run()
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "daemon-reload").Run()
	})
	must(t, "systemctl", "daemon-reload")
	// Type=notify: start returns only after READY=1.
	if out, err := exec.Command("systemctl", "start", smokeUnit).CombinedOutput(); err != nil {
		journal, _ := exec.Command("journalctl", "-u", smokeUnit, "--no-pager", "-n", "30").CombinedOutput()
		t.Fatalf("start: %v\n%s\n%s", err, out, journal)
	}
	pid := strings.TrimSpace(strings.TrimPrefix(must(t, "systemctl", "show", "-p", "MainPID", smokeUnit), "MainPID="))
	status := must(t, "cat", "/proc/"+pid+"/status")
	if !strings.Contains(status, "\nUid:\t"+svc.Uid+"\t") {
		t.Fatalf("pgproxyd does not run as %s:\n%s", smokeUser, status)
	}
	resp, err := http.Get("http://" + smokeOps + "/readyz") //nolint:noctx // test-only loopback probe
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz = %v %v", resp, err)
	}
	_ = resp.Body.Close()

	connect := func(pw string) error {
		cfg, err := pgx.ParseConfig("host=" + databaseID + "." + smokeSuffix + " port=55474 user=app dbname=app sslmode=require connect_timeout=10 password=" + pw)
		if err != nil {
			return err
		}
		cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
		cfg.TLSConfig = &tls.Config{ServerName: databaseID + "." + smokeSuffix, InsecureSkipVerify: true} //nolint:gosec // self-signed test certificate
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			_ = conn.Close(ctx)
		}
		return err
	}
	if err := connect("wrong-" + password); pgCode(err) != "28P01" {
		t.Fatalf("wrong password = %v, want 28P01", err)
	}
	// The right password passes SCRAM, which proves pgproxyd read the
	// verifier through faas_pgproxyd, and then fails at the stand-in wake.
	if err := connect(password); pgCode(err) != "57P03" {
		t.Fatalf("right password = %v, want 57P03 from the failed wake", err)
	}

	// A secret others can read stops the daemon at startup.
	if err := os.Chmod(dbURL, 0o440); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("systemctl", "restart", smokeUnit).CombinedOutput(); err == nil {
		t.Fatalf("pgproxyd started with a group-readable database URL: %s", out)
	}
	journal, _ := exec.Command("journalctl", "-u", smokeUnit, "--no-pager", "-n", "20").CombinedOutput()
	if !strings.Contains(string(journal), "must not be readable by group or others") {
		t.Fatalf("journal lacks the secret-mode refusal:\n%s", journal)
	}
}
