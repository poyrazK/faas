// adr: 1027 — the import relay reaches only public addresses, insists on
// TLS from the source, and verifies the source's certificate.
package dbvm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestImportSourcesMustBePublic(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.9", "192.168.1.1", "169.254.169.254", "100.64.7.1", "198.19.0.5",
		"0.0.0.0", "::1", "fc00::1", "fe80::1", "::ffff:10.0.0.1", "224.0.0.1"} {
		if publicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s counted as public", addr)
		}
	}
	for _, addr := range []string{"3.5.140.2", "34.226.10.1", "2600:1f18::1", "::ffff:3.5.140.2"} {
		if !publicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s counted as non-public", addr)
		}
	}
}

func TestImportSourceValidate(t *testing.T) {
	for _, ok := range []ImportSource{{Host: "ep-cool-name-123.us-east-2.aws.neon.tech", Port: 5432}, {Host: "203.0.113.9", Port: 1}, {Host: "2600:1f18::1", Port: 65535}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []ImportSource{{Host: "", Port: 5432}, {Host: "a b", Port: 5432}, {Host: "host:1", Port: 5432}, {Host: "db.example", Port: 0}, {Host: "db.example", Port: 70000}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// testCert issues a server certificate for host from a fresh CA.
func testCert(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "import test CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// fakeSource answers one connection like a PostgreSQL server: the
// SSLRequest gets answer, then (for 'S') TLS with cert, then it echoes.
func fakeSource(t *testing.T, answer byte, cert tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		request := make([]byte, 8)
		if _, err := io.ReadFull(conn, request); err != nil || !bytes.Equal(request, sslRequest) {
			return
		}
		if _, err := conn.Write([]byte{answer}); err != nil || answer != 'S' {
			return
		}
		server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		_, _ = io.Copy(server, server)
	}()
	return ln.Addr().String()
}

func TestImportRelayVerifiesTheSourcesTLS(t *testing.T) {
	cert, roots := testCert(t, "db.source.test")
	ctx := context.Background()
	dial := func(r *Runtime, addr, host string) (net.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		tlsConn, err := startPostgresTLS(ctx, conn, host, r.cfg.ImportRoots)
		if err != nil {
			_ = conn.Close()
		}
		return tlsConn, err
	}
	r := &Runtime{cfg: RuntimeConfig{ImportRoots: roots}}
	conn, err := dial(r, fakeSource(t, 'S', cert), "db.source.test")
	if err != nil {
		t.Fatalf("verified source refused: %v", err)
	}
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo %q, %v", got, err)
	}
	_ = conn.Close()
	if _, err := dial(r, fakeSource(t, 'S', cert), "other.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("certificate for another host = %v, want refused", err)
	}
	if _, err := dial(r, fakeSource(t, 'N', cert), "db.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("source without TLS = %v, want refused", err)
	}
	if _, err := dial(&Runtime{}, fakeSource(t, 'S', cert), "db.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("source signed by an unknown CA = %v, want refused", err)
	}
}

func TestImportRelayRefusesPrivateSources(t *testing.T) {
	cert, roots := testCert(t, "localhost")
	addr := fakeSource(t, 'S', cert)
	port := netip.MustParseAddrPort(addr).Port()
	r := &Runtime{cfg: RuntimeConfig{ImportRoots: roots}}
	if _, err := r.dialImportSource(context.Background(), ImportSource{Host: "localhost", Port: int(port)}); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("loopback source = %v, want refused", err)
	}
	r.cfg.ImportAllowPrivate = true
	conn, err := r.dialImportSource(context.Background(), ImportSource{Host: "localhost", Port: int(port)})
	if err != nil {
		t.Fatalf("with private sources allowed: %v", err)
	}
	_ = conn.Close()
}

func TestCustomerImportKeepsActiveSourcePinned(t *testing.T) {
	const id = "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	const activeID = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	const otherID = "4d5e6f7a-8b9c-4d0e-9f1a-2b3c4d5e6f7a"
	root, err := os.MkdirTemp("", "pgim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	vm := &databaseVM{root: root}
	active := ImportSource{Host: "active.example.com", Port: 5432}
	other := ImportSource{Host: "other.example.com", Port: 5432}
	vm.importSrc.Store(&active)
	rt := &Runtime{vms: map[string]*databaseVM{id: vm}}
	listener, err := net.Listen("unix", rt.udsPath(vm))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	mockErr := make(chan error, 4)
	go func() {
		for range 4 {
			conn, err := listener.Accept()
			if err != nil {
				mockErr <- err
				return
			}
			mockErr <- respondImportRunning(conn, activeID)
		}
	}()
	for _, test := range []struct {
		op        dbguest.Op
		operation string
		want      string
	}{
		{dbguest.OpCustomerImport, activeID, ""},
		{dbguest.OpCustomerImport, otherID, dbguest.ErrCodeImportOperationMismatch},
		{dbguest.OpImportCheck, activeID, dbguest.ErrCodeImportOperationMismatch},
		{dbguest.OpMigrationCheck, activeID, dbguest.ErrCodeImportOperationMismatch},
	} {
		req := dbguest.Request{Op: test.op, Import: &dbguest.ImportSource{
			Database: "source", User: "source_role", Password: "fixture-password",
			TargetDatabase: "customer", Owner: "customer_role", OperationID: test.operation,
		}}
		result, err := rt.ControlImport(t.Context(), id, other, req)
		if err != nil || result.Error != test.want || result.Import == nil || result.Import.OperationID != activeID {
			t.Fatalf("%s: got %+v, %v", test.op, result, err)
		}
		if got := vm.importSrc.Load(); got != &active || vm.importLn != nil {
			t.Fatalf("%s changed the active relay", test.op)
		}
		if err := <-mockErr; err != nil {
			t.Fatal(err)
		}
	}
}

func respondImportRunning(conn net.Conn, operation string) error {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, "OK 1000\n"); err != nil {
		return err
	}
	var request dbguest.Request
	if err := json.NewDecoder(reader).Decode(&request); err != nil {
		return err
	}
	if request.Op != dbguest.OpImportStatus {
		return fmt.Errorf("guest received unexpected operation %s", request.Op)
	}
	return json.NewEncoder(conn).Encode(dbguest.Response{State: dbguest.StateReady, Import: &dbguest.ImportStatus{
		State: dbguest.ImportRunning, OperationID: operation,
	}})
}
