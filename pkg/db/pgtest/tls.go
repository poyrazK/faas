package pgtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TLSCluster is a private PostgreSQL fixture. Admin connects on a local Unix
// socket; customer URLs require SCRAM authentication over verified TLS TCP.
// It never changes the PostgreSQL instance selected by DATABASE_URL.
type TLSCluster struct {
	Admin      *pgxpool.Pool
	CAPath     string
	AdminURL   string
	dir        string
	port       int
	bin        string
	running    bool
	credential *syscall.Credential
	t          *testing.T
}

func OpenTLSCluster(t *testing.T) *TLSCluster {
	t.Helper()
	bin := os.Getenv("FAAS_COMMIT_TLS_PG_BIN_DIR")
	if bin == "" {
		t.Skip("FAAS_COMMIT_TLS_PG_BIN_DIR required")
	}
	c := &TLSCluster{bin: bin, t: t}
	if os.Geteuid() == 0 {
		name := os.Getenv("FAAS_COMMIT_TLS_PG_USER")
		if name == "" {
			t.Fatal("root TLS fixtures require FAAS_COMMIT_TLS_PG_USER naming an unprivileged PostgreSQL user")
		}
		account, err := user.Lookup(name)
		if err != nil {
			t.Fatalf("PostgreSQL fixture user: %v", err)
		}
		uid, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil || uid == 0 {
			t.Fatal("PostgreSQL fixture user must be unprivileged")
		}
		gid, err := strconv.ParseUint(account.Gid, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		c.credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}
	// Short paths avoid PostgreSQL's Unix socket path limit on macOS.
	tempRoot := os.Getenv("FAAS_COMMIT_TLS_PG_TMPDIR")
	if tempRoot == "" && c.credential != nil {
		// A native harness may use a root-only TMPDIR. The dropped PostgreSQL
		// user needs to traverse the parent of its own private fixture directory.
		// An explicit fixture root lets disk-backed qualification keep database
		// writes off a full system disk without changing arbitrary permissions.
		tempRoot = "/tmp"
	}
	dir, err := os.MkdirTemp(tempRoot, "gcpg-")
	if err != nil {
		t.Fatal(err)
	}
	c.dir = dir
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	c.own(dir)
	var cKey string
	c.CAPath, cKey = tlsFixtureCertificate(t, dir)
	c.own(c.CAPath)
	c.own(cKey)
	data := filepath.Join(dir, "data")
	c.run("initdb", "-D", data, "--no-locale", "--encoding=UTF8", "-A", "trust", "-U", "admin")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.port = listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	config := fmt.Sprintf("\nlisten_addresses='127.0.0.1'\nport=%d\nunix_socket_directories=%s\nssl=on\nssl_cert_file=%s\nssl_key_file=%s\n", c.port, quote(dir), quote(c.CAPath), quote(cKey))
	file, err := os.OpenFile(filepath.Join(data, "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(config)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("TLS fixture configuration: %v %v", writeErr, closeErr)
	}
	if err := os.WriteFile(filepath.Join(data, "pg_hba.conf"), []byte("local all all trust\nhostssl all all 127.0.0.1/32 scram-sha-256\nhost all all 127.0.0.1/32 reject\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c.running {
			c.stop("immediate")
		}
	})
	c.Start()
	u := url.URL{Scheme: "postgres", User: url.User("admin"), Path: "/postgres"}
	u.RawQuery = url.Values{"host": {dir}, "port": {fmt.Sprint(c.port)}, "sslmode": {"disable"}}.Encode()
	c.AdminURL = u.String()
	c.Admin, err = pgxpool.New(context.Background(), c.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Admin.Close)
	return c
}

func (c *TLSCluster) URL(username, password string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(username, password), Host: net.JoinHostPort("localhost", fmt.Sprint(c.port)), Path: "/postgres", RawQuery: "sslmode=verify-full"}
	return u.String()
}
func (c *TLSCluster) Start() {
	c.run("pg_ctl", "-D", filepath.Join(c.dir, "data"), "-l", filepath.Join(c.dir, "postgres.log"), "-w", "start")
	c.running = true
}
func (c *TLSCluster) Stop() { c.stop("fast") }
func (c *TLSCluster) stop(mode string) {
	c.run("pg_ctl", "-D", filepath.Join(c.dir, "data"), "-m", mode, "-w", "stop")
	c.running = false
}
func (c *TLSCluster) Log() string {
	data, _ := os.ReadFile(filepath.Join(c.dir, "postgres.log"))
	return string(data)
}
func (c *TLSCluster) own(path string) {
	if c.credential != nil {
		if err := os.Chown(path, int(c.credential.Uid), int(c.credential.Gid)); err != nil {
			c.t.Fatal(err)
		}
	}
}
func (c *TLSCluster) run(args ...string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(c.bin, args[0]), args[1:]...)
	cmd.Dir = c.dir
	if c.credential != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: c.credential}
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		c.t.Fatalf("TLS PostgreSQL fixture %s: %v\n%s\n%s", args[0], err, output, c.Log())
	}
}

func tlsFixtureCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &certificate, &certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
