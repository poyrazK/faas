package e2etest

// adr: 20261117190000 — end-to-end acceptance for compute store grants.
//
// FAAS_E2E_STORAGE_GRANTS=1 runs the harness against a real S3 store (a
// loopback MinIO from FAAS_E2E_MINIO_BIN) with the compute daemons (imaged,
// builderd, vmmd) holding no store key: they reach every object through
// secretd's GrantStorageObjects, as the default-local node, over mutual TLS.
// The control-plane daemons (apid, schedd) keep the key, and schedd runs the
// object GC (deferred deletes plus the jobs/ and sources/ sweep). Teardown
// fails the test if no grant was issued or secretd refused one.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/onebox-faas/faas/pkg/pki"
)

// StorageGrantsEnv opts a harness run into store grants.
const StorageGrantsEnv = "FAAS_E2E_STORAGE_GRANTS"

// MinioDataEnv, when set, is a MinIO data directory kept across tests and
// runs (default: a fresh one per test).
const MinioDataEnv = "FAAS_E2E_MINIO_DATA"

const (
	storageGrantsBucket = "faas-e2e-artifacts"
	storageGrantsRegion = "us-east-1"
	storageGrantsNode   = "default-local"
)

// StorageGrantsRequested reports whether this run uses store grants.
func StorageGrantsRequested() bool { return os.Getenv(StorageGrantsEnv) == "1" }

// storageGrants is the per-harness grant world.
type storageGrants struct {
	// controlEnv is what every daemon gets: the S3 store with its key.
	controlEnv []string
	// computeEnv follows controlEnv for imaged, builderd and vmmd: no key,
	// deletes deferred, every object through secretd.
	computeEnv []string
	// secretd's process; teardown reads its output for refusals.
	secretd *exec.Cmd
	// Endpoint, AccessKey and SecretKey reach MinIO directly.
	Endpoint, AccessKey, SecretKey string
}

// StorageGrantsOn reports whether this harness runs on store grants.
func (h *Harness) StorageGrantsOn() bool { return h.grants != nil }

// StorageGrantsComputeEnv is the environment of a granted compute daemon in
// this harness, or nil when grants are off.
func (h *Harness) StorageGrantsComputeEnv() []string {
	if h.grants == nil {
		return nil
	}
	return append(append([]string(nil), h.grants.controlEnv...), h.grants.computeEnv...)
}

