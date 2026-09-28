package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
	"gopkg.in/yaml.v3"
)

// Scenario files keep application assertions in the customer's own test
// command. Gregale owns the expiring environment and lifecycle evidence.
type testManifest struct {
	Version   int                     `yaml:"version"`
	Scenarios map[string]testScenario `yaml:"scenarios"`
}

type testScenario struct {
	Project  string       `yaml:"project"`
	Source   string       `yaml:"source"`
	Trigger  []string     `yaml:"trigger"`
	Command  []string     `yaml:"command"`
	Setup    [][]string   `yaml:"setup"`
	Cleanup  [][]string   `yaml:"cleanup"`
	Postgres bool         `yaml:"postgres"`
	Buckets  []testBucket `yaml:"buckets"`
	WaitFor  testWaitFor  `yaml:"wait_for"`
	Timeout  string       `yaml:"timeout"`
}

type testWaitFor struct {
	QueueIdle bool               `yaml:"queue_idle"`
	Objects   []testObjectOutput `yaml:"objects"`
}

type testObjectOutput struct {
	Bucket        string `yaml:"bucket"`
	Prefix        string `yaml:"prefix"`
	MinCount      int    `yaml:"min_count"`
	MinTotalBytes int64  `yaml:"min_total_bytes"`
}

type testOutputEvidence struct {
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix"`
	Count      int    `json:"count"`
	TotalBytes int64  `json:"total_bytes"`
}

type testBucket struct {
	Name       string `yaml:"name"`
	Prefix     string `yaml:"prefix"`
	Region     string `yaml:"region"`
	Permission string `yaml:"permission"`
}

var testBucketPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)

type testWakeEvidence struct {
	Header   string `json:"header,omitempty"`
	WakeID   string `json:"wake_id,omitempty"`
	Method   string `json:"method,omitempty"`
	Status   int    `json:"status,omitempty"`
	Requests int    `json:"requests"`
}

type testRunReceipt struct {
	Scenario     string               `json:"scenario"`
	Profile      string               `json:"profile"`
	Engine       string               `json:"engine"`
	RunID        string               `json:"run_id"`
	AppSlug      string               `json:"app_slug,omitempty"`
	DeploymentID string               `json:"deployment_id,omitempty"`
	Status       string               `json:"status"`
	Error        string               `json:"error,omitempty"`
	CleanupError string               `json:"cleanup_error,omitempty"`
	Buckets      []string             `json:"buckets,omitempty"`
	Evidence     testWakeEvidence     `json:"evidence"`
	Outputs      []testOutputEvidence `json:"outputs,omitempty"`
	QueueIdle    bool                 `json:"queue_idle,omitempty"`
}

