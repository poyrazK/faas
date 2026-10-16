//go:build metal

package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
)

const fixtureRepository = "gregale/ci-native-fixture"
const fixtureContents = "gregale-ci-native-v1"

type nativeFixture struct {
	config       Config
	dir          string
	journal      Journal
	pool         *pgxpool.Pool
	state        *state.PgStore
	store        *ci.PostgresStore
	physical     *ci.GCSObjects
	blobs        *ci.ManagedBlobs
	github       *grpc.Server
	h            *e2etest.Harness
	client       *api.Client
	key          string
	ciConfig     ci.Config
	job          api.JobResponse
	registry     *e2etest.FakeRegistry
	proxy        *http.Server
	target       atomic.Pointer[url.URL]
	lostAckArmed atomic.Bool
	lostAck      atomic.Bool
	sourceReads  atomic.Int32
	apidKilled   bool
	scheddKilled bool
	closed       bool
}

func newNativeFixture(t *testing.T) *nativeFixture {
	t.Helper()
	c, err := Load(os.Getenv)
	mustNative(t, err)
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("CI native acceptance requires root on dedicated native Linux amd64")
	}
	info, err := os.Stat("/dev/kvm")
	if err != nil || info.Mode()&os.ModeCharDevice == 0 || os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("CI native acceptance requires KVM, a staged kernel and enabled PostgreSQL; skips cannot qualify")
	}
	dir := os.Getenv(EvidenceEnv)
	if _, err := os.Lstat(filepath.Join(dir, "journal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("use a fresh evidence directory; reconcile the previous journal first")
	}
	id, err := DatabaseIdentity(c.DatabaseURL)
	mustNative(t, err)
	f := &nativeFixture{config: c, dir: dir, journal: NewJournal(c, id)}
	f.save(t)
	// Register before provisioning so partial setup always attempts cleanup.
	t.Cleanup(func() { f.close(t) })
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	f.pool, err = CreateSchema(ctx, c.DatabaseURL, f.journal)
	mustNative(t, err)
	f.state = state.NewPgStore(f.pool)
	account, err := f.state.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "ci-native-" + f.journal.RunID + "@test.example", Plan: api.PlanPro})
	mustNative(t, err)
	f.journal.AccountID = account.Account.ID
	f.save(t)
	f.key = nativeKey(t, f.state, account.Account.ID, api.ScopesAdminOnly)
	f.physical, err = ci.NewGCSObjects(ctx, c.Bucket)
	mustNative(t, err)
	f.reconnect()
	f.ciConfig = ci.ConfigFromEnv(func(key string) string {
		switch key {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv, ci.WorkerEnv:
			return "1"
		case ci.AccountsEnv:
			return f.journal.AccountID
		}
		return ""
	})
	f.reconnect()
	f.startTLS(t)
	githubSocket := f.startGitHub(t)
	mustNative(t, f.state.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: account.Account.ID, InstallationID: 12, DefaultBranch: "main", AuditGithubLogin: "gregale-ci-fixture", SealedToken: []byte("synthetic-seal-not-a-credential"), TokenExpiresAt: time.Now().Add(time.Hour)}))
	// The fake registry bootstraps only the platform fixture base and wake
	// probe. The CI runner itself is the supplied immutable real OCI image.
	f.registry = e2etest.NewFakeRegistry()
	base, _ := e2etest.HelloImage("ci-native/base", "")
	e2etest.OverrideBuilderBase(t, f.registry.AddImage("ci-native/base", base))
	deploy, _ := e2etest.BaseLayerImage("ci-native/deploy", "ci-native-wake")
	e2etest.OverrideDeployBase(t, f.registry.AddImage("ci-native/deploy", deploy))
	t.Setenv("DATABASE_URL", c.DatabaseURL)
	t.Setenv("FAAS_JOBS_DISPATCH", "1")
	t.Setenv("FAAS_SAMPLE_INTERVAL", "1s")
	t.Setenv("FAAS_ROLLUP_INTERVAL", "1s")
	env := []string{
		ci.InternalEnv + "=1", ci.GitHubEnv + "=1", ci.DispatchEnv + "=1", ci.TransportEnv + "=1", ci.WorkerEnv + "=1",
		"FAAS_GITHUBD_SOCKET=" + githubSocket,
		ci.RetentionEnv + "=0", ci.AccountsEnv + "=" + account.Account.ID,
		ci.RuntimeURLEnv + "=" + c.Origin, ci.StorageBucketEnv + "=" + c.Bucket,
		"FAAS_OCI_INSECURE=1", // Loopback platform fixture registry only.
	}
	for _, name := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	// Match the normal Jobs host sealing boundary with fresh fixture keys.
	identity, err := age.GenerateX25519Identity()
	mustNative(t, err)
	keyPath, pubPath := filepath.Join(dir, "host.age"), filepath.Join(dir, "host.age.pub")
	mustNative(t, os.WriteFile(keyPath, []byte(identity.String()), 0400))
	mustNative(t, os.WriteFile(pubPath, []byte(identity.Recipient().String()), 0444))
	env = append(env, "FAAS_HOST_AGE_IDENTITY_PATH="+keyPath, "FAAS_HOST_AGE_RECIPIENT_PATH="+pubPath)
	f.h = e2etest.Start(t, f.pool, e2etest.APID|e2etest.Schedd|e2etest.VMMD|e2etest.Imaged|e2etest.Meterd|e2etest.Gatewayd|e2etest.GatewaydPublic, env...)
	// Run the intent drain before the harness's own subprocess cleanup. The
	// earlier registration still covers setup failures; close is idempotent.
	t.Cleanup(func() { f.close(t) })
	f.journal.HarnessDir = f.h.TmpDir
	f.save(t)
	target, err := url.Parse(f.h.APIDURL)
	mustNative(t, err)
	f.target.Store(target)
	f.client = api.NewClient(f.h.APIDURL, f.key)
	f.job, err = f.client.CreateJob(ctx, api.CreateJobRequest{Name: "ci-native-runner", ImageRef: c.Image, Command: []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, RAMMB: api.CIJobsRAMMB, TaskTimeoutSec: 600, RetryMax: 0})
	mustNative(t, err)
	nativeEventually(t, 3*time.Minute, "materialized pinned runner", func() bool {
		job, err := f.client.GetJob(t.Context(), f.job.Name)
		if err != nil {
			return false
		}
		if job.ImageMaterializationStatus == "failed" {
			t.Fatal("native runner image materialization failed")
		}
		f.job = job
		return job.ImageMaterializationStatus == "ready" && job.ImageStorageKey != "" && job.ImageResolvedDigest == strings.Split(c.Image, "@")[1]
	})
	return f
}