func (h *Harness) startStorageGrants(t *testing.T, bin, dbURL string) {
	t.Helper()
	minio := os.Getenv("FAAS_E2E_MINIO_BIN")
	if minio == "" {
		t.Fatalf("e2etest: %s=1 needs FAAS_E2E_MINIO_BIN (a minio server binary)", StorageGrantsEnv)
	}
	if err := ensureSecretd(bin); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.TmpDir, "storage-grants")
	dataDir := filepath.Join(dir, "minio")
	g := &storageGrants{AccessKey: "e2e" + randHex(t, 6), SecretKey: randHex(t, 20)}
	if persistent := os.Getenv(MinioDataEnv); persistent != "" {
		// A store that outlives the test, as a fleet's bucket does: bases,
		// scans and kernels published by an earlier run are reused instead
		// of staged and scanned again inside a test's deploy deadline.
		dataDir = persistent
		g.AccessKey, g.SecretKey = persistentMinioCredentials(t, persistent)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	addr := freeTCPAddr(t)
	g.Endpoint = "http://" + addr
	cmd := exec.Command(minio, "server", dataDir, "--address", addr, "--console-address", freeTCPAddr(t), "--quiet")
	cmd.Env = append(os.Environ(), "MINIO_ROOT_USER="+g.AccessKey, "MINIO_ROOT_PASSWORD="+g.SecretKey)
	minioLog, err := os.Create(filepath.Join(dir, "minio.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = minioLog.Close() }()
	cmd.Stdout, cmd.Stderr = minioLog, minioLog
	if err := cmd.Start(); err != nil {
		t.Fatalf("e2etest: start minio: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitHTTPOK(t, g.Endpoint+"/minio/health/live", 30*time.Second, minioLog.Name())
	client := s3.New(s3.Options{Region: storageGrantsRegion, BaseEndpoint: aws.String(g.Endpoint), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(g.AccessKey, g.SecretKey, "")})
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(storageGrantsBucket)}); err != nil {
		var owned *s3types.BucketAlreadyOwnedByYou
		if !errors.As(err, &owned) {
			t.Fatalf("e2etest: create bucket: %v", err)
		}
	}
	publishKernels(t, client)

	// secretd's leaf and the node's: the handshake admits the node's CN only
	// because default-local is a registered compute node.
	pkiRoot := filepath.Join(dir, "pki")
	if _, _, err := pki.EnsureCA(pkiRoot, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(pkiRoot, pkiRoot, pki.FeatureSecretOpener, "control-plane"); err != nil {
		t.Fatal(err)
	}
	nodeRoot := filepath.Join(dir, "node")
	if _, err := pki.EnsureFeatureLeavesForNode(pkiRoot, nodeRoot, pki.FeatureSecretOpener, "compute-only", storageGrantsNode); err != nil {
		t.Fatal(err)
	}
	caPath, _ := pki.CARoot(pkiRoot)
	serverCert, serverKey := pki.LeafPaths(pkiRoot, pki.FeatureRoles(pki.FeatureSecretOpener, "control-plane")[0])
	clientCert, clientKey := pki.LeafPaths(nodeRoot, pki.FeatureRoles(pki.FeatureSecretOpener, "compute-only")[0])
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "runtime.age")
	secretFile := filepath.Join(dir, "s3-secret")
	// secretbox refuses an identity anything but its owner may write.
	if err := os.WriteFile(identity, []byte(id.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte(g.SecretKey), 0o600); err != nil {
		t.Fatal(err)
	}
	secretdAddr := freeTCPAddr(t)
	env := append(testEnvCommon(dbURL),
		"FAAS_SECRETD_LISTEN=tcp://"+secretdAddr,
		"FAAS_SECRETD_TLS_CERT_PATH="+serverCert, "FAAS_SECRETD_TLS_KEY_PATH="+serverKey, "FAAS_SECRETD_TLS_CA_PATH="+caPath,
		"FAAS_SECRETD_RUNTIME_IDENTITY="+identity,
		"FAAS_SECRETD_STORAGE_GRANTS=s3", "FAAS_SECRETD_STORAGE_BUCKET="+storageGrantsBucket,
		"FAAS_SECRETD_STORAGE_S3_ENDPOINT="+g.Endpoint, "FAAS_SECRETD_STORAGE_S3_REGION="+storageGrantsRegion,
		"FAAS_SECRETD_STORAGE_S3_ACCESS_KEY="+g.AccessKey, "FAAS_SECRETD_STORAGE_S3_SECRET_KEY_FILE="+secretFile,
	)
	g.secretd = startProc(t, bin, "secretd", env)
	h.procs = append(h.procs, g.secretd)
	waitTCP(t, secretdAddr, 30*time.Second)

	g.controlEnv = []string{
		"FAAS_STORAGE_BACKEND=s3",
		"FAAS_S3_BUCKET=" + storageGrantsBucket,
		"FAAS_S3_ENDPOINT=" + g.Endpoint,
		"FAAS_S3_REGION=" + storageGrantsRegion,
		"FAAS_S3_PATH_STYLE=1",
		"FAAS_S3_ACCESS_KEY_ID=" + g.AccessKey,
		"FAAS_S3_SECRET_ACCESS_KEY=" + g.SecretKey,
		"FAAS_STORAGE_CACHE_DIR=" + filepath.Join(dir, "cache"),
		// As on a production compute node: no prefix stays on node disk,
		// so snapshots, bases, kernels, layers and scans go through grants.
		"FAAS_STORAGE_LOCAL_PREFIXES=none",
		"FAAS_REQUIRE_SHARED_ARTIFACTS=1",
	}
	g.computeEnv = []string{
		// Empty is unset: the node holds no key at all (slice 5).
		"FAAS_S3_ACCESS_KEY_ID=",
		"FAAS_S3_SECRET_ACCESS_KEY=",
		"FAAS_STORAGE_ACCESS=secretd",
		"FAAS_STORAGE_DELETE_MODE=defer",
		"FAAS_SECRETD_TARGET=tcp://" + secretdAddr,
		"FAAS_SECRETD_TLS_CERT_PATH=" + clientCert,
		"FAAS_SECRETD_TLS_KEY_PATH=" + clientKey,
		"FAAS_SECRETD_TLS_CA_PATH=" + caPath,
	}
	h.grants = g
	t.Logf("e2etest: storage grants on: minio %s, secretd %s, compute daemons hold no store key", g.Endpoint, secretdAddr)
}

// checkStorageGrants fails the test when the run never went through grants
// or secretd refused the node: a passing run must prove the path.
func (h *Harness) checkStorageGrants(t *testing.T) {
	t.Helper()
	if h.grants == nil {
		return
	}
	var grants, objects int
	if err := h.Pool.QueryRow(context.Background(),
		`select count(*), coalesce(sum(objects), 0) from storage_grant_audit`).Scan(&grants, &objects); err != nil {
		t.Errorf("e2etest: read storage_grant_audit: %v", err)
		return
	}
	rows, err := h.Pool.Query(context.Background(),
		`select subject_class, access, sum(objects) from storage_grant_audit group by 1, 2 order by 1, 2`)
	if err == nil {
		for rows.Next() {
			var class, access string
			var n int64
			if rows.Scan(&class, &access, &n) == nil {
				t.Logf("e2etest: storage grants: %s %s %d objects", class, access, n)
			}
		}
		rows.Close()
	}
	if out, ok := h.grants.secretd.Stdout.(*safeBuffer); ok {
		if n := strings.Count(out.String(), "secretd: refused storage grant"); n > 0 {
			t.Errorf("e2etest: secretd refused %d storage grant requests; see its log", n)
		}
	}
	if grants == 0 && !t.Failed() {
		t.Errorf("e2etest: storage grants were on but secretd issued none: the compute daemons did not use them")
	}
	t.Logf("e2etest: storage grants: %d grant requests, %d objects", grants, objects)
}

var secretdBuild struct {
	once sync.Once
	err  error
}

func ensureSecretd(dir string) error {
	secretdBuild.once.Do(func() {
		if binaryPresent(filepath.Join(dir, "secretd")) {
			return
		}
		mod, err := moduleImportPath()
		if err != nil {
			secretdBuild.err = err
			return
		}
		args := []string{"build"}
		if daemonBuildTags != "" {
			args = append(args, "-tags", daemonBuildTags)
		}
		args = append(args, "-o", filepath.Join(dir, "secretd"), mod+"/cmd/secretd")
		var out bytes.Buffer
		cmd := exec.Command("go", args...)
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			secretdBuild.err = fmt.Errorf("e2etest: go build secretd: %w\n%s", err, out.String())
		}
	})
	return secretdBuild.err
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func waitHTTPOK(t *testing.T, url string, d time.Duration, logPath string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec,noctx // loopback test fixture
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, _ := os.ReadFile(logPath)
	t.Fatalf("e2etest: %s not ready after %s\n%s", url, d, out)
}

// publishKernels does what `gregalectl artifact publish` does on a real
// fleet: it copies the host's release kernels into the shared store under
// kernel/<version>, with the control plane's key. Compute nodes never
// write kernels, and under grants nothing else stays on node disk.
func publishKernels(t *testing.T, client *s3.Client) {
	t.Helper()
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	entries, err := os.ReadDir(filepath.Join(root, "kernel"))
	if err != nil {
		t.Fatalf("e2etest: storage grants need the host's kernels: %v", err)
	}
	published := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "kernel", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String(storageGrantsBucket),
			Key: aws.String("kernel/" + e.Name()), Body: bytes.NewReader(body)})
		if err != nil {
			t.Fatalf("e2etest: publish kernel/%s: %v", e.Name(), err)
		}
		published++
	}
	if published == 0 {
		t.Fatalf("e2etest: storage grants need a kernel in %s", filepath.Join(root, "kernel"))
	}
}

// persistentMinioCredentials returns the root credentials of a persistent
// MinIO data directory, creating them on first use: MinIO keeps its config
// under the root credentials it started with.
func persistentMinioCredentials(t *testing.T, dataDir string) (string, string) {
	t.Helper()
	path := dataDir + ".credentials"
	if raw, err := os.ReadFile(path); err == nil {
		if user, secret, ok := strings.Cut(strings.TrimSpace(string(raw)), ":"); ok && user != "" && secret != "" {
			return user, secret
		}
		t.Fatalf("e2etest: malformed %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	user, secret := "e2e"+randHex(t, 6), randHex(t, 20)
	if err := os.WriteFile(path, []byte(user+":"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return user, secret
}