func cmdTest(args []string) int {
	fs := newFlagSet("test", flag.ContinueOnError)
	scenarioName := fs.String("scenario", "", "scenario name from the manifest")
	profile := fs.String("profile", "all", "warm, cold, restored, or all")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "scenario manifest path")
	reportPath := fs.String("report", "", "write a JSON report to this path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale test --scenario NAME [--profile warm|cold|restored|all] [--manifest PATH] [--report PATH]", "test")
		return 1
	}
	if *scenarioName == "" {
		return printErr("Scenario required", errors.New("pass --scenario NAME"))
	}
	profiles, err := selectedTestProfiles(*profile)
	if err != nil {
		return printErr("Invalid profile", err)
	}
	scenarios, sourceDir, err := readTestManifest(*manifestPath)
	if err != nil {
		return printErr("Invalid scenario manifest", err)
	}
	scenario, ok := scenarios[*scenarioName]
	if !ok {
		return printErr("Unknown scenario", fmt.Errorf("%q is not declared in %s", *scenarioName, *manifestPath))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	results := make([]testRunReceipt, 0, len(profiles))
	failed := false
	for _, selected := range profiles {
		if ctx.Err() != nil {
			failed = true
			break
		}
		receipt := runTestProfile(ctx, client, *scenarioName, scenario, sourceDir, selected)
		results = append(results, receipt)
		if receipt.Status != "passed" {
			failed = true
		}
		if !jsonOutput {
			fmt.Fprintf(osStdout, "%s: %s (%s, app %s)\n", receipt.Scenario, receipt.Status, receipt.Profile, receipt.AppSlug)
			if receipt.Error != "" {
				fmt.Fprintln(osStderr, receipt.Error)
			}
			if receipt.CleanupError != "" {
				fmt.Fprintln(osStderr, receipt.CleanupError)
			}
		}
	}
	if *reportPath != "" {
		if err := writeTestReport(*reportPath, results); err != nil {
			return printErr("Could not save test report", err)
		}
	}
	if jsonOutput {
		if err := writeJSON(results); err != nil {
			return printErr("Could not print test report", err)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func selectedTestProfiles(profile string) ([]string, error) {
	switch profile {
	case "all":
		return []string{"warm", "cold", "restored"}, nil
	case "warm", "cold", "restored":
		return []string{profile}, nil
	default:
		return nil, fmt.Errorf("profile %q is invalid; use warm, cold, restored, or all", profile)
	}
}

func readTestManifest(path string) (map[string]testScenario, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 1024*1024 {
		return nil, "", errors.New("scenario manifest exceeds 1 MiB")
	}
	var manifest testManifest
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, "", errors.New("scenario manifest must contain exactly one YAML document")
	}
	if manifest.Version != 1 {
		return nil, "", errors.New("scenario manifest version must be 1")
	}
	for name, scenario := range manifest.Scenarios {
		if len(name) < 3 || len(name) > 80 || scenario.Project != sanitizeSlug(scenario.Project) || len(scenario.Project) < 3 || len(scenario.Project) > 40 {
			return nil, "", fmt.Errorf("scenario %q needs a valid project slug", name)
		}
		if len(scenario.Command) == 0 || scenario.Command[0] == "" {
			return nil, "", fmt.Errorf("scenario %q needs a command", name)
		}
		if len(scenario.Trigger) > 0 && scenario.Trigger[0] == "" {
			return nil, "", fmt.Errorf("scenario %q has an empty trigger command", name)
		}
		if (scenario.WaitFor.QueueIdle || len(scenario.WaitFor.Objects) > 0) && len(scenario.Trigger) == 0 {
			return nil, "", fmt.Errorf("scenario %q needs trigger when wait_for is set", name)
		}
		for _, step := range append(append([][]string{}, scenario.Setup...), scenario.Cleanup...) {
			if len(step) == 0 || step[0] == "" {
				return nil, "", fmt.Errorf("scenario %q has an empty setup or cleanup command", name)
			}
		}
		if scenario.Timeout != "" {
			d, parseErr := time.ParseDuration(scenario.Timeout)
			if parseErr != nil || d <= 0 || d > time.Hour {
				return nil, "", fmt.Errorf("scenario %q timeout must be a duration between 1 second and 1 hour", name)
			}
		}
		seenBuckets := map[string]bool{}
		for _, bucket := range scenario.Buckets {
			if bucket.Name != sanitizeSlug(bucket.Name) || len(bucket.Name) < 3 || len(bucket.Name) > 30 || seenBuckets[bucket.Name] {
				return nil, "", fmt.Errorf("scenario %q has an invalid or duplicate bucket name %q", name, bucket.Name)
			}
			seenBuckets[bucket.Name] = true
			if bucket.Permission != "" && bucket.Permission != "read" && bucket.Permission != "write" && bucket.Permission != "read_write" {
				return nil, "", fmt.Errorf("scenario %q bucket %q has invalid permission", name, bucket.Name)
			}
			if bucket.Prefix != "" && !testBucketPrefixPattern.MatchString(bucket.Prefix) {
				return nil, "", fmt.Errorf("scenario %q bucket %q has invalid secret prefix", name, bucket.Name)
			}
		}
		for _, output := range scenario.WaitFor.Objects {
			if !seenBuckets[output.Bucket] || output.MinCount < 1 || output.MinTotalBytes < 0 {
				return nil, "", fmt.Errorf("scenario %q has an invalid object wait condition for bucket %q", name, output.Bucket)
			}
		}
	}
	return manifest.Scenarios, filepath.Dir(absolute), nil
}

func runTestProfile(parent context.Context, client *Client, name string, scenario testScenario, manifestDir, profile string) (receipt testRunReceipt) {
	receipt = testRunReceipt{Scenario: name, Profile: profile, Engine: "real-vm", Status: "failed"}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		receipt.Error = fmt.Sprintf("create test run identity: %v", err)
		return
	}
	receipt.RunID = hex.EncodeToString(random)
	timeout := 15 * time.Minute
	if scenario.Timeout != "" {
		timeout, _ = time.ParseDuration(scenario.Timeout)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	source := scenario.Source
	if source == "" {
		source = "."
	}
	sourceDir, err := resolveDeploySourceDir(manifestDir, source)
	if err != nil {
		receipt.Error = fmt.Sprintf("source directory: %v", err)
		return
	}
	config, err := resolveDevSourceConfig(sourceDir)
	if err != nil {
		receipt.Error = fmt.Sprintf("detect app shape: %v", err)
		return
	}
	session, err := client.UpsertDevSession(ctx, scenario.Project, config.sessionRequest(receipt.RunID, scenario.Postgres, ""))
	if err != nil {
		receipt.Error = fmt.Sprintf("create isolated test environment: %v", err)
		return
	}
	receipt.AppSlug = session.App.Slug
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := client.DestroyDevSession(cleanupCtx, scenario.Project, receipt.RunID); err != nil {
			receipt.addCleanupError(fmt.Sprintf("destroy test environment %s: %v", receipt.AppSlug, err))
		}
	}()
	bucketEnv := make([]string, 0, 2*len(scenario.Buckets))
	bucketByName := make(map[string]string, len(scenario.Buckets))
	for _, spec := range scenario.Buckets {
		bucketName := spec.Name + "-" + receipt.RunID[:8]
		bucket, err := client.CreateObjectBucket(ctx, session.App.Slug, api.CreateObjectBucketRequest{
			Name: bucketName, Region: spec.Region,
		})
		if err != nil {
			receipt.Error = fmt.Sprintf("create test bucket %s: %v", spec.Name, err)
			return
		}
		receipt.Buckets = append(receipt.Buckets, bucket.Name)
		bucketByName[spec.Name] = bucket.Name
		permission := spec.Permission
		if permission == "" {
			permission = "read_write"
		}
		binding, err := client.CreateObjectStorageComputeBinding(ctx, session.App.Slug, bucket.Name, api.CreateObjectStorageComputeBindingRequest{
			Permission: permission, Prefix: spec.Prefix,
		})
		if err != nil {
			receipt.Error = fmt.Sprintf("bind test bucket %s: %v", spec.Name, err)
			return
		}
		bucketEnv = append(bucketEnv, "GREGALE_TEST_BUCKET_"+strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))+"="+bucket.Name)
		bucketEnv = append(bucketEnv, "GREGALE_TEST_BUCKET_PREFIX_"+strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))+"="+binding.Prefix)
	}
	var deploymentID string
	deployArgs := config.deployArgs(session.App.Slug, sourceDir)
	// The nested deploy command has its own progress output. Keep --json's
	// stdout as one machine-readable test receipt.
	previousStdout := osStdout
	if jsonOutput {
		osStdout = osStderr
	}
	deployResult := cmdDeployTarballToExisting(ctx, deployArgs, true, deployExecution{
		onQueued: func(dep api.DeploymentResponse) { deploymentID = dep.ID },
	})
	osStdout = previousStdout
	receipt.DeploymentID = deploymentID
	if deployResult != 0 || deploymentID == "" {
		receipt.Error = fmt.Sprintf("source deployment failed (exit %d)", deployResult)
		return
	}
	target, err := url.Parse(canonicalAppURL(session.App))
	if err != nil || target.Scheme == "" || target.Host == "" {
		receipt.Error = "test environment returned an invalid app URL"
		return
	}
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	env := append(os.Environ(),
		"GREGALE_TEST_URL="+proxy.URL,
		"GREGALE_TEST_APP_SLUG="+session.App.Slug,
		"GREGALE_TEST_RUN_ID="+receipt.RunID,
		"GREGALE_TEST_PROFILE="+profile,
		"GREGALE_TEST_ENGINE=real-vm",
	)
	env = append(env, bucketEnv...)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		for _, command := range scenario.Cleanup {
			if err := runTestCommand(cleanupCtx, sourceDir, env, command); err != nil {
				receipt.addCleanupError(fmt.Sprintf("fixture cleanup: %v", err))
			}
		}
	}()
	for _, command := range scenario.Setup {
		if err := runTestCommand(ctx, sourceDir, env, command); err != nil {
			receipt.Error = fmt.Sprintf("fixture setup: %v", err)
			return
		}
	}
	if err := prepareTestProfile(ctx, client, session.App.Slug, profile); err != nil {
		receipt.Error = fmt.Sprintf("prepare %s profile: %v", profile, err)
		return
	}
	recorder.reset()
	if len(scenario.Trigger) > 0 {
		if err := runTestCommand(ctx, sourceDir, env, scenario.Trigger); err != nil {
			receipt.Error = fmt.Sprintf("application trigger command: %v", err)
			receipt.Evidence = recorder.snapshot()
			return
		}
		outputs, err := waitForTestOutputs(ctx, client, session.App.Slug, scenario.WaitFor, bucketByName, receipt.RunID)
		receipt.Outputs = outputs
		if err != nil {
			receipt.Error = fmt.Sprintf("wait for application output: %v", err)
			receipt.Evidence = recorder.snapshot()
			return
		}
		receipt.QueueIdle = scenario.WaitFor.QueueIdle
	}
	if err := runTestCommand(ctx, sourceDir, env, scenario.Command); err != nil {
		receipt.Error = fmt.Sprintf("application assertion command: %v", err)
	}
	receipt.Evidence = recorder.snapshot()
	if err := verifyTestProfile(ctx, client, session.App.Slug, profile, &receipt.Evidence); err != nil {
		if receipt.Error != "" {
			receipt.Error += "; "
		}
		receipt.Error += fmt.Sprintf("lifecycle evidence: %v", err)
	}
	if receipt.Error == "" {
		receipt.Status = "passed"
	}
	return
}