func (f *nativeFixture) reconnect() {
	f.store = ci.NewPostgresStore(f.pool)
	f.blobs = &ci.ManagedBlobs{Store: f.store, Physical: f.physical}
}

func (f *nativeFixture) verify(_ context.Context, account string, installation, repository int64, name string) error {
	if account != f.journal.AccountID || installation != 12 || repository != 34 || name != fixtureRepository {
		return ci.ErrGitHubAccess
	}
	return nil
}

type nativeSource struct {
	githubdgrpc.UnimplementedService
	fixture *nativeFixture
}

func (s nativeSource) ListInstallableRepos(account string, installation int64) ([]githubdgrpc.Repo, error) {
	if err := s.fixture.verify(context.Background(), account, installation, 34, fixtureRepository); err != nil {
		return nil, err
	}
	return []githubdgrpc.Repo{{ID: 34, FullName: fixtureRepository, DefaultBranch: "main"}}, nil
}

func (f *nativeFixture) startGitHub(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(f.dir, "githubd.sock")
	listener, err := net.Listen("unix", socket)
	mustNative(t, err)
	f.github = grpc.NewServer()
	githubdgrpc.New(nativeSource{fixture: f}, nil, nil).Register(f.github)
	go func() { _ = f.github.Serve(listener) }()
	return socket
}

