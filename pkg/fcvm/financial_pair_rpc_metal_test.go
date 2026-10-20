//go:build linux && metal

package fcvm_test

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
	"math/big"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func init() { fcvm.FinancialPairServeRPC = serveFinancialPairRPC }

type financialPairVMM struct{ *fcvm.Manager }

func (m financialPairVMM) SignalAndKill(ctx context.Context, instance string, signal, grace int32) (bool, int32, error) {
	return m.Manager.SignalAndKill(ctx, instance, syscall.Signal(signal), time.Duration(grace)*time.Second)
}

func serveFinancialPairRPC(ctx context.Context, manager *fcvm.Manager, options fcvm.FinancialPairTrafficOptions) (string, func(), error) {
	identity, err := tls.X509KeyPair(options.Certificate, options.Key)
	if err != nil {
		return "", nil, err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(options.CA) {
		return "", nil, errors.New("invalid fixture CA")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity}, ClientCAs: ca, ClientAuth: tls.RequireAndVerifyClientCert}
	listener, err := wire.ListenAs(ctx, "tcp://"+net.JoinHostPort(options.Host, "0"), config, "root")
	if err != nil {
		return "", nil, err
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(config)))
	service := vmmdgrpc.New(financialPairVMM{manager}, nil, "financial-pair-native", nil)
	service.Register(server)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	stop := func() {
		server.Stop()
		_ = listener.Close()
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = service.Close(closeCtx)
		<-done
	}
	return "tcp://" + listener.Addr().String(), stop, nil
}

// Keys exist only in memory and the child configuration's stdin. Never use
// the host's production CA, node certificates, or operator credentials.
func financialPairTLS(t *testing.T) (fcvm.FinancialPairTrafficOptions, *tls.Config) {
	t.Helper()
	host := os.Getenv("FAAS_FINANCIAL_PAIR_ADDR_B")
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || !ip.IsGlobalUnicast() || host == os.Getenv("FAAS_FINANCIAL_PAIR_ADDR_A") {
		t.Fatal("traffic acceptance requires distinct, reachable FAAS_FINANCIAL_PAIR_ADDR_A/B")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "financial-pair-fixture-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(15 * time.Minute), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	issue := func(serial int64, usage x509.ExtKeyUsage) ([]byte, []byte) {
		leafKey, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "financial-pair-fixture"}, NotBefore: template.NotBefore, NotAfter: template.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{ip}}
		encoded, certErr := x509.CreateCertificate(rand.Reader, leaf, template, &leafKey.PublicKey, key)
		if certErr != nil {
			t.Fatal(certErr)
		}
		encodedKey, keyErr := x509.MarshalPKCS8PrivateKey(leafKey)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	}
	serverCert, serverKey := issue(2, x509.ExtKeyUsageServerAuth)
	clientCert, clientKey := issue(3, x509.ExtKeyUsageClientAuth)
	client, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	return fcvm.FinancialPairTrafficOptions{Host: host, CA: caPEM, Certificate: serverCert, Key: serverKey}, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{client}, RootCAs: roots}
}

func TestFinancialPairRPCRequiresMutualTLS(t *testing.T) {
	t.Setenv("FAAS_FINANCIAL_PAIR_ADDR_A", "192.0.2.1")
	t.Setenv("FAAS_FINANCIAL_PAIR_ADDR_B", "192.0.2.2")
	options, clientTLS := financialPairTLS(t)
	// A bounded, unfunded loopback transport probe. It supplies no host or
	// financial qualification evidence and executes no lifecycle RPC.
	options.Host = "127.0.0.1"
	clientTLS.ServerName = "192.0.2.2"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	target, stop, err := serveFinancialPairRPC(ctx, &fcvm.Manager{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for _, tc := range []struct {
		name      string
		valid     bool
		configure func(*tls.Config)
	}{
		{"trusted_peer", true, func(*tls.Config) {}},
		{"missing_client_certificate", false, func(c *tls.Config) { c.Certificates = nil }},
		{"untrusted_server", false, func(c *tls.Config) { c.RootCAs = x509.NewCertPool() }},
		{"wrong_server_identity", false, func(c *tls.Config) { c.ServerName = "192.0.2.3" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := clientTLS.Clone()
			tc.configure(config)
			connection, err := wire.DialContext(ctx, target, config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = connection.Close() }()
			callCtx, callCancel := context.WithTimeout(ctx, time.Second)
			defer callCancel()
			_, err = vmmdpb.NewVmmdClient(connection).Ping(callCtx, &vmmdpb.PingRequest{})
			if (err == nil) != tc.valid {
				t.Fatalf("authenticated fixture transport: valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
