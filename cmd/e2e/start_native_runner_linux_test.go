//go:build linux

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// Verify the live runner's provisioning/teardown against the actual API wire
// contract. This local gate does not run builders or certify native execution.
func TestE2E_Start_NativeRunnerProvisionAndCleanup(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "start-native-unrelated", api.PlanPro)
	if f == nil {
		t.Fatal("native runner API contracts require PostgreSQL; use make e2e-start")
	}
	app, err := f.store.AppByID(t.Context(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 is required to verify the native Start runner")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	code := `import os, pathlib, runpy, tempfile
m = runpy.run_path('../../scripts/ci/start-native.py')
api = m['API'](os.environ['FAAS_API'], os.environ['FAAS_TOKEN'])
request = api.request
def diagnose_scan(method, path, *args, **kwargs):
    result = request(method, path, *args, **kwargs)
    if path == '/v1/projects/scan':
        print('portable scan targets:', [(row.get('name'), row.get('root_dir'), row.get('action')) for row in result[1].get('workloads', [])], flush=True)
    return result
api.request = diagnose_scan
target = {'api_origin': api.origin, 'account_id': os.environ['START_CONTRACT_ACCOUNT'], 'project_slug': 'start-contract', 'named_environment': 'staging'}
report = {'resources': {'operation_id': 'start-runner-contract', 'provisioning': 'not_started', 'apps': {}}}
with tempfile.TemporaryDirectory() as temporary:
    root = pathlib.Path(temporary)
    source = root / 'source'
    m['prepare_sources'](source, target['project_slug'], 'portable-api-contract')
    run = m['Run'](api, target, root / 'acceptance.json', report)
    try:
        run.provision(source)
        assert len(report['resources']['apps']) == 4
        assert report['resources']['environment_id']
    finally:
        run.cleanup()
    assert report['cleanup']['status'] == 'passed'
    assert api.get('/v1/apps/start-native-unrelated')[1]['id'] == os.environ['START_CONTRACT_APP']
`
	cmd := exec.CommandContext(ctx, python, "-c", code)
	cmd.Env = append(os.Environ(), "FAAS_API="+f.h.APIDURL, "FAAS_TOKEN="+f.key, "START_CONTRACT_ACCOUNT="+app.AccountID, "START_CONTRACT_APP="+f.app.ID)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("live runner API contracts: %v\n%s", err, output)
	}
}

// Drive the live runner's recovery methods with the actual CLI, API, scheduler,
// and gateways. Builder completion and guest execution remain simulated here;
// the native command has no such substitution and still requires native proof.
func TestE2E_Start_NativeRunnerFailureRecovery(t *testing.T) {
	bin := buildGregale(t)
	relay := filepath.Join(t.TempDir(), "start-relay")
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", relay, "../../scripts/ci/start-relay").CombinedOutput(); err != nil {
		t.Fatalf("build acceptance relay: %v\n%s", err, output)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 is required for the native Start runner contracts")
	}
	for _, name := range []string{"public-default", "private-default", "public-named", "private-named"} {
		for _, scenario := range []string{"failure-recovery", "configuration", "compatibility", "submission-lost-reply", "submission-process-killed"} {
			t.Run(name+"/"+scenario, func(t *testing.T) {
				recipient := filepath.Join(t.TempDir(), "host.age.pub")
				if err := writeTestRecipient(recipient); err != nil {
					t.Fatal(err)
				}
				identityPath := filepath.Join(filepath.Dir(recipient), "host.age")
				if err := os.Rename(recipient+".priv", identityPath); err != nil {
					t.Fatal(err)
				}
				identity, err := secretbox.LoadHostKey(identityPath)
				if err != nil {
					t.Fatal(err)
				}
				f := newStartAcceptanceFixture(t, bin, "start-native-"+name, strings.HasPrefix(name, "private"), "staging",
					"FAAS_HOST_AGE_RECIPIENT_PATH="+recipient, "FAAS_HOST_AGE_IDENTITY_PATH="+identityPath)
				if strings.HasSuffix(name, "default") {
					f.environment = ""
					linkPath := filepath.Join(f.source, ".gregale", "project.json")
					data, readErr := os.ReadFile(linkPath)
					if readErr != nil {
						t.Fatal(readErr)
					}
					var link map[string]any
					if json.Unmarshal(data, &link) != nil {
						t.Fatal("invalid fixture link")
					}
					link["environment"] = ""
					data, _ = json.Marshal(link)
					writeStartAcceptanceFile(t, linkPath, string(data))
				}
				marker := "portable-native-" + name
				prepare := `import pathlib, runpy, sys
m = runpy.run_path('../../scripts/ci/start-native.py')
m['write_fixture'](pathlib.Path(sys.argv[1]), sys.argv[2])`
				if output, prepareErr := exec.CommandContext(t.Context(), python, "-c", prepare, f.source, marker).CombinedOutput(); prepareErr != nil {
					t.Fatalf("prepare runner fixture: %v\n%s", prepareErr, output)
				}
				var markers sync.Map
				var faultMu sync.Mutex
				faults := make(map[string]bool)
				f.platform.vmmd.SetForwardHandler("", func(ctx context.Context, request e2etest.RequestCapture) (e2etest.FakeResponse, error) {
					instance, loadErr := f.platform.store.InstanceByID(ctx, request.Init.Instance)
					if loadErr != nil {
						return e2etest.FakeResponse{}, loadErr
					}
					value, found := markers.Load(instance.DeploymentID)
					if !found {
						return e2etest.FakeResponse{}, errors.New("fixture response lacks accepted source identity")
					}
					fixture := value.(nativeStartSource)
					currentMarker := fixture.Marker
					control := "/__start_acceptance/" + currentMarker
					faultMu.Lock()
					switch request.Init.RequestUri {
					case control + "/arm":
						faults[instance.ID] = true
					case control + "/recover":
						faults[instance.ID] = false
					}
					failed := faults[instance.ID]
					faultMu.Unlock()
					auth := false
					for _, header := range request.Init.Headers {
						auth = auth || strings.EqualFold(header.Name, "Authorization")
					}
					payload := map[string]any{"marker": currentMarker, "response_failed": failed, "authorization_seen": auth}
					if fixture.ConfigurationDigest != "" {
						// Stand in for the guest using the ciphertext actually sent by
						// schedd for this instance, not the test's expected plaintext.
						configured := false
						for _, boot := range f.platform.vmmd.ColdBootCalls() {
							if boot.Instance != instance.ID {
								continue
							}
							for _, sealed := range boot.App.GetSealedEnv() {
								if sealed.Key != "START_NATIVE_REQUIRED" {
									continue
								}
								envelope, openErr := secretbox.Open(identity, sealed.Ciphertext)
								if openErr != nil {
									return e2etest.FakeResponse{}, errors.New("fixture could not open its scoped runtime configuration")
								}
								configured = fmt.Sprintf("%x", sha256.Sum256([]byte(envelope[sealed.Key]))) == fixture.ConfigurationDigest
							}
						}
						payload["configuration_ok"], payload["source_checked"] = configured, fixture.SourceGuard
					}
					body, _ := json.Marshal(payload)
					status := http.StatusOK
					if failed && request.Init.RequestUri == "/" {
						status = http.StatusServiceUnavailable
					}
					return e2etest.FakeResponse{Status: status, Body: body, Headers: []*vmmdpb.Header{
						{Name: "Content-Type", Value: "application/json"}, {Name: "Cache-Control", Value: "no-store"},
					}}, nil
				})
				var baseline state.Deployment
				if !strings.HasPrefix(scenario, "submission-") {
					s := f.start(t)
					s.answer("Choose [1]: ", "1")
					s.answer("Deploy this app (y/N) [n]: ", "y")
					baseline = f.accepted(t)
					markers.Store(baseline.ID, nativeStartSource{Marker: marker})
					f.publish(t, baseline, marker)
					s.expect("Keep building in this project.")
					f.assertSession(t, baseline.ID, baseline.ID)
					s.answer("Choose [1]: ", "1")
					s.wait(0)
				}
				app, loadErr := f.platform.store.AppByID(t.Context(), f.platform.app.ID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				project, loadErr := f.platform.store.ProjectByID(t.Context(), app.ProjectID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				environment, loadErr := f.platform.store.ProjectEnvironmentBySlug(t.Context(), app.AccountID, project.ID, "staging")
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				receipt := filepath.Join(t.TempDir(), "acceptance.json")
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()
				code := `import json, os, pathlib, runpy
m = runpy.run_path('../../scripts/ci/start-native.py')
api = m['API'](os.environ['FAAS_API'], os.environ['FAAS_TOKEN'])
target = {'api_origin': api.origin, 'account_id': os.environ['START_CONTRACT_ACCOUNT'], 'project_slug': os.environ['START_CONTRACT_PROJECT'], 'named_environment': 'staging'}
report = {'scenarios': [{'case': os.environ['START_CONTRACT_CASE'], 'status': 'running'}], 'resources': {'apps': {os.environ['START_CONTRACT_SLUG']: os.environ['START_CONTRACT_APP']}, 'project_id': os.environ['START_CONTRACT_PROJECT_ID'], 'environment_id': os.environ['START_CONTRACT_ENV_ID']}}
run = m['Run'](api, target, pathlib.Path(os.environ['START_CONTRACT_RECEIPT']), report)
launch = m['Launch'](pathlib.Path(os.environ['START_CONTRACT_CLI']), pathlib.Path(os.environ['START_CONTRACT_SOURCE']), pathlib.Path(os.environ['XDG_STATE_HOME']), dict(os.environ), os.environ['START_CONTRACT_SLUG'], os.environ['START_CONTRACT_SCOPE'])
verify = run.verified
def diagnose_verification(terminal, *args):
    try:
        return verify(terminal, *args)
    except BaseException:
        output = terminal.output.replace(os.environ['FAAS_TOKEN'], '[credential]')
        secret_path = launch.source / m['CONFIGURATION_FILE']
        if secret_path.exists():
            value = secret_path.read_text().strip().partition('=')[2]
            if value:
                output = output.replace(value, '[fixture-secret]')
        print(output.replace(str(secret_path), '[secret-file]')[-8192:], flush=True)
        raise
run.verified = diagnose_verification
baseline = run.deployment(launch.slug, launch.scope, os.environ['START_CONTRACT_BASELINE']) if os.environ['START_CONTRACT_BASELINE'] else None
result = report['scenarios'][0]
if os.environ['START_CONTRACT_SCENARIO'].startswith('submission-'):
    mode = os.environ['START_CONTRACT_SCENARIO'].removeprefix('submission-')
    run.submission_recovery(launch, pathlib.Path(os.environ['START_CONTRACT_RELAY']), os.environ['START_CONTRACT_MARKER'], result, {mode: m['SUBMISSION_MODES'][mode]})
    submission = result['recovery']['lost_commit_reply']
    assert submission['status'] == 'passed' and submission['original_session_unchanged'] is True
    assert set(submission['modes']) == {mode + '-first', mode + '-update'}
    first, update = (submission['modes'][mode + '-' + stage] for stage in ('first', 'update'))
    for row in (first, update):
        assert row['status'] == 'passed' and row['source_and_cache_removed'] is row['configuration_unchanged'] is True
        assert row['recovery_requests']['writes'] == 0 and row['recovery_requests']['upload_reads'] > 0
        assert row['committed_id'] == row['verified']['deployment_id'] == row['saved_after_recovery']['verified_id']
        assert row['exit_status'] == (3 if mode == 'lost-reply' else -9)
    assert first['pending']['accepted_id'] == first['pending']['verified_id'] == ''
    assert update['pending']['accepted_id'] == update['pending']['verified_id'] == first['committed_id']
    assert first['source_sha256'] != update['source_sha256']
elif os.environ['START_CONTRACT_SCENARIO'] == 'compatibility':
    run.compatibility(launch, pathlib.Path(os.environ['START_CONTRACT_RELAY']), os.environ['START_CONTRACT_MARKER'], result)
    compatibility = result['recovery']['compatibility']
    assert compatibility['status'] == 'passed'
    assert compatibility['original_session_unchanged'] is compatibility['session_origin_unchanged'] is True
    modes = m['COMPATIBILITY_MODES'] + (('response-outage',) if os.environ['START_CONTRACT_CASE'].startswith('private') else ())
    assert set(compatibility['modes']) == set(modes)
    assert all(row['status'] == 'passed' for row in compatibility['modes'].values())
elif os.environ['START_CONTRACT_SCENARIO'] == 'configuration':
    run.missing_configuration(launch, baseline, os.environ['START_CONTRACT_MARKER'], result)
    configuration = result['recovery']['missing_configuration']
    assert configuration['status'] == 'passed'
    assert all(configuration[key] is True for key in ('blocked_before_submission', 'reviewed_before_mutation', 'other_scope_unchanged', 'resumed_without_secret_file', 'private_data_excluded', 'guest_configuration_verified', 'build_source_guard_verified'))
else:
    repaired, marker = run.failed_build(launch, baseline, os.environ['START_CONTRACT_MARKER'], result)
    run.failed_response(launch, repaired, marker, result)
    assert result['recovery']['failed_build']['status'] == result['recovery']['response_retry']['status'] == 'passed'
assert os.environ['FAAS_TOKEN'] not in run.path.read_text()
`
				cmd := exec.CommandContext(ctx, python, "-c", code)
				cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
				cmd.WaitDelay = 5 * time.Second
				cmd.Env = append(f.cliEnv, "START_CONTRACT_ACCOUNT="+app.AccountID, "START_CONTRACT_PROJECT="+project.Slug,
					"START_CONTRACT_PROJECT_ID="+project.ID, "START_CONTRACT_ENV_ID="+environment.ID, "START_CONTRACT_SLUG="+app.Slug,
					"START_CONTRACT_APP="+app.ID, "START_CONTRACT_CLI="+bin, "START_CONTRACT_SOURCE="+f.source, "START_CONTRACT_RECEIPT="+receipt,
					"START_CONTRACT_BASELINE="+baseline.ID, "START_CONTRACT_SCOPE="+f.environment, "START_CONTRACT_MARKER="+marker, "START_CONTRACT_CASE="+name,
					"START_CONTRACT_SCENARIO="+scenario, "START_CONTRACT_RELAY="+relay)
				var output bytes.Buffer
				cmd.Stdout, cmd.Stderr = &output, &output
				if startErr := cmd.Start(); startErr != nil {
					t.Fatal(startErr)
				}
				done := make(chan struct{})
				var runErr error
				go func() { runErr = cmd.Wait(); close(done) }()
				t.Cleanup(func() {
					cancel()
					<-done
				})
				handled := map[string]bool{baseline.ID: true}
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						if runErr != nil {
							t.Fatalf("live recovery runner: %v\n%s", runErr, strings.ReplaceAll(output.String(), f.platform.key, "[REDACTED]"))
						}
						rows, listErr := f.platform.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
						count := 3
						if scenario == "configuration" || scenario == "compatibility" || strings.HasPrefix(scenario, "submission-") {
							count = 2
						}
						if listErr != nil || len(rows) != count {
							t.Fatalf("runner recovery created duplicate deployments: count=%d err=%v", len(rows), listErr)
						}
						return
					case <-ticker.C:
						rows, listErr := f.platform.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
						if listErr != nil {
							t.Fatal(listErr)
						}
						for _, deployment := range rows {
							if handled[deployment.ID] {
								continue
							}
							build, buildErr := f.platform.store.BuildByDeployment(t.Context(), deployment.ID)
							if errors.Is(buildErr, state.ErrNotFound) {
								continue
							}
							if buildErr != nil {
								t.Fatal(buildErr)
							}
							if build.Status != state.BuildQueued {
								continue
							}
							secretPath := filepath.Join(f.source, "start-native-keys.txt")
							forbidden := []string{secretPath}
							if secretFile, readErr := os.ReadFile(secretPath); readErr == nil {
								key, value, ok := strings.Cut(strings.TrimSpace(string(secretFile)), "=")
								if !ok || key != "START_NATIVE_REQUIRED" || value == "" {
									t.Fatal("invalid private fixture configuration")
								}
								forbidden = append(forbidden, value)
							}
							fixture := readNativeStartSource(t, deployment, forbidden...)
							if fixture.BuildFails {
								claim, claimErr := f.platform.store.ClaimQueuedBuild(t.Context(), build.ID)
								if claimErr != nil {
									t.Fatal(claimErr)
								}
								if failErr := f.platform.store.FailBuild(t.Context(), claim, state.FailureUserError, "start-native intentional build failure"); failErr != nil {
									t.Fatal(failErr)
								}
							} else {
								data, _ := os.ReadFile(receipt)
								var checkpoint struct {
									Milestone string `json:"milestone"`
								}
								milestone := "verify-fixed-build"
								if scenario == "compatibility" {
									milestone = "verify-compatibility-baseline"
								}
								if strings.HasPrefix(scenario, "submission-") {
									milestone = "verify-submission-recovered"
								}
								if fixture.ConfigurationDigest != "" {
									milestone = "verify-configured-deployment"
									if !fixture.SourceGuard || len(forbidden) != 2 {
										t.Fatal("configured source lacks its build guard or private fixture input")
									}
								}
								if json.Unmarshal(data, &checkpoint) != nil || checkpoint.Milestone != milestone {
									continue // Keep the build pending until the runner has checked its receipt.
								}
								markers.Store(deployment.ID, fixture)
								f.publish(t, deployment, fixture.Marker)
							}
							handled[deployment.ID] = true
						}
					}
				}
			})
		}
	}
}

type nativeStartSource struct {
	Marker, ConfigurationDigest string
	BuildFails, SourceGuard     bool
}

func readNativeStartSource(t *testing.T, deployment state.Deployment, forbidden ...string) nativeStartSource {
	t.Helper()
	var result nativeStartSource
	prefix, err := tarball.RootPrefix(deployment.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(deployment.SourcePath) //nolint:forbidigo // Isolated harness spool path, never customer input.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	root := strings.Trim(strings.TrimSuffix(prefix, "/")+"/"+deployment.SourceRoot, "/")
	if root != "" {
		root += "/"
	}
	pattern := regexp.MustCompile(`const marker = ("[^"]+");`)
	digestPattern := regexp.MustCompile(`\.digest\("hex"\) === "([0-9a-f]{64})"`)
	guardScript, guardFile := false, false
	for {
		header, readErr := archive.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		name := strings.TrimPrefix(header.Name, "./")
		if filepath.Base(name) == "start-native-keys.txt" || filepath.Base(name) == "start-native-source-checked.json" || header.Size > 64<<10 {
			t.Fatal("private configuration or generated source proof entered the fixture upload")
		}
		body, readErr := io.ReadAll(io.LimitReader(archive, 64<<10))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, value := range forbidden {
			if strings.Contains(string(body), value) || strings.Contains(name, value) {
				t.Fatal("private fixture value or file path reached the accepted source archive")
			}
		}
		if name == root+"package.json" {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(body, &pkg) != nil {
				t.Fatal("invalid accepted fixture package")
			}
			result.BuildFails = strings.Contains(pkg.Scripts["build"], "process.exit(42)")
			guardScript = pkg.Scripts["build"] == "node check-source.js"
		} else if name == root+"check-source.js" {
			guardFile = true
		} else if name == root+"handler.js" {
			if match := pattern.FindSubmatch(body); len(match) == 2 {
				if json.Unmarshal(match[1], &result.Marker) != nil {
					t.Fatal("invalid fixture nonce")
				}
			}
			if match := digestPattern.FindSubmatch(body); len(match) == 2 {
				result.ConfigurationDigest = string(match[1])
			}
		}
	}
	if result.Marker == "" {
		t.Fatalf("accepted source lacks its fixture nonce: root=%q", root)
	}
	result.SourceGuard = guardScript && guardFile
	return result
}