func (s nativeSource) StreamSourceRef(ctx context.Context, account string, installation int64, repository string, sha string, bound int64) (io.ReadCloser, string, bool, int64, error) {
	if err := s.fixture.verify(ctx, account, installation, 34, repository); err != nil || sha != strings.Repeat("a", 40) || bound != api.CIJobsSourceArchiveMaxBytes {
		return nil, "", false, 0, ci.ErrGitHubAccess
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tarball := tar.NewWriter(gz)
	if err := tarball.WriteHeader(&tar.Header{Name: "fixture/fixture.txt", Mode: 0644, Size: int64(len(fixtureContents))}); err != nil {
		return nil, "", false, 0, err
	}
	if _, err := io.WriteString(tarball, fixtureContents); err != nil {
		return nil, "", false, 0, err
	}
	if err := tarball.Close(); err != nil {
		return nil, "", false, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, "", false, 0, err
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), sha, false, int64(buf.Len()), nil
}

func (f *nativeFixture) startTLS(t *testing.T) {
	t.Helper()
	certificate, err := tls.LoadX509KeyPair(f.config.TLSCert, f.config.TLSKey)
	mustNative(t, err)
	listener, err := net.Listen("tcp", f.config.Listen)
	mustNative(t, err)
	origin, _ := url.Parse(f.config.Origin)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := f.target.Load()
		if target == nil || r.Host != origin.Host || !strings.HasPrefix(r.URL.Path, "/v1/internal/ci/") {
			http.NotFound(w, r)
			return
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorLog = log.New(io.Discard, "", 0)
		proxy.Director = func(req *http.Request) {
			req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
			req.Host = origin.Host
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		proxy.ModifyResponse = func(response *http.Response) error {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				if strings.HasSuffix(response.Request.URL.Path, "/source") {
					f.sourceReads.Add(1)
				}
				if strings.HasSuffix(response.Request.URL.Path, "/objects") && f.lostAckArmed.CompareAndSwap(true, false) {
					f.lostAck.Store(true)
					_ = response.Body.Close()
					return errors.New("injected lost upload acknowledgement")
				}
			}
			return nil
		}
		proxy.ServeHTTP(w, r)
	})
	f.proxy = &http.Server{Handler: handler, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = f.proxy.ServeTLS(listener, "", "") }()
}

func (f *nativeFixture) save(t *testing.T) {
	t.Helper()
	mustNative(t, WriteJournal(f.dir, f.journal))
}

func (f *nativeFixture) close(t *testing.T) {
	if f.closed {
		return
	}
	f.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if f.h != nil {
		if f.apidKilled {
			_ = f.h.RestartAPID()
		}
		if f.scheddKilled {
			_ = f.h.RestartSchedd()
		}
	}
	err := Cleanup(ctx, f.config.DatabaseURL, f.config.Bucket, f.dir)
	if f.h != nil {
		f.h.Stop()
	}
	if f.proxy != nil {
		_ = f.proxy.Close()
	}
	if f.github != nil {
		f.github.Stop()
	}
	if f.registry != nil {
		f.registry.Close()
	}
	if f.physical != nil {
		_ = f.physical.Close()
	}
	if f.pool != nil {
		f.pool.Close()
	}
	if err != nil {
		t.Error(err)
		return // Preserve schema, journals and native images after any failed drain.
	}
	if err := Finalize(ctx, f.config.DatabaseURL, f.dir); err != nil {
		t.Error(err)
	}
}

func nativeKey(t *testing.T, store *state.PgStore, account string, scopes []string) string {
	t.Helper()
	key, hash, err := api.GenerateAPIKey()
	mustNative(t, err)
	_, err = store.CreateAPIKey(t.Context(), account, hash, "ci-native-fixture", scopes)
	mustNative(t, err)
	return key
}

func mustNative(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func nativeEventually(t *testing.T, limit time.Duration, label string, check func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native CI timed out waiting for " + label)
		case <-ticker.C:
		}
	}
}

func (f *nativeFixture) nativeStep(name, run string) ci.Job {
	verify := fmt.Sprintf(`printf '%%s  %s\n' '%s' | sha256sum -c -
test "$(cat fixture.txt)" = '%s'`, ci.JobsRunnerPath, f.config.RunnerSHA, fixtureContents)
	return ci.Job{Image: f.config.Image, TimeoutSeconds: 180, Steps: []ci.Step{{Name: "identity", Run: verify}, {Name: name, Run: run}}, Artifacts: []string{"reports/**"}}
}

// ciRequest uses the public HTTPS origin and ordinary account authentication.
// Native task control is driven exclusively by the apid worker, never by Tick.
func (f *nativeFixture) ciRequest(t *testing.T, method, path string, body any, key string, out any) bool {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		mustNative(t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.config.Origin+"/v1/internal/ci/"+path, reader)
	mustNative(t, err)
	req.Header.Set("Authorization", "Bearer "+f.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	mustNative(t, err)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("native CI HTTP request failed: %s %s status=%d", method, path, response.StatusCode)
	}
	if strings.HasPrefix(path, "executions") && response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("execution response is cacheable")
	}
	if out != nil {
		mustNative(t, json.NewDecoder(io.LimitReader(response.Body, api.CIExecutionSnapshotMaxBytes)).Decode(out))
	}
	return response.Header.Get("Idempotency-Replayed") == "true"
}