func (r *testRunReceipt) addCleanupError(message string) {
	if r.CleanupError != "" {
		r.CleanupError += "; "
	}
	r.CleanupError += message
	r.Status = "failed"
}

func prepareTestProfile(ctx context.Context, client *Client, slug, profile string) error {
	switch profile {
	case "warm":
		if err := client.Park(ctx, slug); err != nil {
			return err
		}
		response, err := client.Wake(ctx, slug)
		if err != nil {
			return err
		}
		if response.WakeID == "" {
			return errors.New("wake response omitted wake_id")
		}
		_, err = waitForAppWake(ctx, client, slug, response.WakeID, 2*time.Minute, 250*time.Millisecond)
		return err
	case "cold":
		return client.ParkPreviewFresh(ctx, slug)
	case "restored":
		return client.Park(ctx, slug)
	default:
		return fmt.Errorf("unknown profile %q", profile)
	}
}

func verifyTestProfile(ctx context.Context, client *Client, slug, profile string, evidence *testWakeEvidence) error {
	if evidence.Requests == 0 {
		return errors.New("assertion command sent no requests through GREGALE_TEST_URL")
	}
	if profile == "warm" {
		if evidence.Header != wire.HotWakeValue || evidence.WakeID != "" {
			return fmt.Errorf("first request observed wake=%q wake_id=%q; expected hot", evidence.Header, evidence.WakeID)
		}
		return nil
	}
	if evidence.WakeID == "" {
		return fmt.Errorf("first request has no wake_id (wake=%q)", evidence.Header)
	}
	want := "restore"
	wantHeader := wire.RestoredWakeValue
	if profile == "cold" {
		want = "cold_boot"
		wantHeader = wire.ColdWakeValue
	}
	if evidence.Header != wantHeader {
		return fmt.Errorf("first request observed wake=%q; expected %q", evidence.Header, wantHeader)
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		timeline, err := client.ListWakeTimeline(ctx, slug, evidence.WakeID, "", 100)
		if err != nil {
			return err
		}
		for _, event := range timeline.Events {
			if event.Kind != "wake.boot_completed" {
				continue
			}
			method, _ := event.Data["method"].(string)
			if method == want {
				evidence.Method = method
				return nil
			}
			if method != "" {
				evidence.Method = method
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("expected %s boot; observed %q for wake %s", want, evidence.Method, evidence.WakeID)
		case <-ticker.C:
		}
	}
}

func runTestCommand(ctx context.Context, dir string, env, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = osStdout
	if jsonOutput {
		cmd.Stdout = osStderr
	}
	cmd.Stderr = osStderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}

type testOutputClient interface {
	QueueState(context.Context, string) (api.QueueStateResponse, error)
	ListBucketObjects(context.Context, string, string, string, string, int) (api.BucketObjectPage, error)
}

func waitForTestOutputs(ctx context.Context, client testOutputClient, slug string, conditions testWaitFor, buckets map[string]string, runID string) ([]testOutputEvidence, error) {
	if !conditions.QueueIdle && len(conditions.Objects) == 0 {
		return nil, nil
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		if conditions.QueueIdle {
			state, err := client.QueueState(ctx, slug)
			if err != nil {
				return nil, fmt.Errorf("read queue state: %w", err)
			}
			ready = state.Depth == 0 && state.InFlight == 0
		}
		observed := make([]testOutputEvidence, 0, len(conditions.Objects))
		for _, condition := range conditions.Objects {
			bucket := buckets[condition.Bucket]
			prefix := strings.ReplaceAll(condition.Prefix, "${GREGALE_TEST_RUN_ID}", runID)
			output := testOutputEvidence{Bucket: bucket, Prefix: prefix}
			cursor := ""
			for pages := 0; pages < 100; pages++ {
				page, err := client.ListBucketObjects(ctx, slug, bucket, prefix, cursor, 100)
				if err != nil {
					return observed, fmt.Errorf("list bucket %s: %w", bucket, err)
				}
				for _, object := range page.Items {
					output.Count++
					output.TotalBytes += object.SizeBytes
				}
				if page.NextCursor == "" {
					break
				}
				if pages == 99 {
					return observed, fmt.Errorf("bucket %s output listing exceeded 10000 objects", bucket)
				}
				cursor = page.NextCursor
			}
			observed = append(observed, output)
			if output.Count < condition.MinCount || output.TotalBytes < condition.MinTotalBytes {
				ready = false
			}
		}
		if ready {
			return observed, nil
		}
		select {
		case <-ctx.Done():
			return observed, fmt.Errorf("completion conditions not reached: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

type testProxyRecorder struct {
	mu       sync.Mutex
	evidence testWakeEvidence
}

type testProxyRequestKey struct{}

func (r *testProxyRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evidence = testWakeEvidence{}
}

func (r *testProxyRecorder) snapshot() testWakeEvidence {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.evidence
}

func newTestProxy(target *url.URL) (*httptest.Server, *testProxyRecorder) {
	recorder := &testProxyRecorder{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Host = target.Host
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		// Requests may finish out of order. The first request to reach the
		// proxy establishes the profile's wake evidence.
		if response.Request.Context().Value(testProxyRequestKey{}) == 1 {
			recorder.evidence.Header = response.Header.Get(wire.WakeHeader)
			recorder.evidence.WakeID = response.Header.Get("X-Faas-Wake-ID")
			recorder.evidence.Status = response.StatusCode
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		recorder.mu.Lock()
		recorder.evidence.Requests++
		sequence := recorder.evidence.Requests
		recorder.mu.Unlock()
		proxy.ServeHTTP(w, request.WithContext(context.WithValue(request.Context(), testProxyRequestKey{}, sequence)))
	}))
	return server, recorder
}

func writeTestReport(path string, receipts []testRunReceipt) error {
	body, err := json.MarshalIndent(receipts, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	return nil
}
