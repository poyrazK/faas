//go:build metal && linux

// adr: 1027 — schedd imports another PostgreSQL database through vmmd's
// gRPC handlers: vmmd keeps the endpoint and relays the copy over TLS.
package dbsched

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
)

// metalImportRoots, when set, makes newMetalNode's runtimes trust it for
// import sources and reach loopback ones.
var metalImportRoots *x509.CertPool

// localhostCert issues a certificate for localhost from a fresh CA.
func localhostCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "import CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// tlsShim fronts a database VM like a remote PostgreSQL server requiring
// TLS, and returns its port.
func tlsShim(t *testing.T, rt *dbvm.Runtime, id string, cert tls.Certificate) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	sslRequest := []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				request := make([]byte, 8)
				if _, err := io.ReadFull(conn, request); err != nil || !bytes.Equal(request, sslRequest) {
					return
				}
				if _, err := conn.Write([]byte{'S'}); err != nil {
					return
				}
				client := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				vm, err := rt.Dial(context.Background(), id)
				if err != nil {
					return
				}
				defer func() { _ = vm.Close() }()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(vm, client); done <- struct{}{} }()
				go func() { _, _ = io.Copy(client, vm); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestMetalSchedulerImport(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	cert, roots := localhostCert(t)
	metalImportRoots = roots
	t.Cleanup(func() { metalImportRoots = nil })
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(workdir, "imbucket")}
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	a := newMetalNode(t, workdir, map[int]string{17: base}, "im", 20<<30, &topLeaser{}, bucket)
	store := NewMemStore()
	seedNodes(t, store, a.node)
	vmm := NewGRPCVMMWithDial(nil, func(ctx context.Context, target string, _ *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	t.Cleanup(func() { _ = vmm.Close() })
	s := New(store, vmm, Config{IdleTimeout: time.Hour, ReadyTimeout: time.Minute}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	source, target := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), source)
		_ = s.Delete(context.Background(), target)
	})
	setup := `CREATE ROLE gregale_schema_owner NOLOGIN NOINHERIT;
CREATE DATABASE gregale OWNER gregale_schema_owner;
\connect gregale
ALTER SCHEMA public OWNER TO gregale_schema_owner;`
	for _, id := range []string{source, target} {
		if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
			t.Fatal(err)
		}
		if resp, err := s.Admin(ctx, id, setup); err != nil || resp.Error != "" {
			t.Fatalf("setup: %+v %v", resp, err)
		}
	}
	if resp, err := s.Admin(ctx, source, `CREATE ROLE src_mig LOGIN PASSWORD 'source-password' IN ROLE gregale_schema_owner;
GRANT CONNECT ON DATABASE gregale TO src_mig;
\connect gregale
SET ROLE gregale_schema_owner;
CREATE TABLE orders (id bigserial PRIMARY KEY, total numeric NOT NULL);
INSERT INTO orders (total) SELECT g * 1.5 FROM generate_series(1, 50000) g;`); err != nil || resp.Error != "" {
		t.Fatalf("source data: %+v %v", resp, err)
	}
	port := tlsShim(t, a.rt, source, cert)
	spec := ImportSpec{Host: "localhost", Port: port, Source: dbguest.ImportSource{Database: "gregale", User: "src_mig", Password: "source-password",
		TargetDatabase: "gregale", Owner: "gregale_schema_owner", OperationID: uuid.NewString()}}
	started := time.Now()
	if status, err := s.Import(ctx, target, spec); err != nil || status.State != dbguest.ImportRunning {
		t.Fatalf("import = %+v, %v", status, err)
	}
	for {
		status, err := s.ImportStatus(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == dbguest.ImportDone {
			break
		}
		if status.State != dbguest.ImportRunning {
			t.Fatalf("import = %+v", status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	took := time.Since(started)
	if resp, err := s.Admin(ctx, target, `CREATE ROLE checker LOGIN PASSWORD 'checker-password' IN ROLE gregale_schema_owner; GRANT CONNECT ON DATABASE gregale TO checker;`); err != nil || resp.Error != "" {
		t.Fatalf("checker: %+v %v", resp, err)
	}
	cfg, err := pgx.ParseConfig("host=dbvm user=checker dbname=gregale sslmode=disable password=checker-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return a.rt.Dial(ctx, target) }
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var rows int64
	var sum string
	if err := conn.QueryRow(ctx, "SELECT count(*), sum(total)::text FROM orders").Scan(&rows, &sum); err != nil || rows != 50000 || sum != "1875037500.0" {
		t.Fatalf("imported %d rows summing %s (%v); want 50000 summing 1875037500.0", rows, sum, err)
	}
	t.Logf("schedd import of 50000 rows through vmmd in %v", took.Round(time.Millisecond))
}
