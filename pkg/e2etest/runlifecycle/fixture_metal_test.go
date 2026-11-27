//go:build metal

package runlifecycle_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// This is a dedicated acceptance package. Missing fixtures are failures, never
// skipped native evidence. The runner supplies a private copy of a real Node
// base patched with this source tree's guest-init; it never changes host bases.
type runFixture struct {
	webhookRecipient  *age.X25519Recipient
	h                 *e2etest.Harness
	client            *api.Client
	accountID         string
	runs              map[string]bool
	root              string
	storageRoot       string
	nativeRoot        string
	identity          sched.RuntimeSnapshotIdentity
	templateAccountID string
	artifacts         sched.ExecutionRuntimeArtifacts
	barrierRoot       string
}

func newRunFixture(t *testing.T) *runFixture { return newRunFixtureWithTemplates(t, false) }

func newRunFixtureWithTemplates(t *testing.T, templates bool) *runFixture {
	t.Helper()
	return startRunFixture(t, templates, nil)
}

func startRunFixture(t *testing.T, templates bool, apidEnv []string) *runFixture {
	t.Helper()
	return startRunFixtureWithServices(t, templates, apidEnv, nil, 0)
}

func startRunFixtureWithServices(t *testing.T, templates bool, apidEnv []string, configure func(string) []string, additional e2etest.Which) *runFixture {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("Run lifecycle acceptance requires root on the designated Linux amd64 KVM test host")
	}
	for _, path := range []string{"/dev/kvm", "/etc/faas/builder-acceptance-host", "/sys/fs/cgroup/cgroup.controllers"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("acceptance prerequisite %s: %v", path, err)
		}
	}
	for _, key := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_TEST_FC_VERSION", "FAAS_TEST_EXECUTION_BASE", "FAAS_GUEST_INIT", "FAAS_TEST_RUN_FIXTURE_ROOT"} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			t.Fatalf("required acceptance fixture %s is unset", key)
		}
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("Run lifecycle acceptance cannot skip PostgreSQL")
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	// The operator runner retires this directory only after physical leakcheck.
	// Retain keys, inputs and journals if a failed crash test needs recovery.
	parent := os.Getenv("FAAS_TEST_RUN_FIXTURE_ROOT")
	root := filepath.Join(parent, t.Name())
	if !filepath.IsAbs(root) {
		t.Fatal("Run lifecycle fixture root must be absolute")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	harnesses := filepath.Join(root, "harnesses")
	if err := os.Mkdir(harnesses, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "1")
	t.Setenv("GREGALE_CI_ACCEPTANCE_EVIDENCE", harnesses)
	storageRoot := filepath.Join(root, "storage")
	baseKey, layerKey := "base/runner-node24-amd64.ext4", "layers/run-node24.ext4"
	if additional&e2etest.Imaged != 0 {
		// The deployed function must use imaged's separately staged and
		// scanned runtime release. Keep the exact Run fixture backing apart.
		baseKey = "base/run-execution-node24-fixture-amd64.ext4"
	}
	// vmmd's cold path resolves the kernel through its Firecracker-version key.
	kernelKey := "kernel/" + os.Getenv("FAAS_TEST_FC_VERSION")
	baseDigest := stageRunFile(t, os.Getenv("FAAS_TEST_EXECUTION_BASE"), filepath.Join(storageRoot, baseKey))
	// Synthetic admission identity for these exact ext4 bytes. This is not an
	// OCI manifest digest or evidence of a vulnerability scan.
	fixtureBaseRef := "gregale.invalid/run-lifecycle-node24@sha256:" + baseDigest
	kernelDigest := stageRunFile(t, os.Getenv("FAAS_TEST_KERNEL"), filepath.Join(storageRoot, kernelKey))
	initDigest := hashRunFile(t, os.Getenv("FAAS_GUEST_INIT"))
	stageRunLayer(t, filepath.Join(storageRoot, layerKey), filepath.Join(root, "layer"))
	// Test-only admission fixture, like agentchain.admitAgentBase. This is
	// deliberately not a Grype scan or image-release qualification receipt.
	scan := filepath.Join(storageRoot, wire.ScanKeyForBaseKey(baseKey))
	if err := os.MkdirAll(filepath.Dir(scan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scan, []byte(`{"image":"`+fixtureBaseRef+`","findings":{"CRITICAL":0},"fix_available_findings":{"CRITICAL":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FAAS_RUN_ENVIRONMENT_QUALIFICATION") == "1" {
		builderPath := os.Getenv("FAAS_TEST_ENVIRONMENT_BUILDER_BASE")
		if builderPath == "" {
			t.Fatal("exact-source builder base is required")
		}
		builderDigest := stageRunFile(t, builderPath, filepath.Join(storageRoot, "base/runner-builder-amd64.ext4"))
		builderScan := filepath.Join(storageRoot, "scans/runner-builder-amd64.ext4.scan.json")
		// The platform builder fixture follows the same explicit test-only scan
		// boundary as Node above. Newly produced customer environments are
		// scanned by real Grype before publication.
		if err := os.WriteFile(builderScan, []byte(`{"image":"gregale.invalid/run-builder@sha256:`+builderDigest+`","findings":{"CRITICAL":0},"fix_available_findings":{"CRITICAL":0}}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fleetIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	// schedd loads the rotation set by directory, using canonical basenames.
	privatePath, publicPath := filepath.Join(root, "host.age"), filepath.Join(root, "host.age.pub")
	fleetPrivatePath, fleetPublicPath := filepath.Join(root, "fleet.age"), filepath.Join(root, "fleet.age.pub")
	if err := os.WriteFile(privatePath, []byte(identity.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, []byte(identity.Recipient().String()), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetPrivatePath, []byte(fleetIdentity.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetPublicPath, []byte(fleetIdentity.Recipient().String()), 0o444); err != nil {
		t.Fatal(err)
	}
	nativeRoot := filepath.Join(root, "native-snap")
	if err := os.Mkdir(nativeRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	barrierRoot := filepath.Join(root, "barrier")
	if err := os.Mkdir(barrierRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"FAAS_STORAGE_BACKEND=local", "FAAS_STORAGE_ROOT=" + storageRoot, "FAAS_NATIVE_RUNTIME_SNAPSHOT_ROOT=" + nativeRoot,
		"FAAS_DEPLOY_BASE_REF_NODE24=" + fixtureBaseRef,
		"FAAS_EXECUTION_API_ENABLED=1", "FAAS_EXECUTION_DISPATCH=1",
		"FAAS_HOST_KEY_PATH=" + privatePath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + privatePath, "FAAS_HOST_AGE_RECIPIENT_PATH=" + publicPath,
		"FAAS_FLEET_AGE_IDENTITY_PATH=" + fleetPrivatePath, "FAAS_FLEET_AGE_RECIPIENT_PATH=" + fleetPublicPath,
		"FAAS_SAMPLE_INTERVAL=1s", "FAAS_ROLLUP_INTERVAL=1s",
		"FAAS_EXECUTION_NODE24_ARCH=amd64", "FAAS_EXECUTION_NODE24_KERNEL_DIGEST=" + kernelDigest,
		"FAAS_EXECUTION_NODE24_EXECUTOR_DIGEST=" + initDigest, "FAAS_EXECUTION_NODE24_BASE_DIGEST=" + baseDigest,
		"FAAS_EXECUTION_NODE24_KERNEL_KEY=" + kernelKey, "FAAS_EXECUTION_NODE24_BASE_KEY=" + baseKey,
		"FAAS_EXECUTION_NODE24_LAYER_KEY=" + layerKey, "FAAS_EXECUTION_NODE24_FC_VERSION=" + os.Getenv("FAAS_TEST_FC_VERSION"),
	}
	if os.Getenv("FAAS_RUN_ENVIRONMENT_QUALIFICATION") == "1" {
		pythonBase := environmentPythonBase
		if override := os.Getenv("FAAS_TEST_SYSTEM_ENVIRONMENT_BASE_REF"); override != "" {
			pythonBase = override
		}
		// Loopback is confined to these private acceptance daemon processes.
		env = append(env, "FAAS_EGRESS_ALLOW_LOOPBACK=1", "FAAS_EXECUTION_V2_ENABLED=1", "FAAS_EXECUTION_EXTENDED_ENABLED=1", "FAAS_EXECUTION_ENVIRONMENTS_ENABLED=1", "FAAS_EXECUTION_ENVIRONMENT_NODE24_BASE_REF="+environmentNodeBase,
			"FAAS_EXECUTION_ENVIRONMENT_PYTHON313_BASE_REF="+pythonBase,
			"FAAS_EXECUTION_PYTHON313_ARCH=amd64", "FAAS_EXECUTION_PYTHON313_KERNEL_DIGEST="+kernelDigest,
			"FAAS_EXECUTION_PYTHON313_EXECUTOR_DIGEST="+initDigest, "FAAS_EXECUTION_PYTHON313_BASE_DIGEST="+baseDigest,
			"FAAS_EXECUTION_PYTHON313_KERNEL_KEY="+kernelKey, "FAAS_EXECUTION_PYTHON313_BASE_KEY="+baseKey,
			"FAAS_EXECUTION_PYTHON313_LAYER_KEY="+layerKey, "FAAS_EXECUTION_PYTHON313_FC_VERSION="+os.Getenv("FAAS_TEST_FC_VERSION"))
	}
	if os.Getenv("FAAS_RUN_TASK_QUALIFICATION") == "1" {
		sessionKey := sha256.Sum256([]byte(uuid.NewString()))
		env = append(env, "FAAS_EXECUTION_V2_ENABLED=1", "FAAS_EXECUTION_TASKS_ENABLED=1", "FAAS_CAPABILITY_FLAGS_RELEASE_ALL=1", "FAAS_SESSION_KEY="+hex.EncodeToString(sessionKey[:]))
	}
	if os.Getenv("FAAS_RUN_FILE_QUALIFICATION") == "1" {
		env = append(env, "FAAS_EXECUTION_V2_ENABLED=1", "FAAS_SCHEDD_EXECUTION_DISPATCH_CONCURRENCY=2")
	}
	// Source Runs are networkless; an idle prepared-network pool would obscure
	// the zero-resource assertion made while vmmd is still running.
	// Version-7 ownership proofs let the successor retire one-shot survivors.
	// Default quarantine deliberately preserves them instead of guessing.
	recovery := "journal_adoption_v7"
	if os.Getenv("FAAS_RUN_LIFECYCLE_NATIVE_RECOVERY") == "1" {
		if os.Getenv("FAAS_RUN_NATIVE_PRIVATE_MOUNT_NS") != "1" {
			t.Fatal("native recovery cohort requires the runner's private mount namespace")
		}
		prepareNativeRunJail(t, "")
		images, publications := filepath.Join(root, "native-images"), filepath.Join(root, "native-publications")
		for _, path := range []string{images, publications} {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		e2etest.OverrideVMMDConfig(t, fmt.Sprintf("prepared_networks = 0\nnative_process_recovery = true\nnative_launch_owner_records = true\nnative_runtime_capture = true\nnative_runtime_snapshot_root = %q\nnative_image_staging_root = %q\nnative_snapshot_publication_root = %q\n", nativeRoot, images, publications))
		env = append(env, "FAAS_TEST_RUN_FIXTURE_ROOT="+parent, "GREGALE_CI_NATIVE_ACCEPTANCE=1", "FAAS_TEST_EXECUTION_TEMPLATE_BARRIER_ROOT="+barrierRoot)
		recovery = "native_process_recovery_owner_v3"
	} else {
		e2etest.OverrideVMMDConfig(t, fmt.Sprintf("prepared_networks = 0\nadoption_record = true\nadopt_running_vms = true\nnative_runtime_snapshot_root = %q\n", nativeRoot))
	}
	templateAccountID := ""
	if templates {
		captureAccount, err := state.NewPgStore(pool).CreateAccount(t.Context(), "run-template-"+uuid.NewString()+"@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		templateAccountID = captureAccount.ID
		if err := os.MkdirAll(filepath.Join(storageRoot, "snap"), 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, "FAAS_EXECUTION_TEMPLATE_CAPTURE=1", "FAAS_EXECUTION_TEMPLATE_ACCOUNT_ID="+templateAccountID,
			`FAAS_EXECUTION_TEMPLATE_SHAPES=[{"runtime":"node24","memory_mb":256,"ephemeral_disk_mb":64}]`,
			"FAAS_SCHEDD_EXECUTION_DISPATCH_CONCURRENCY=4")
	}
	services := e2etest.APID | e2etest.Schedd | e2etest.VMMD | e2etest.Meterd
	if os.Getenv("FAAS_RUN_ENVIRONMENT_QUALIFICATION") == "1" {
		services |= e2etest.Gatewayd
	}
	if configure != nil {
		env = append(env, configure(storageRoot)...)
	}
	h := e2etest.StartWithAPIDEnv(t, pool, services|additional, apidEnv, env...)
	t.Cleanup(func() {
		if t.Failed() {
			// Keep diagnostics in the runner's private on-node evidence. Export
			// only sanitized summaries, never raw daemon output.
			h.DumpLogs(t)
		}
	})
	if actual, err := os.ReadFile(publicPath); err != nil || strings.TrimSpace(string(actual)) != identity.Recipient().String() {
		t.Fatal("vmmd did not retain the fixture's private/public key pair")
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "run-lifecycle-"+uuid.NewString()+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	token, digest, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, digest, "run-lifecycle", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: runtime=node24 kernel_sha256=%s base_sha256=%s guest_init_sha256=%s fc=%s catalog_empty_before_startup=true scan=fixture recovery=%s", kernelDigest, baseDigest, initDigest, os.Getenv("FAAS_TEST_FC_VERSION"), recovery)
	return &runFixture{webhookRecipient: identity.Recipient(), h: h, client: api.NewClient(h.APIDURL, token), accountID: account.ID, runs: map[string]bool{}, root: root, storageRoot: storageRoot, nativeRoot: nativeRoot, templateAccountID: templateAccountID, barrierRoot: barrierRoot,
		artifacts: sched.ExecutionRuntimeArtifacts{Architecture: "amd64", KernelDigest: kernelDigest, GuestExecutorDigest: initDigest, BaseImageDigest: baseDigest, KernelKey: kernelKey, BaseKey: baseKey, LayerKey: layerKey, FCVersion: os.Getenv("FAAS_TEST_FC_VERSION")},
		identity:  sched.RuntimeSnapshotIdentity{FCVersion: os.Getenv("FAAS_TEST_FC_VERSION"), Runtime: api.ExecutionRuntimeNode24, Architecture: "amd64", KernelDigest: kernelDigest, GuestExecutorDigest: initDigest, BaseImageDigest: baseDigest, MemoryMB: 256, EphemeralDiskMB: 64, FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion}}
}

func hashRunFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // Protected operator-pinned acceptance fixture, never a customer-supplied path.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func stageRunFile(t *testing.T, source, target string) string {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("fixture must be a regular file: %s: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, target); err != nil {
		in, openErr := os.Open(source) //nolint:forbidigo // Regular fixture validated above; its parent is the root-owned acceptance staging directory.
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer in.Close()
		out, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("stage fixture: %v, %v", copyErr, closeErr)
		}
	}
	return hashRunFile(t, target)
}

func stageRunLayer(t *testing.T, path, skeleton string) {
	t.Helper()
	marker := filepath.Join(skeleton, "upper", "etc", "faas", "execution.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	// The operator runner keeps host evidence private with umask 077. Guest
	// overlay directories must still be traversable by the UID 1000 runtime.
	for _, dir := range []string{skeleton, filepath.Join(skeleton, "upper"), filepath.Join(skeleton, "upper", "etc"), filepath.Dir(marker)} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{"kind":"execution","version":1,"profile":"standard"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"truncate", "-s", "64M", path}, {"mkfs.ext4", "-q", "-O", "^has_journal", "-d", skeleton, "-L", "run-layer", "-F", path}} {
		if out, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", command[0], err, out)
		}
	}
	metadata, err := exec.Command("debugfs", "-R", "stat /upper", path).CombinedOutput()
	if err != nil || !strings.Contains(string(metadata), "Mode:  0755") {
		t.Fatalf("guest overlay root must be traversable by its runtime identity: %v: %s", err, metadata)
	}
}

func runRequest(source string) api.CreateExecutionRequest {
	return api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: source, Input: []byte(`{"value":41}`),
		Limits: &api.ExecutionLimitRequest{TimeoutMS: 30000, MemoryMB: 256, CPUMillicores: 1000, EphemeralDiskMB: 64, MaxOutputBytes: 65536}}
}

func (f *runFixture) submit(t *testing.T, key string, request api.CreateExecutionRequest) api.ExecutionResponse {
	t.Helper()
	row, err := f.client.CreateExecution(api.ContextWithIdempotencyKey(t.Context(), key), request)
	if err != nil || row.ID == "" {
		t.Fatalf("admit %s: %+v, %v", key, row, err)
	}
	f.runs[row.ID] = true
	return row
}

func (f *runFixture) wait(t *testing.T, id string, want api.ExecutionStatus) api.ExecutionResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if _, err := uuid.Parse(id); err != nil {
		t.Fatal(err)
	}
	var console *os.File
	defer func() {
		if console != nil {
			_ = console.Close()
		}
	}()
	for ctx.Err() == nil {
		if console == nil {
			// Keep the owned guest console open across vmmd's unlink on teardown.
			console, _ = os.Open(filepath.Join("/var/log/faas", "vm-"+id+".console")) //nolint:forbidigo // Root-owned acceptance console for the validated UUID of this test's Run.
		}
		row, err := f.client.GetExecution(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == want || (want == "terminal" && row.Status.Terminal()) {
			if row.Status.Terminal() {
				f.waitRetired(t, id)
			}
			return row
		}
		if row.Status.Terminal() {
			if console != nil {
				if info, err := console.Stat(); err == nil {
					_, _ = console.Seek(max(0, info.Size()-65536), io.SeekStart)
					data, _ := io.ReadAll(io.LimitReader(console, 65536))
					t.Logf("owned guest console tail:\n%s", data)
				}
			}
			t.Fatalf("Run %s reached %s before %s: failure=%+v stdout=%q stderr=%q", id, row.Status, want, row.Failure, row.Stdout, row.Stderr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Run %s did not reach %s: %v", id, want, ctx.Err())
	return api.ExecutionResponse{}
}

func (f *runFixture) waitRetired(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := wire.DialContext(ctx, f.h.VMMDSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := vmmdpb.NewVmmdClient(conn)
	for ctx.Err() == nil {
		stats, err := client.Stats(ctx, &vmmdpb.StatsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		live := false
		for _, instance := range stats.GetInstances() {
			live = live || instance.GetInstance() == id
		}
		if !live {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("terminal Run %s still owns a live VM: %v", id, ctx.Err())
}

func (f *runFixture) assertSettled(t *testing.T) {
	t.Helper()
	for id := range f.runs {
		row := f.wait(t, id, "terminal")
		var payloads, charges int
		if err := f.h.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM execution_payloads WHERE execution_id=$1::uuid), (SELECT count(*) FROM execution_usage_ledger WHERE execution_id=$1::uuid)`, id).Scan(&payloads, &charges); err != nil {
			t.Fatal(err)
		}
		if payloads != 0 || charges != 1 {
			t.Fatalf("Run %s (%s): payloads=%d usage_rows=%d", id, row.Status, payloads, charges)
		}
	}
	usage, err := state.NewPgStore(f.h.Pool).ExecutionUsageByAccount(t.Context(), f.accountID, time.Now().UTC())
	if err != nil || usage.Runs != int64(len(f.runs)) {
		t.Fatalf("usage reconciliation: %+v, %v", usage, err)
	}
	// Run before Harness.stop can destroy any surviving VM on our behalf.
	out, err := exec.CommandContext(t.Context(), "bash", "../../../deploy/scripts/leakcheck.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("pre-teardown leakcheck: %v: %s", err, out)
	}
	t.Logf("pre-teardown %s", out)
}

func (f *runFixture) guestMarkerCount(t *testing.T, id string) int {
	t.Helper()
	events, err := state.NewPgStore(f.h.Pool).ListExecutionEvents(t.Context(), f.accountID, id, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	for _, event := range events {
		if event.Type != state.ExecutionEventStdout {
			continue
		}
		var payload struct {
			Chunk string `json:"chunk"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		stdout.WriteString(payload.Chunk)
	}
	return strings.Count(stdout.String(), "running-marker")
}

func (f *runFixture) waitGuestStarted(t *testing.T, id string) {
	t.Helper()
	f.wait(t, id, api.ExecutionStatusRunning)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if count := f.guestMarkerCount(t, id); count == 1 {
			return
		} else if count > 1 {
			t.Fatal("guest entrypoint ran more than once")
		}
		row, err := f.client.GetExecution(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status.Terminal() {
			t.Fatalf("Run ended before the guest produced its execution witness: %+v", row)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("guest produced no execution witness")
}
