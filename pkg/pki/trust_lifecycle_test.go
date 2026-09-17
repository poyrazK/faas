package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestIssueTrustBundleSupportsTwelveDistinctComputeNodes(t *testing.T) {
	t.Parallel()
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 12; index++ {
		nodeCN := fmt.Sprintf("compute-%d.faas", index)
		san := AltNames{IPAddresses: []net.IP{net.ParseIP(fmt.Sprintf("10.42.0.%d", index))}}
		bundle := filepath.Join(t.TempDir(), fmt.Sprintf("node-%d", index))
		if err := IssueTrustBundle(issuer, bundle, "compute-only", nodeCN, san); err != nil {
			t.Fatalf("IssueTrustBundle node %d: %v", index, err)
		}
		if err := ValidateTrustBundleForNode(bundle, "compute-only", san, nodeCN); err != nil {
			t.Fatalf("ValidateTrustBundleForNode node %d: %v", index, err)
		}
		_, caKey := CARoot(bundle)
		if _, err := os.Stat(caKey); !os.IsNotExist(err) {
			t.Fatalf("node %d bundle contains CA private key", index)
		}
	}
}

func TestInstallTrustBundleRotatesBatchAndPreservesLiveModes(t *testing.T) {
	t.Parallel()
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-7.faas"
	san := AltNames{DNSNames: []string{"compute-7.internal"}}
	initial := filepath.Join(t.TempDir(), "initial")
	if err := IssueTrustBundle(issuer, initial, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "active")
	copyTreeForPKITest(t, initial, target)
	role := RolesForBox("compute-only")[0]
	certPath, keyPath := LeafPaths(target, role)
	before := parseTestCert(t, certPath)
	if err := os.Chmod(keyPath, 0o440); err != nil {
		t.Fatal(err)
	}

	replacement := filepath.Join(t.TempDir(), "replacement")
	if err := IssueTrustBundle(issuer, replacement, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	if err := InstallTrustBundle(replacement, target, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	after := parseTestCert(t, certPath)
	if before.SerialNumber.Cmp(after.SerialNumber) == 0 {
		t.Fatal("trust bundle install did not rotate the leaf")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o440 {
		t.Fatalf("installed key mode = %#o, want preserved 0440", info.Mode().Perm())
	}
	if err := ValidateTrustBundleForNode(target, "compute-only", san, nodeCN); err != nil {
		t.Fatalf("installed bundle validation: %v", err)
	}
}

func TestInstallTrustBundleRejectsPartialCandidateWithoutChangingLiveFiles(t *testing.T) {
	t.Parallel()
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-8.faas"
	san := AltNames{DNSNames: []string{"compute-8.internal"}}
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	role := RolesForBox("compute-only")[0]
	activeCert, _ := LeafPaths(active, role)
	before, err := os.ReadFile(activeCert)
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate")
	if err := IssueTrustBundle(issuer, candidate, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	_, missingKey := LeafPaths(candidate, role)
	if err := os.Remove(missingKey); err != nil {
		t.Fatal(err)
	}
	if err := InstallTrustBundle(candidate, active, "compute-only", nodeCN, san); err == nil {
		t.Fatal("partial candidate installed successfully")
	}
	after, err := os.ReadFile(activeCert)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected partial candidate changed active certificate")
	}
}

func TestInstallTrustBundleRecoversInterruptedApplyingJournal(t *testing.T) {
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-recovery.faas"
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate")
	if err := IssueTrustBundle(issuer, candidate, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	role := RolesForBox("compute-only")[0]
	destination, _ := LeafPaths(active, role)
	source, _ := LeafPaths(candidate, role)
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageInstallFile(destination, body, 0o444, -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := unusedSiblingPath(destination, ".pki-previous-")
	if err != nil {
		t.Fatal(err)
	}
	journal := installJournal{Phase: "applying", Files: []installFile{{
		Destination: destination,
		Staged:      staged,
		Backup:      backup,
	}}}
	if err := persistInstallJournal(active, journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(destination, backup); err != nil {
		t.Fatal(err)
	}
	if err := InstallTrustBundle(candidate, active, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	if HasPendingInstall(active) {
		t.Fatal("install journal remains after recovery and successful install")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatal("recovered install did not activate the candidate certificate")
	}
}

func TestShortLivedLeafRenewalKeepsMutualTLSHealthy(t *testing.T) {
	issuer := t.TempDir()
	caCert, caKey, err := EnsureCA(issuer, false)
	if err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-9.faas"
	san := AltNames{DNSNames: []string{"compute-9.internal"}}
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	serverRole, clientRole := computeHandshakeRoles(t)
	writeShortLivedLeaf(t, active, serverRole, nodeCN, caCert, caKey, san)
	serverCertPath, _ := LeafPaths(active, serverRole)
	if remaining := time.Until(parseTestCert(t, serverCertPath).NotAfter); remaining >= ReissueThreshold {
		t.Fatalf("test leaf remaining validity = %s, want inside %s renewal threshold", remaining, ReissueThreshold)
	}
	assertMutualTLSHandshake(t, active, serverRole, clientRole)
	assertMutualTLSHealthRPC(t, active, serverRole, clientRole)

	exported := filepath.Join(t.TempDir(), "exported")
	if err := ExportTrustBundle(active, exported, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate")
	changed, err := RenewTrustBundle(issuer, exported, candidate, "compute-only", nodeCN, san)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Directory != serverRole.Directory || changed[0].Filename != serverRole.Filename {
		t.Fatalf("renewed roles = %+v, want only short-lived %s/%s", changed, serverRole.Directory, serverRole.Filename)
	}
	if err := InstallTrustBundle(candidate, active, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(parseTestCert(t, serverCertPath).NotAfter); remaining <= ReissueThreshold {
		t.Fatalf("renewed leaf remaining validity = %s, want beyond %s", remaining, ReissueThreshold)
	}
	assertMutualTLSHandshake(t, active, serverRole, clientRole)
	assertMutualTLSHealthRPC(t, active, serverRole, clientRole)
}

func TestRenewTrustBundleChangesOnlyLeavesInsideThreshold(t *testing.T) {
	issuer := t.TempDir()
	caCert, caKey, err := EnsureCA(issuer, false)
	if err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-10.faas"
	san := AltNames{DNSNames: []string{"compute-10.internal"}}
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, san); err != nil {
		t.Fatal(err)
	}
	expiring, _ := computeHandshakeRoles(t)
	writeShortLivedLeaf(t, active, expiring, nodeCN, caCert, caKey, san)
	before := map[string]string{}
	for _, role := range RolesForBox("compute-only") {
		path, _ := LeafPaths(active, role)
		before[role.Directory+"/"+role.Filename] = parseTestCert(t, path).SerialNumber.String()
	}

	output := filepath.Join(t.TempDir(), "renewed")
	changed, err := RenewTrustBundle(issuer, active, output, "compute-only", nodeCN, san)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Directory != expiring.Directory || changed[0].Filename != expiring.Filename {
		t.Fatalf("changed roles = %+v, want only %s/%s", changed, expiring.Directory, expiring.Filename)
	}
	for _, role := range RolesForBox("compute-only") {
		path, _ := LeafPaths(output, role)
		after := parseTestCert(t, path).SerialNumber.String()
		key := role.Directory + "/" + role.Filename
		if role.Directory == expiring.Directory && role.Filename == expiring.Filename {
			if after == before[key] {
				t.Fatalf("expiring leaf %s retained serial %s", key, after)
			}
		} else if after != before[key] {
			t.Fatalf("safe leaf %s serial changed from %s to %s", key, before[key], after)
		}
	}
}

func TestIssueTrustBundleRejectsSymlinkAliasWithoutDeletingCAKey(t *testing.T) {
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "issuer-alias")
	if err := os.Symlink(issuer, alias); err != nil {
		t.Fatal(err)
	}
	if err := IssueTrustBundle(issuer, alias, "compute-only", "compute-11.faas", AltNames{}); err == nil {
		t.Fatal("IssueTrustBundle accepted a symlink staging root")
	}
	_, caKey := CARoot(issuer)
	if _, err := os.Stat(caKey); err != nil {
		t.Fatalf("issuer CA key changed after rejected symlink alias: %v", err)
	}
}

func TestExportTrustBundleNeverCopiesCAKey(t *testing.T) {
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-12.faas"
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(t.TempDir(), "exported")
	if err := ExportTrustBundle(active, exported, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	_, caKey := CARoot(exported)
	if _, err := os.Stat(caKey); !os.IsNotExist(err) {
		t.Fatalf("exported bundle contains CA key: %v", err)
	}
}

func TestRenewalExportRepairsMissingTransportSANs(t *testing.T) {
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	nodeCN := "compute-san-drift.faas"
	active := filepath.Join(t.TempDir(), "active")
	if err := IssueTrustBundle(issuer, active, "compute-only", nodeCN, AltNames{}); err != nil {
		t.Fatal(err)
	}
	requiredSAN := AltNames{DNSNames: []string{"compute-san-drift.internal"}}
	strictExport := filepath.Join(t.TempDir(), "strict")
	if err := ExportTrustBundle(active, strictExport, "compute-only", nodeCN, requiredSAN); err == nil {
		t.Fatal("strict export accepted a bundle with missing transport SANs")
	}

	renewalExport := filepath.Join(t.TempDir(), "renewal")
	if err := ExportTrustBundleForRenewal(active, renewalExport, "compute-only"); err != nil {
		t.Fatalf("renewal export rejected repairable SAN drift: %v", err)
	}
	_, exportedCAKey := CARoot(renewalExport)
	if _, err := os.Stat(exportedCAKey); !os.IsNotExist(err) {
		t.Fatalf("renewal export contains CA key: %v", err)
	}

	candidate := filepath.Join(t.TempDir(), "candidate")
	changed, err := RenewTrustBundle(issuer, renewalExport, candidate, "compute-only", nodeCN, requiredSAN)
	if err != nil {
		t.Fatalf("renew SAN-drifted bundle: %v", err)
	}
	if len(changed) != len(RolesForBox("compute-only")) {
		t.Fatalf("renewed %d leaves, want %d leaves missing the transport SAN", len(changed), len(RolesForBox("compute-only")))
	}
	if err := ValidateTrustBundleForNode(candidate, "compute-only", requiredSAN, nodeCN); err != nil {
		t.Fatalf("renewed bundle remains invalid: %v", err)
	}
}

func computeHandshakeRoles(t *testing.T) (Role, Role) {
	t.Helper()
	var server, client Role
	for _, role := range RolesForBox("compute-only") {
		switch {
		case role.Directory == "vmmd" && role.Filename == "server":
			server = role
		case role.Directory == "builderd" && role.Filename == "vmmd-client":
			client = role
		}
	}
	if server.Filename == "" || client.Filename == "" {
		t.Fatal("compute trust set lacks vmmd server or builderd client role")
	}
	return server, client
}

func writeShortLivedLeaf(t *testing.T, root string, role Role, commonName string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, extra AltNames) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	sans := mergeAltNames(mergeAltNames(LocalDevSANs(), role.AltNames), extra)
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     sans.DNSNames,
		IPAddresses:  sans.IPAddresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, _ := LeafPaths(root, role)
	if err := writeLeaf(certPath,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		t.Fatal(err)
	}
}

func assertMutualTLSHandshake(t *testing.T, root string, serverRole, clientRole Role) {
	t.Helper()
	caPath, _ := CARoot(root)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append CA")
	}
	serverCertPath, serverKeyPath := LeafPaths(root, serverRole)
	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPath, clientKeyPath := LeafPaths(root, clientRole)
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	serverTLS := tls.Server(serverSide, &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	})
	clientTLS := tls.Client(clientSide, &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		ServerName:   "vmmd.faas",
		MinVersion:   tls.VersionTLS13,
	})
	serverResult := make(chan error, 1)
	go func() { serverResult <- serverTLS.Handshake() }()
	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

func assertMutualTLSHealthRPC(t *testing.T, root string, serverRole, clientRole Role) {
	t.Helper()
	caPath, _ := CARoot(root)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append RPC CA")
	}
	serverCertPath, serverKeyPath := LeafPaths(root, serverRole)
	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPath, clientKeyPath := LeafPaths(root, clientRole)
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	})))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-serveErr
	})
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS13,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("mTLS health RPC: %v", err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health RPC status = %s", response.Status)
	}
}

func copyTreeForPKITest(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, info.Mode().Perm())
	}); err != nil {
		t.Fatalf("copy trust tree: %v", err)
	}
}
