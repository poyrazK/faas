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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
	"gopkg.in/yaml.v3"
)

// Scenario files may declare HTTP checks or run application-owned commands.
// Gregale owns the expiring environment and lifecycle evidence.
type testManifest struct {
	Version   int                     `yaml:"version"`
	Scenarios map[string]testScenario `yaml:"scenarios"`
	Suites    map[string]testSuite    `yaml:"suites,omitempty"`
}

type testScenario struct {
	Project          string                 `yaml:"project"`
	Source           string                 `yaml:"source"`
	Secrets          map[string]string      `yaml:"secrets"`
	Consumers        []testConsumer         `yaml:"consumers"`
	ConsumerAuthMode string                 `yaml:"consumer_auth_mode"`
	Simulation       []string               `yaml:"simulation"`
	Services         map[string]testService `yaml:"services"`
	Trigger          []string               `yaml:"trigger"`
	Command          []string               `yaml:"command"`
	Requests         []testHTTPRequest      `yaml:"requests"`
	Checks           []testHTTPRequest      `yaml:"checks"`
	Setup            [][]string             `yaml:"setup"`
	Cleanup          [][]string             `yaml:"cleanup"`
	Postgres         bool                   `yaml:"postgres"`
	Buckets          []testBucket           `yaml:"buckets"`
	WaitFor          testWaitFor            `yaml:"wait_for"`
	Timeout          string                 `yaml:"timeout"`
	Load             *testLoadSpec          `yaml:"load"`
	Local            *testLocalAppSpec      `yaml:"local"`
}

type testService struct {
	Source      string            `yaml:"source"`
	Fixture     string            `yaml:"fixture"`
	FailFirst   int               `yaml:"fail_first"`
	Postgres    bool              `yaml:"postgres"`
	Secrets     map[string]string `yaml:"secrets"`
	AsyncRoutes []testAsyncRoute  `yaml:"async_routes"`
}

type testAsyncRoute struct {
	Path        string           `yaml:"path"`
	Methods     []string         `yaml:"methods"`
	RetryPolicy *testRetryPolicy `yaml:"retry_policy"`
}

type testRetryPolicy struct {
	MaxAttempts   int     `yaml:"max_attempts"`
	BaseSeconds   float64 `yaml:"base_seconds"`
	MaxSeconds    float64 `yaml:"max_seconds"`
	JitterSeconds float64 `yaml:"jitter_seconds"`
}

type testConsumer struct {
	Name   string   `yaml:"name"`
	Scopes []string `yaml:"scopes"`
}

type testWaitFor struct {
	QueueIdle   bool                   `yaml:"queue_idle"`
	Invocations []testInvocationOutput `yaml:"invocations"`
	Objects     []testObjectOutput     `yaml:"objects"`
	Deliveries  []testDeliveryOutput   `yaml:"deliveries"`
}

type testInvocationOutput struct {
	Service     string `yaml:"service"`
	TriggerKey  string `yaml:"trigger_key"`
	MinAttempts int    `yaml:"min_attempts"`
}

type testInvocationEvidence struct {
	Service  string `json:"service"`
	ID       string `json:"id"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
}

type testDeliveryOutput struct {
	Service     string `yaml:"service"`
	MinAttempts int    `yaml:"min_attempts"`
	LastStatus  int    `yaml:"last_status"`
}

type testDeliveryEvidence struct {
	Service  string `json:"service"`
	Attempts int    `json:"attempts"`
	Statuses []int  `json:"statuses"`
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
	Service    string `yaml:"service"`
	Prefix     string `yaml:"prefix"`
	Region     string `yaml:"region"`
	Permission string `yaml:"permission"`
}

type testBucketRef struct {
	Name    string
	AppSlug string
}

var testBucketPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)
var testTriggerKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type testWakeEvidence struct {
	Header   string `json:"header,omitempty"`
	WakeID   string `json:"wake_id,omitempty"`
	Method   string `json:"method,omitempty"`
	Status   int    `json:"status,omitempty"`
	Requests int    `json:"requests"`
}

type testServiceWakeEvidence struct {
	WakeID string `json:"wake_id"`
	Method string `json:"method"`
}

type testServiceHotEvidence struct {
	RequestID  string `json:"request_id"`
	InstanceID string `json:"instance_id"`
	Status     int    `json:"status"`
}

type testRunReceipt struct {
	Suite        string                             `json:"suite,omitempty"`
	Scenario     string                             `json:"scenario"`
	Profile      string                             `json:"profile"`
	Engine       string                             `json:"engine"`
	Case         string                             `json:"case,omitempty"`
	Attempt      int                                `json:"attempt"`
	StartedAt    time.Time                          `json:"started_at"`
	FinishedAt   time.Time                          `json:"finished_at"`
	DurationMS   int64                              `json:"duration_ms"`
	Phases       []testPhaseEvidence                `json:"phases,omitempty"`
	RunID        string                             `json:"run_id,omitempty"`
	AppSlug      string                             `json:"app_slug,omitempty"`
	DeploymentID string                             `json:"deployment_id,omitempty"`
	Services     map[string]string                  `json:"services,omitempty"`
	AsyncRoutes  map[string][]string                `json:"async_routes,omitempty"`
	ServiceWake  map[string]testServiceWakeEvidence `json:"service_wake,omitempty"`
	ServiceHot   map[string]testServiceHotEvidence  `json:"service_hot,omitempty"`
	Status       string                             `json:"status"`
	SkipReason   string                             `json:"skip_reason,omitempty"`
	Error        string                             `json:"error,omitempty"`
	CleanupError string                             `json:"cleanup_error,omitempty"`
	Buckets      []string                           `json:"buckets,omitempty"`
	Evidence     *testWakeEvidence                  `json:"evidence,omitempty"`
	Outputs      []testOutputEvidence               `json:"outputs,omitempty"`
	Invocations  []testInvocationEvidence           `json:"invocations,omitempty"`
	Deliveries   []testDeliveryEvidence             `json:"deliveries,omitempty"`
	QueueIdle    bool                               `json:"queue_idle,omitempty"`
	Diagnostics  map[string]testWorkloadDiagnostics `json:"diagnostics,omitempty"`
	Requests     []testHTTPRequestEvidence          `json:"requests,omitempty"`
	Load         *testLoadEvidence                  `json:"load,omitempty"`
	LocalApp     *testLocalAppEvidence              `json:"local_app,omitempty"`
	Baseline     *testBaselineEvidence              `json:"baseline,omitempty"`
}

func cmdTest(args []string) int {
	if len(args) > 0 && args[0] == "init" {
		return cmdTestInit(args[1:])
	}
	if len(args) > 0 && args[0] == "import" {
		return cmdTestImport(args[1:])
	}
	if len(args) > 0 && args[0] == "compare" {
		return cmdTestCompare(args[1:])
	}
	if len(args) > 0 && args[0] == "ci" {
		return cmdTestCI(args[1:])
	}
	fs := newFlagSet("test", flag.ContinueOnError)
	scenarioName := fs.String("scenario", "", "scenario name from the manifest")
	suiteName := fs.String("suite", "", "named suite from the manifest (runs members in declaration order)")
	failFast := fs.Bool("fail-fast", false, "stop after the first failed run and its cleanup")
	validateOnly := fs.Bool("validate", false, "validate scenario sources without platform access")
	preflightOnly := fs.Bool("preflight", false, "check account capacity before provisioning")
	engine := fs.String("engine", "real-vm", "real-vm, local, or simulated")
	baseURL := fs.String("base-url", "", "HTTP loopback origin (optional with local.command)")
	dataPath := fs.String("data", "", "JSON or CSV case data for the local engine")
	load := fs.Bool("load", false, "run native HTTP journeys concurrently with the local engine")
	vus := fs.Int("vus", 0, "concurrent users or arrival-rate concurrency cap (1..50, default 1)")
	rate := fs.Int("rate", 0, "target journeys per second with --load and duration (1..1000)")
	iterations := fs.Int("iterations", 0, "total journeys for --load (1..10000, default 100)")
	duration := fs.String("duration", "", "schedule journeys for this duration with --load (1s..5m)")
	pacing := fs.String("pacing", "", "pause between each user's load journeys (0s..1m)")
	progress := fs.Bool("progress", false, "print live load progress to stderr")
	baselinePath := fs.String("baseline", "", "compare local load with a saved successful JSON report")
	profile := fs.String("profile", "all", "warm, cold, restored, or all")
	repeat := fs.Int("repeat", 1, "runs per lifecycle profile or local case (1..20)")
	maxWorkloadMinutes := fs.Int("max-workload-minutes", 0, "maximum estimated VM workload-minutes for this command (0 disables guard)")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "scenario manifest path")
	reportPath := fs.String("report", "", "write a JSON report to this path")
	junitPath := fs.String("junit", "", "write a JUnit XML report to this path")
	htmlPath := fs.String("html", "", "write a standalone HTML report to this path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale test [--validate|--preflight] [--scenario NAME|--suite NAME] [--fail-fast] [--engine real-vm|local|simulated] [--base-url URL] [--data PATH] [--load [--vus N] [--iterations N|--duration D [--rate N]] [--pacing D] [--progress] [--baseline PATH]] [--profile warm|cold|restored|all] [--repeat N] [--max-workload-minutes N] [--manifest PATH] [--report PATH] [--junit PATH] [--html PATH]", "test")
		return 1
	}
	if err := validateDistinctTestReportPaths(*reportPath, *junitPath, *htmlPath); err != nil {
		return printErr("Invalid test report paths", err)
	}
	loadOverrides := testLoadOverrides{VUs: *vus, Iterations: *iterations, Rate: *rate, Duration: *duration, Pacing: *pacing}
	fs.Visit(func(selected *flag.Flag) {
		switch selected.Name {
		case "vus":
			loadOverrides.VUsSet = true
		case "iterations":
			loadOverrides.IterationsSet = true
		case "rate":
			loadOverrides.RateSet = true
		case "duration":
			loadOverrides.DurationSet = true
		case "pacing":
			loadOverrides.PacingSet = true
		}
	})
	if !*load && (loadOverrides.VUsSet || loadOverrides.IterationsSet || loadOverrides.RateSet || loadOverrides.DurationSet || loadOverrides.PacingSet || *progress) {
		return printErr("Invalid load options", errors.New("--vus, --iterations, --rate, --duration, --pacing, and --progress require --load"))
	}
	if *baselinePath != "" && (!*load || *validateOnly || *preflightOnly) {
		return printErr("Invalid baseline options", errors.New("--baseline requires --load and test execution"))
	}
	if *repeat < 1 || *repeat > 20 {
		return printErr("Invalid repeat count", errors.New("--repeat must be between 1 and 20"))
	}
	if *maxWorkloadMinutes < 0 {
		return printErr("Invalid workload-minute limit", errors.New("--max-workload-minutes must be zero or greater"))
	}
	if (*validateOnly && *preflightOnly) || ((*validateOnly || *preflightOnly) && (*reportPath != "" || *junitPath != "" || *htmlPath != "")) {
		return printErr("Invalid test options", errors.New("validation and preflight do not run scenarios or write reports"))
	}
	if *scenarioName != "" && *suiteName != "" {
		return printErr("Invalid test selection", errors.New("--scenario and --suite are mutually exclusive"))
	}
	if *scenarioName == "" && *suiteName == "" && !*validateOnly {
		return printErr("Scenario required", errors.New("pass --scenario NAME or --suite NAME"))
	}
	if *failFast && (*validateOnly || *preflightOnly) {
		return printErr("Invalid test options", errors.New("--fail-fast applies only to test execution"))
	}
	if *suiteName != "" {
		if *dataPath != "" {
			return printErr("Invalid suite data", errors.New("declare data on suite members instead of using --data with --suite"))
		}
		options := testSuiteOptions{
			Engine: *engine, Profile: *profile, BaseURL: *baseURL, Repeat: *repeat,
			Load: *load, LoadOverrides: loadOverrides, Progress: *progress,
			BaselinePath:       *baselinePath,
			MaxWorkloadMinutes: *maxWorkloadMinutes, Validate: *validateOnly, Preflight: *preflightOnly,
		}
		fs.Visit(func(selected *flag.Flag) {
			options.EngineSet = options.EngineSet || selected.Name == "engine"
			options.ProfileSet = options.ProfileSet || selected.Name == "profile"
		})
		return cmdTestSuite(*manifestPath, *suiteName, options, *failFast, *reportPath, *junitPath, *htmlPath)
	}
	if *load && *engine != "local" {
		return printErr("Invalid load engine", errors.New("--load currently requires --engine local"))
	}
	if *engine != "local" && (*baseURL != "" || *dataPath != "") {
		return printErr("Invalid local test options", errors.New("--base-url and --data apply only to --engine local"))
	}
	var profiles []string
	switch *engine {
	case "real-vm":
		var err error
		profiles, err = selectedTestProfiles(*profile)
		if err != nil {
			return printErr("Invalid profile", err)
		}
	case "simulated", "local":
		explicitProfile := false
		fs.Visit(func(selected *flag.Flag) { explicitProfile = explicitProfile || selected.Name == "profile" })
		if explicitProfile {
			return printErr("Invalid profile", errors.New("--profile applies only to real-vm tests"))
		}
		if *engine == "local" {
			if *maxWorkloadMinutes > 0 {
				return printErr("Invalid local test options", errors.New("--max-workload-minutes applies only to real-vm tests"))
			}
			if *baseURL != "" {
				resolvedURL, err := validateLocalTestURL(*baseURL)
				if err != nil {
					return printErr("Invalid local test URL", err)
				}
				*baseURL = resolvedURL
			}
		}
	default:
		return printErr("Invalid engine", fmt.Errorf("engine %q is invalid; use real-vm, local, or simulated", *engine))
	}
	cases, dataFields, err := readTestData(*dataPath)
	if err != nil {
		return printErr("Invalid test case data", err)
	}
	scenarios, sourceDir, err := readTestManifestForScenario(*manifestPath, *scenarioName, dataFields)
	if err != nil {
		return printErr("Invalid scenario manifest", err)
	}
	loadConfigs := make(map[string]*testLoadConfig)
	if *load {
		for name, scenario := range scenarios {
			if *scenarioName != "" && name != *scenarioName {
				continue
			}
			cfg, err := resolveTestLoadConfig(scenario, loadOverrides)
			if err != nil {
				return printErr("Invalid load scenario", fmt.Errorf("scenario %q: %w", name, err))
			}
			if *progress {
				cfg.Progress = func(update testLoadProgress) { printTestLoadProgress(osStderr, update) }
			}
			loadConfigs[name] = cfg
		}
	}
	if *validateOnly {
		return validateTestManifestForEngine(scenarios, sourceDir, *scenarioName, *engine)
	}
	if *preflightOnly {
		if *engine != "real-vm" {
			return printErr("Invalid preflight", errors.New("--preflight applies only to real-vm tests"))
		}
		if _, err := collectTestValidation(scenarios, sourceDir, *scenarioName); err != nil {
			return printErr("Invalid scenario source", err)
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		return runTestPreflight(context.Background(), client, *scenarioName, scenarios[*scenarioName], profiles, *repeat)
	}
	scenario, ok := scenarios[*scenarioName]
	if !ok {
		return printErr("Unknown scenario", fmt.Errorf("%q is not declared in %s", *scenarioName, *manifestPath))
	}
	if *engine == "real-vm" && *maxWorkloadMinutes > 0 {
		estimate := estimateTestWorkloadMinutes(scenario, len(profiles)*(*repeat))
		if estimate > *maxWorkloadMinutes {
			return printErr("Test resource guard exceeded", fmt.Errorf("up to %d workload-minutes exceed --max-workload-minutes %d; select fewer profiles or attempts, or raise the limit", estimate, *maxWorkloadMinutes))
		}
	}
	var client *Client
	if *engine == "real-vm" {
		if _, err := collectTestValidation(scenarios, sourceDir, *scenarioName); err != nil {
			return printErr("Invalid scenario source", err)
		}
		client, err = authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
	} else if *engine == "simulated" && len(scenario.Simulation) == 0 {
		return printErr("No simulation", fmt.Errorf("scenario %q has no simulation command", *scenarioName))
	} else if *engine == "local" {
		if err := validateLocalTestScenario(scenario); err != nil {
			return printErr("Invalid local scenario", err)
		}
		if *baseURL == "" && scenario.Local == nil {
			return printErr("Invalid local test URL", errors.New("--engine local requires --base-url or a local.command in the scenario"))
		}
	}
	prepared := testPreparedScenario{
		Name: *scenarioName, Scenario: scenario, ManifestDir: sourceDir,
		Engine: *engine, Profiles: profiles, Cases: cases, BaseURL: *baseURL, Load: loadConfigs[*scenarioName],
	}
	return executePreparedTests(client, "", []testPreparedScenario{prepared}, *repeat, *failFast, *reportPath, *junitPath, *htmlPath, *baselinePath)
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

func readTestManifest(path string, dataFields ...string) (map[string]testScenario, string, error) {
	return readTestManifestForScenario(path, "", dataFields)
}

func readTestManifestForScenario(path, selected string, dataFields []string) (map[string]testScenario, string, error) {
	manifest, dir, err := readTestManifestDocument(path, func(name string, scenario testScenario) []string {
		if selected != "" && name != selected {
			return testHTTPDataFields(scenario)
		}
		return dataFields
	})
	return manifest.Scenarios, dir, err
}

func readTestManifestDocument(path string, fieldsForScenario func(string, testScenario) []string) (testManifest, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return testManifest{}, "", err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return testManifest{}, "", err
	}
	if len(data) > 1024*1024 {
		return testManifest{}, "", errors.New("scenario manifest exceeds 1 MiB")
	}
	var manifest testManifest
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return testManifest{}, "", err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return testManifest{}, "", errors.New("scenario manifest must contain exactly one YAML document")
	}
	if manifest.Version != 1 {
		return testManifest{}, "", errors.New("scenario manifest version must be 1")
	}
	if err := validateTestSuites(manifest); err != nil {
		return testManifest{}, "", err
	}
	for name, scenario := range manifest.Scenarios {
		if len(name) < 3 || len(name) > 80 || !api.ValidAppSlug(scenario.Project) {
			return testManifest{}, "", fmt.Errorf("scenario %q needs a valid project slug", name)
		}
		if len(scenario.Command) == 0 && len(scenario.Requests) == 0 && len(scenario.Checks) == 0 {
			return testManifest{}, "", fmt.Errorf("scenario %q needs a command, requests, or checks", name)
		}
		if len(scenario.Command) > 0 && scenario.Command[0] == "" {
			return testManifest{}, "", fmt.Errorf("scenario %q has an empty assertion command", name)
		}
		fields := fieldsForScenario(name, scenario)
		if err := validateTestHTTPRequestsWithData(scenario, fields); err != nil {
			return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
		}
		if err := validateTestLoadSpec(scenario.Load); err != nil {
			return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
		}
		if err := validateTestLoadStepThresholds(scenario); err != nil {
			return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
		}
		if err := validateTestLocalAppSpec(scenario.Local); err != nil {
			return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
		}
		if len(scenario.Simulation) > 0 && scenario.Simulation[0] == "" {
			return testManifest{}, "", fmt.Errorf("scenario %q has an empty simulation command", name)
		}
		if len(scenario.Services) > 15 {
			return testManifest{}, "", fmt.Errorf("scenario %q may declare at most 15 services", name)
		}
		for service, spec := range scenario.Services {
			if !api.ValidAppSlug(service) ||
				len(scenario.Project)+1+len(service) > 40 || service == scenario.Project || (spec.Source == "") == (spec.Fixture == "") {
				return testManifest{}, "", fmt.Errorf("scenario %q service %q needs exactly one source or fixture", name, service)
			}
			if spec.Fixture != "" && spec.Fixture != testDeliverySinkFixture {
				return testManifest{}, "", fmt.Errorf("scenario %q service %q has unknown fixture %q", name, service, spec.Fixture)
			}
			if spec.FailFirst < 0 || spec.FailFirst > 20 || (spec.FailFirst != 0 && spec.Fixture != testDeliverySinkFixture) {
				return testManifest{}, "", fmt.Errorf("scenario %q service %q has invalid fail_first", name, service)
			}
			if spec.Fixture == testDeliverySinkFixture {
				if _, reserved := spec.Secrets["GREGALE_TEST_SINK_FAIL_FIRST"]; reserved {
					return testManifest{}, "", fmt.Errorf("scenario %q service %q sets reserved fixture secret", name, service)
				}
				if _, reserved := spec.Secrets["GREGALE_TEST_SINK_TOKEN"]; reserved {
					return testManifest{}, "", fmt.Errorf("scenario %q service %q sets reserved fixture secret", name, service)
				}
			}
			if spec.Fixture != "" && len(spec.AsyncRoutes) > 0 {
				return testManifest{}, "", fmt.Errorf("scenario %q service %q cannot add async routes to a built-in fixture", name, service)
			}
			seenRoutes := map[string]bool{}
			for _, route := range spec.AsyncRoutes {
				if !strings.HasPrefix(route.Path, "/") || len(route.Path) > 2048 || len(route.Methods) == 0 || len(route.Methods) > 4 {
					return testManifest{}, "", fmt.Errorf("scenario %q service %q has an invalid async route", name, service)
				}
				for _, method := range route.Methods {
					if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
						return testManifest{}, "", fmt.Errorf("scenario %q service %q async route has unsupported method %q", name, service, method)
					}
					key := method + " " + route.Path
					if seenRoutes[key] {
						return testManifest{}, "", fmt.Errorf("scenario %q service %q has duplicate async route %s", name, service, key)
					}
					seenRoutes[key] = true
				}
				if route.RetryPolicy != nil {
					if problem := route.RetryPolicy.dto().Validate(); problem != nil {
						return testManifest{}, "", fmt.Errorf("scenario %q service %q async route retry policy: %s", name, service, problem.Detail)
					}
				}
			}
		}
		if len(scenario.Trigger) > 0 && scenario.Trigger[0] == "" {
			return testManifest{}, "", fmt.Errorf("scenario %q has an empty trigger command", name)
		}
		if (scenario.WaitFor.QueueIdle || len(scenario.WaitFor.Invocations) > 0 || len(scenario.WaitFor.Objects) > 0 || len(scenario.WaitFor.Deliveries) > 0) && len(scenario.Trigger) == 0 && len(scenario.Requests) == 0 {
			return testManifest{}, "", fmt.Errorf("scenario %q needs trigger or requests when wait_for is set", name)
		}
		seenTriggerKeys := map[string]bool{}
		for _, invocation := range scenario.WaitFor.Invocations {
			if _, ok := scenario.Services[invocation.Service]; !ok || !testTriggerKeyPattern.MatchString(invocation.TriggerKey) || seenTriggerKeys[invocation.TriggerKey] || invocation.MinAttempts < 0 {
				return testManifest{}, "", fmt.Errorf("scenario %q has an invalid invocation wait condition", name)
			}
			seenTriggerKeys[invocation.TriggerKey] = true
		}
		for _, delivery := range scenario.WaitFor.Deliveries {
			service, ok := scenario.Services[delivery.Service]
			if !ok || service.Fixture != testDeliverySinkFixture || delivery.MinAttempts < 1 || delivery.MinAttempts > 100 ||
				(delivery.LastStatus != 0 && (delivery.LastStatus < 200 || delivery.LastStatus > 599)) {
				return testManifest{}, "", fmt.Errorf("scenario %q has an invalid delivery wait condition for service %q", name, delivery.Service)
			}
		}
		for _, step := range append(append([][]string{}, scenario.Setup...), scenario.Cleanup...) {
			if len(step) == 0 || step[0] == "" {
				return testManifest{}, "", fmt.Errorf("scenario %q has an empty setup or cleanup command", name)
			}
		}
		if scenario.Timeout != "" {
			d, parseErr := time.ParseDuration(scenario.Timeout)
			if parseErr != nil || d < time.Second || d > time.Hour {
				return testManifest{}, "", fmt.Errorf("scenario %q timeout must be a duration between 1 second and 1 hour", name)
			}
		}
		seenBuckets := map[string]bool{}
		for _, bucket := range scenario.Buckets {
			if bucket.Name != sanitizeSlug(bucket.Name) || len(bucket.Name) < 3 || len(bucket.Name) > 30 || seenBuckets[bucket.Name] {
				return testManifest{}, "", fmt.Errorf("scenario %q has an invalid or duplicate bucket name %q", name, bucket.Name)
			}
			seenBuckets[bucket.Name] = true
			if bucket.Permission != "" && bucket.Permission != "read" && bucket.Permission != "write" && bucket.Permission != "read_write" {
				return testManifest{}, "", fmt.Errorf("scenario %q bucket %q has invalid permission", name, bucket.Name)
			}
			if bucket.Service != "" && bucket.Service != scenario.Project {
				if _, ok := scenario.Services[bucket.Service]; !ok {
					return testManifest{}, "", fmt.Errorf("scenario %q bucket %q names unknown service %q", name, bucket.Name, bucket.Service)
				}
			}
			if bucket.Prefix != "" && !testBucketPrefixPattern.MatchString(bucket.Prefix) {
				return testManifest{}, "", fmt.Errorf("scenario %q bucket %q has invalid secret prefix", name, bucket.Name)
			}
		}
		for _, output := range scenario.WaitFor.Objects {
			if !seenBuckets[output.Bucket] || output.MinCount < 1 || output.MinTotalBytes < 0 {
				return testManifest{}, "", fmt.Errorf("scenario %q has an invalid object wait condition for bucket %q", name, output.Bucket)
			}
		}
		if err := validateTestSecrets(scenario.Project, scenario.Secrets, scenario); err != nil {
			return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
		}
		for service, spec := range scenario.Services {
			if err := validateTestSecrets(service, spec.Secrets, scenario); err != nil {
				return testManifest{}, "", fmt.Errorf("scenario %q: %w", name, err)
			}
		}
		if len(scenario.Consumers) > 16 {
			return testManifest{}, "", fmt.Errorf("scenario %q may declare at most 16 consumers", name)
		}
		if scenario.ConsumerAuthMode != "" && scenario.ConsumerAuthMode != api.ConsumerAuthModeOptional && scenario.ConsumerAuthMode != api.ConsumerAuthModeRequired {
			return testManifest{}, "", fmt.Errorf("scenario %q consumer_auth_mode must be optional or required", name)
		}
		seenConsumers := map[string]bool{}
		for _, consumer := range scenario.Consumers {
			if !api.ValidAppSlug(consumer.Name) || seenConsumers[consumer.Name] {
				return testManifest{}, "", fmt.Errorf("scenario %q has invalid or duplicate consumer %q", name, consumer.Name)
			}
			seenConsumers[consumer.Name] = true
			seenScopes := map[string]bool{}
			for _, scope := range consumer.Scopes {
				if (scope != "read" && scope != "write" && scope != "admin") || seenScopes[scope] {
					return testManifest{}, "", fmt.Errorf("scenario %q consumer %q has invalid or duplicate scope", name, consumer.Name)
				}
				seenScopes[scope] = true
			}
		}
	}
	return manifest, filepath.Dir(absolute), nil
}

var testSecretReferencePattern = regexp.MustCompile(`\$\{([^{}]+)\}`)

func validateTestSecrets(workload string, secrets map[string]string, scenario testScenario) error {
	serviceURLs := map[string]string{scenario.Project: "https://example.test"}
	serviceSlugs := map[string]string{scenario.Project: "example-test"}
	for service := range scenario.Services {
		serviceURLs[service] = "https://example.test"
		serviceSlugs[service] = "example-test"
	}
	buckets := make(map[string]testBucketRef, len(scenario.Buckets))
	for _, bucket := range scenario.Buckets {
		buckets[bucket.Name] = testBucketRef{Name: "example-test"}
	}
	for key, value := range secrets {
		if api.ValidateSecretKey(key) != nil {
			return fmt.Errorf("workload %q has invalid secret key %q", workload, key)
		}
		if _, err := expandTestSecretValue(value, serviceURLs, serviceSlugs, buckets, "0123456789abcdef0123456789abcdef", "example-run-secret"); err != nil {
			return fmt.Errorf("workload %q secret %q: %w", workload, key, err)
		}
	}
	return nil
}

func expandTestSecretValue(value string, serviceURLs, serviceSlugs map[string]string, buckets map[string]testBucketRef, runID, runSecret string) (string, error) {
	var expansionErr error
	expanded := testSecretReferencePattern.ReplaceAllStringFunc(value, func(match string) string {
		if expansionErr != nil {
			return match
		}
		parts := strings.Split(match[2:len(match)-1], ".")
		var replacement string
		var ok bool
		switch {
		case len(parts) == 2 && parts[0] == "run" && parts[1] == "id":
			replacement, ok = runID, true
		case len(parts) == 2 && parts[0] == "run" && parts[1] == "secret":
			replacement, ok = runSecret, true
		case len(parts) == 3 && parts[0] == "service" && parts[2] == "url":
			replacement, ok = serviceURLs[parts[1]]
		case len(parts) == 3 && parts[0] == "service" && parts[2] == "slug":
			replacement, ok = serviceSlugs[parts[1]]
		case len(parts) == 3 && parts[0] == "bucket" && parts[2] == "name":
			bucket, exists := buckets[parts[1]]
			replacement, ok = bucket.Name, exists
		}
		if !ok {
			expansionErr = fmt.Errorf("unknown reference %s", match)
			return match
		}
		return replacement
	})
	if expansionErr != nil {
		return "", expansionErr
	}
	if strings.Contains(expanded, "${") {
		return "", errors.New("malformed reference")
	}
	return expanded, nil
}

type testConsumerClient interface {
	CreateAPIConsumer(context.Context, string, api.CreateAPIConsumerRequest) (api.APIConsumerResponse, error)
	CreateConsumerKey(context.Context, string, string, api.CreateConsumerKeyRequest) (api.ConsumerKeyResponse, error)
}

type testAccessClient interface {
	UpdateApp(context.Context, string, api.UpdateAppRequest) (api.AppResponse, error)
}

func configureTestWorkloadAccess(ctx context.Context, client testAccessClient, slug, consumerMode string) error {
	// Paid plans may create developer sessions with an operator API-key gate
	// and a second public-auth gate. Those defaults reject application
	// customer credentials before the request reaches the app. Test sessions
	// are isolated and expiring, so start with those platform gates open.
	closed := false
	req := api.UpdateAppRequest{
		RequireAuthn: &closed,
		PublicAuth:   &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen},
	}
	if consumerMode != "" {
		req.ConsumerAuthMode = &consumerMode
	}
	_, err := client.UpdateApp(ctx, slug, req)
	return err
}

func provisionTestConsumers(ctx context.Context, client testConsumerClient, appSlug string, specs []testConsumer, runID string, timeout time.Duration) ([]string, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	expiresAt := time.Now().UTC().Add(timeout + time.Hour)
	env := make([]string, 0, 2*len(specs))
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		consumer, err := client.CreateAPIConsumer(ctx, appSlug, api.CreateAPIConsumerRequest{
			ExternalRef: "gregale-test-" + runID + "-" + spec.Name,
			Name:        "Scenario " + spec.Name,
		})
		if err != nil {
			return env, ids, fmt.Errorf("consumer %s: %w", spec.Name, err)
		}
		ids = append(ids, consumer.ID)
		scopes := spec.Scopes
		if len(scopes) == 0 {
			scopes = []string{"read", "write"}
		}
		key, err := client.CreateConsumerKey(ctx, appSlug, consumer.ID, api.CreateConsumerKeyRequest{
			Name: "scenario-" + spec.Name, Scopes: scopes, ExpiresAt: &expiresAt,
		})
		if err != nil {
			return env, ids, fmt.Errorf("consumer %s key: %w", spec.Name, err)
		}
		if key.Key == "" {
			return env, ids, fmt.Errorf("consumer %s key response omitted credential", spec.Name)
		}
		prefix := "GREGALE_TEST_CONSUMER_" + strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))
		env = append(env, prefix+"_ID="+consumer.ID, prefix+"_KEY="+key.Key)
	}
	return env, ids, nil
}

func runSimulatedTest(parent context.Context, name string, scenario testScenario, manifestDir string) (receipt testRunReceipt) {
	receipt = testRunReceipt{Scenario: name, Profile: "simulated", Engine: "simulated", Status: "failed", StartedAt: time.Now().UTC()}
	phases := newTestPhaseRecorder("simulation")
	defer func() {
		receipt.Phases = phases.finish(receipt.Status)
		receipt.FinishedAt = time.Now().UTC()
		receipt.DurationMS = receipt.FinishedAt.Sub(receipt.StartedAt).Milliseconds()
	}()
	if len(scenario.Simulation) == 0 || scenario.Simulation[0] == "" {
		receipt.Error = "scenario has no simulation command"
		return receipt
	}
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
		receipt.Error = fmt.Sprintf("simulation source directory: %v", err)
		return receipt
	}
	env := append(testCommandBaseEnv(), "GREGALE_TEST_ENGINE=simulated", "GREGALE_TEST_PROFILE=simulated", "GREGALE_TEST_SCENARIO="+name)
	if err := runTestCommand(ctx, sourceDir, env, scenario.Simulation); err != nil {
		receipt.Error = fmt.Sprintf("simulation command: %v", err)
		return receipt
	}
	receipt.Status = "passed"
	return receipt
}

func runTestProfile(parent context.Context, client *Client, name string, scenario testScenario, manifestDir, profile string) (receipt testRunReceipt) {
	receipt = testRunReceipt{Scenario: name, Profile: profile, Engine: "real-vm", Status: "failed", Evidence: &testWakeEvidence{}, StartedAt: time.Now().UTC()}
	phases := newTestPhaseRecorder("provision")
	advancePhase := func(name string) {
		status := "passed"
		if receipt.Error != "" {
			status = "failed"
		}
		phases.advance(name, status)
	}
	defer func() {
		receipt.Phases = phases.finish(receipt.Status)
		receipt.FinishedAt = time.Now().UTC()
		receipt.DurationMS = receipt.FinishedAt.Sub(receipt.StartedAt).Milliseconds()
	}()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		receipt.Error = fmt.Sprintf("create test run identity: %v", err)
		return
	}
	receipt.RunID = hex.EncodeToString(random)
	runSecretBytes := make([]byte, 32)
	if _, err := rand.Read(runSecretBytes); err != nil {
		receipt.Error = fmt.Sprintf("create test run secret: %v", err)
		return
	}
	runSecret := hex.EncodeToString(runSecretBytes)
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
	type deployedTestService struct {
		name, project, sourceDir string
		config                   devSourceConfig
		session                  api.DevSessionResponse
	}
	workloads := []deployedTestService{{name: scenario.Project, project: scenario.Project, sourceDir: sourceDir, config: config, session: session}}
	registered := false
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cleanupCancel()
		allDestroyed := true
		for i := len(workloads) - 1; i >= 0; i-- {
			if err := client.DestroyDevSession(cleanupCtx, workloads[i].project, receipt.RunID); err != nil {
				receipt.addCleanupError(fmt.Sprintf("destroy test workload %s: %v", workloads[i].name, err))
				allDestroyed = false
			}
		}
		if registered && allDestroyed {
			if err := client.DeleteScenarioTest(cleanupCtx, receipt.RunID); err != nil {
				receipt.addCleanupError(fmt.Sprintf("release test service namespace: %v", err))
			}
		}
	}()
	defer func() {
		if receipt.Error == "" && receipt.CleanupError == "" {
			return
		}
		diagnosticCtx, diagnosticCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer diagnosticCancel()
		slugs := make(map[string]string, len(workloads))
		for _, workload := range workloads {
			slugs[workload.name] = workload.session.App.Slug
		}
		receipt.Diagnostics = collectTestDiagnostics(diagnosticCtx, client, slugs)
	}()
	serviceNames := make([]string, 0, len(scenario.Services))
	for name := range scenario.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	fixtureTokens := make(map[string]string)
	for _, serviceName := range serviceNames {
		spec := scenario.Services[serviceName]
		serviceDir := ""
		if spec.Fixture != "" {
			var cleanup func()
			serviceDir, cleanup, err = materializeScenarioFixture(spec.Fixture)
			if err == nil {
				defer cleanup()
			}
		} else {
			serviceDir, err = resolveDeploySourceDir(manifestDir, spec.Source)
		}
		if err != nil {
			receipt.Error = fmt.Sprintf("source directory for %s: %v", serviceName, err)
			return
		}
		if spec.Fixture == testDeliverySinkFixture {
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				receipt.Error = fmt.Sprintf("create fixture token for %s: %v", serviceName, err)
				return
			}
			fixtureTokens[serviceName] = hex.EncodeToString(secret)
		}
		serviceConfig, err := resolveDevSourceConfig(serviceDir)
		if err != nil {
			receipt.Error = fmt.Sprintf("detect app shape for %s: %v", serviceName, err)
			return
		}
		projectName := scenario.Project + "-" + serviceName
		serviceSession, err := client.UpsertDevSession(ctx, projectName, serviceConfig.sessionRequest(receipt.RunID, spec.Postgres, ""))
		if err != nil {
			receipt.Error = fmt.Sprintf("create test workload %s: %v", serviceName, err)
			return
		}
		workloads = append(workloads, deployedTestService{name: serviceName, project: projectName, sourceDir: serviceDir, config: serviceConfig, session: serviceSession})
	}
	members := make([]api.ScenarioTestWorkload, 0, len(workloads))
	for _, workload := range workloads {
		members = append(members, api.ScenarioTestWorkload{Workload: workload.name, AppSlug: workload.session.App.Slug})
	}
	if err := client.RegisterScenarioTest(ctx, receipt.RunID, api.RegisterScenarioTestRequest{Members: members}); err != nil {
		receipt.Error = fmt.Sprintf("register isolated service namespace: %v", err)
		return
	}
	registered = true
	for _, workload := range workloads {
		mode := ""
		if workload.name == scenario.Project {
			mode = scenario.ConsumerAuthMode
		}
		if err := configureTestWorkloadAccess(ctx, client, workload.session.App.Slug, mode); err != nil {
			receipt.Error = fmt.Sprintf("configure test workload %s access: %v", workload.name, err)
			return
		}
	}
	consumerIDs := make([]string, 0, len(scenario.Consumers))
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cleanupCancel()
		for _, id := range consumerIDs {
			if _, err := client.RevokeAPIConsumer(cleanupCtx, session.App.Slug, id); err != nil {
				receipt.addCleanupError(fmt.Sprintf("revoke test consumer %s: %v", id, err))
			}
		}
	}()
	bucketEnv := make([]string, 0, 2*len(scenario.Buckets))
	bucketByName := make(map[string]testBucketRef, len(scenario.Buckets))
	for _, spec := range scenario.Buckets {
		owner := workloads[0]
		if spec.Service != "" {
			for _, workload := range workloads {
				if workload.name == spec.Service {
					owner = workload
					break
				}
			}
		}
		bucketName := spec.Name + "-" + receipt.RunID[:8]
		bucket, err := client.CreateObjectBucket(ctx, owner.session.App.Slug, api.CreateObjectBucketRequest{
			Name: bucketName, Region: spec.Region,
		})
		if err != nil {
			receipt.Error = fmt.Sprintf("create test bucket %s: %v", spec.Name, err)
			return
		}
		receipt.Buckets = append(receipt.Buckets, bucket.Name)
		bucketByName[spec.Name] = testBucketRef{Name: bucket.Name, AppSlug: owner.session.App.Slug}
		permission := spec.Permission
		if permission == "" {
			permission = "read_write"
		}
		binding, err := client.CreateObjectStorageComputeBinding(ctx, owner.session.App.Slug, bucket.Name, api.CreateObjectStorageComputeBindingRequest{
			Permission: permission, Prefix: spec.Prefix,
		})
		if err != nil {
			receipt.Error = fmt.Sprintf("bind test bucket %s to %s: %v", spec.Name, owner.name, err)
			return
		}
		bucketEnv = append(bucketEnv, "GREGALE_TEST_BUCKET_"+strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))+"="+bucket.Name)
		bucketEnv = append(bucketEnv, "GREGALE_TEST_BUCKET_PREFIX_"+strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))+"="+binding.Prefix)
	}
	serviceURLs := make(map[string]string, len(workloads))
	serviceSlugs := make(map[string]string, len(workloads))
	serviceAppIDs := make(map[string]string, len(workloads))
	for _, workload := range workloads {
		appURL := canonicalAppURL(workload.session.App)
		parsed, parseErr := url.Parse(appURL)
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			receipt.Error = fmt.Sprintf("test workload %s returned an invalid app URL", workload.name)
			return
		}
		serviceURLs[workload.name] = appURL
		serviceSlugs[workload.name] = workload.session.App.Slug
		serviceAppIDs[workload.name] = workload.session.App.ID
	}
	for _, workload := range workloads {
		secrets := scenario.Secrets
		if workload.name != scenario.Project {
			secrets = scenario.Services[workload.name].Secrets
		}
		if token := fixtureTokens[workload.name]; token != "" {
			if err := client.SetSecret(ctx, workload.session.App.Slug, "GREGALE_TEST_SINK_TOKEN", token); err != nil {
				receipt.Error = fmt.Sprintf("set fixture token for %s: %v", workload.name, err)
				return
			}
			if err := client.SetSecret(ctx, workload.session.App.Slug, "GREGALE_TEST_SINK_FAIL_FIRST", fmt.Sprint(scenario.Services[workload.name].FailFirst)); err != nil {
				receipt.Error = fmt.Sprintf("set fixture failure count for %s: %v", workload.name, err)
				return
			}
		}
		keys := make([]string, 0, len(secrets))
		for key := range secrets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, err := expandTestSecretValue(secrets[key], serviceURLs, serviceSlugs, bucketByName, receipt.RunID, runSecret)
			if err != nil {
				receipt.Error = fmt.Sprintf("resolve secret %s for %s: %v", key, workload.name, err)
				return
			}
			if err := client.SetSecret(ctx, workload.session.App.Slug, key, value); err != nil {
				receipt.Error = fmt.Sprintf("set secret %s for %s: %v", key, workload.name, err)
				return
			}
		}
	}
	consumerEnv, createdIDs, err := provisionTestConsumers(ctx, client, session.App.Slug, scenario.Consumers, receipt.RunID, timeout)
	consumerIDs = append(consumerIDs, createdIDs...)
	if err != nil {
		receipt.Error = fmt.Sprintf("create test consumers: %v", err)
		return
	}
	// The nested deploy command has its own progress output. Keep --json's
	// stdout as one machine-readable test receipt.
	advancePhase("deploy")
	previousStdout := osStdout
	if jsonOutput {
		osStdout = osStderr
	}
	for _, workload := range workloads {
		var deploymentID string
		deployResult := cmdDeployTarballToExisting(ctx, workload.config.deployArgs(workload.session.App.Slug, workload.sourceDir), true, deployExecution{
			onQueued: func(dep api.DeploymentResponse) { deploymentID = dep.ID },
		})
		if deployResult != 0 || deploymentID == "" {
			osStdout = previousStdout
			receipt.Error = fmt.Sprintf("source deployment for %s failed (exit %d)", workload.name, deployResult)
			return
		}
		if workload.name == scenario.Project {
			receipt.DeploymentID = deploymentID
		} else {
			if receipt.Services == nil {
				receipt.Services = make(map[string]string)
			}
			receipt.Services[workload.name] = workload.session.App.Slug
		}
	}
	osStdout = previousStdout
	advancePhase("async_routes")
	for _, workload := range workloads[1:] {
		routes := scenario.Services[workload.name].AsyncRoutes
		if len(routes) == 0 {
			continue
		}
		created, err := createTestAsyncRoutes(ctx, client, workload.session.App.Slug, canonicalAppURL(workload.session.App), routes)
		if err != nil {
			receipt.Error = fmt.Sprintf("configure async routes for %s: %v", workload.name, err)
			return
		}
		if receipt.AsyncRoutes == nil {
			receipt.AsyncRoutes = make(map[string][]string)
		}
		receipt.AsyncRoutes[workload.name] = created
	}
	target, err := url.Parse(canonicalAppURL(session.App))
	if err != nil || target.Scheme == "" || target.Host == "" {
		receipt.Error = "test environment returned an invalid app URL"
		return
	}
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	env := append(testCommandBaseEnv(),
		"GREGALE_TEST_URL="+proxy.URL,
		"GREGALE_TEST_APP_SLUG="+session.App.Slug,
		"GREGALE_TEST_RUN_ID="+receipt.RunID,
		"GREGALE_TEST_PROFILE="+profile,
		"GREGALE_TEST_ENGINE=real-vm",
	)
	env = append(env, bucketEnv...)
	env = append(env, consumerEnv...)
	for _, workload := range workloads[1:] {
		key := strings.ToUpper(strings.ReplaceAll(workload.name, "-", "_"))
		env = append(env, "GREGALE_TEST_SERVICE_"+key+"_URL="+canonicalAppURL(workload.session.App))
		env = append(env, "GREGALE_TEST_SERVICE_"+key+"_APP_SLUG="+workload.session.App.Slug)
		if token := fixtureTokens[workload.name]; token != "" {
			env = append(env, "GREGALE_TEST_SINK_"+key+"_TOKEN="+token)
		}
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cleanupCancel()
		for _, command := range scenario.Cleanup {
			if err := runTestCommand(cleanupCtx, sourceDir, env, command); err != nil {
				receipt.addCleanupError(fmt.Sprintf("fixture cleanup: %v", err))
			}
		}
	}()
	advancePhase("prepare_profile")
	for _, command := range scenario.Setup {
		if err := runTestCommand(ctx, sourceDir, env, command); err != nil {
			receipt.Error = fmt.Sprintf("fixture setup: %v", err)
			return
		}
	}
	for _, workload := range workloads {
		if err := prepareTestProfile(ctx, client, workload.session.App.Slug, profile); err != nil {
			receipt.Error = fmt.Sprintf("prepare %s profile for %s: %v", profile, workload.name, err)
			return
		}
	}
	serviceWakeBaseline := make(map[string]map[string]bool, len(workloads)-1)
	serviceRequestBaseline := make(map[string]map[string]bool, len(workloads)-1)
	serviceRequestCutoff := make(map[string]time.Time, len(workloads)-1)
	for _, workload := range workloads[1:] {
		timeline, err := client.GetAppWakeTimeline(ctx, workload.session.App.Slug, api.AppWakeTimelineOptions{})
		if err != nil {
			receipt.Error = fmt.Sprintf("read wake baseline for %s: %v", workload.name, err)
			return
		}
		seen := make(map[string]bool, len(timeline.Rows))
		for _, row := range timeline.Rows {
			if row.WakeID != "" {
				seen[row.WakeID] = true
			}
		}
		serviceWakeBaseline[workload.name] = seen
		if profile == "warm" {
			requests, err := client.ListAppDebugRequestsWithOptions(ctx, workload.session.App.Slug, api.DebugTelemetryListOptions{Since: "1h", Limit: 200})
			if err != nil {
				receipt.Error = fmt.Sprintf("read request baseline for %s: %v", workload.name, err)
				return
			}
			cutoff, err := time.Parse(time.RFC3339Nano, requests.WindowEnd)
			if err != nil {
				receipt.Error = fmt.Sprintf("request baseline for %s omitted a valid window end: %v", workload.name, err)
				return
			}
			serviceRequestCutoff[workload.name] = cutoff
			seenRequests := make(map[string]bool, len(requests.Requests))
			for _, request := range requests.Requests {
				if id := testTelemetryRequestKey(request); id != "" {
					seenRequests[id] = true
				}
			}
			serviceRequestBaseline[workload.name] = seenRequests
		}
	}
	recorder.reset()
	advancePhase("trigger")
	captures := make(map[string]string)
	if len(scenario.Trigger) > 0 || len(scenario.Requests) > 0 {
		triggerOutputPath := ""
		if len(scenario.Trigger) > 0 && len(scenario.WaitFor.Invocations) > 0 {
			output, err := os.CreateTemp("", "gregale-test-trigger-*.json")
			if err != nil {
				receipt.Error = fmt.Sprintf("create trigger output: %v", err)
				return
			}
			triggerOutputPath = output.Name()
			_ = output.Close()
			defer func() { _ = os.Remove(triggerOutputPath) }()
			env = append(env, "GREGALE_TEST_TRIGGER_OUTPUT="+triggerOutputPath)
		}
		if len(scenario.Trigger) > 0 {
			if err := runTestCommand(ctx, sourceDir, env, scenario.Trigger); err != nil {
				receipt.Error = fmt.Sprintf("application trigger command: %v", err)
				receipt.captureWakeEvidence(recorder)
				return
			}
		}
		var requestEvidence []testHTTPRequestEvidence
		requestEvidence, err = runTestHTTPRequests(ctx, proxy.URL, receipt.RunID, consumerEnv, scenario.Requests, captures)
		receipt.Requests = append(receipt.Requests, requestEvidence...)
		if err != nil {
			receipt.Error = fmt.Sprintf("application request: %v", err)
			receipt.captureWakeEvidence(recorder)
			return
		}
		advancePhase("completion")
		triggerValues := make(map[string]string)
		if triggerOutputPath != "" {
			triggerValues, err = readTestTriggerOutput(triggerOutputPath)
			if err != nil {
				receipt.Error = fmt.Sprintf("read application trigger output: %v", err)
				receipt.captureWakeEvidence(recorder)
				return
			}
		}
		for key, value := range captures {
			if _, exists := triggerValues[key]; exists {
				receipt.Error = fmt.Sprintf("trigger output and HTTP capture both define %s", key)
				receipt.captureWakeEvidence(recorder)
				return
			}
			triggerValues[key] = value
		}
		for _, condition := range scenario.WaitFor.Invocations {
			evidence, err := waitForTestInvocation(ctx, client, condition, serviceAppIDs[condition.Service], triggerValues[condition.TriggerKey])
			receipt.Invocations = append(receipt.Invocations, evidence)
			if err != nil {
				receipt.Error = fmt.Sprintf("wait for invocation from %s: %v", condition.Service, err)
				receipt.captureWakeEvidence(recorder)
				return
			}
		}
		slugs := make([]string, 0, len(workloads))
		for _, workload := range workloads {
			slugs = append(slugs, workload.session.App.Slug)
		}
		outputs, err := waitForTestOutputs(ctx, client, slugs, scenario.WaitFor, bucketByName, receipt.RunID)
		receipt.Outputs = outputs
		if err != nil {
			receipt.Error = fmt.Sprintf("wait for application output: %v", err)
			receipt.captureWakeEvidence(recorder)
			return
		}
		receipt.QueueIdle = scenario.WaitFor.QueueIdle
		for _, condition := range scenario.WaitFor.Deliveries {
			serviceURL := serviceURLs[condition.Service]
			serviceSlug := serviceSlugs[condition.Service]
			evidence, err := waitForTestDelivery(ctx, client, serviceSlug, serviceURL, fixtureTokens[condition.Service], profile,
				serviceWakeBaseline[condition.Service], condition)
			receipt.Deliveries = append(receipt.Deliveries, evidence)
			if err != nil {
				receipt.Error = fmt.Sprintf("wait for delivery sink %s: %v", condition.Service, err)
				receipt.captureWakeEvidence(recorder)
				return
			}
		}
	}
	advancePhase("assertions")
	requestEvidence, err := runTestHTTPRequests(ctx, proxy.URL, receipt.RunID, consumerEnv, scenario.Checks, captures)
	receipt.Requests = append(receipt.Requests, requestEvidence...)
	if err != nil {
		receipt.Error = fmt.Sprintf("application check: %v", err)
	} else if len(scenario.Command) > 0 {
		if err := runTestCommand(ctx, sourceDir, env, scenario.Command); err != nil {
			receipt.Error = fmt.Sprintf("application assertion command: %v", err)
		}
	}
	receipt.captureWakeEvidence(recorder)
	advancePhase("lifecycle_evidence")
	if err := verifyTestProfile(ctx, client, session.App.Slug, profile, receipt.Evidence); err != nil {
		if receipt.Error != "" {
			receipt.Error += "; "
		}
		receipt.Error += fmt.Sprintf("lifecycle evidence: %v", err)
	}
	if profile != "warm" {
		receipt.ServiceWake = make(map[string]testServiceWakeEvidence, len(workloads)-1)
		for _, workload := range workloads[1:] {
			evidence, err := verifyTestServiceWake(ctx, client, workload.session.App.Slug, profile, serviceWakeBaseline[workload.name])
			if err != nil {
				if receipt.Error != "" {
					receipt.Error += "; "
				}
				receipt.Error += fmt.Sprintf("service %s lifecycle evidence: %v", workload.name, err)
			}
			if evidence.WakeID != "" {
				receipt.ServiceWake[workload.name] = evidence
			}
		}
	} else {
		receipt.ServiceHot = make(map[string]testServiceHotEvidence, len(workloads)-1)
		for _, workload := range workloads[1:] {
			evidence, err := verifyTestServiceHot(ctx, client, workload.session.App.Slug,
				serviceWakeBaseline[workload.name], serviceRequestBaseline[workload.name], serviceRequestCutoff[workload.name])
			if err != nil {
				if receipt.Error != "" {
					receipt.Error += "; "
				}
				receipt.Error += fmt.Sprintf("service %s hot evidence: %v", workload.name, err)
			}
			if evidence.RequestID != "" {
				receipt.ServiceHot[workload.name] = evidence
			}
		}
	}
	if receipt.Error == "" {
		receipt.Status = "passed"
	}
	advancePhase("cleanup")
	return
}

func (r *testRunReceipt) addCleanupError(message string) {
	if r.CleanupError != "" {
		r.CleanupError += "; "
	}
	r.CleanupError += message
	r.Status = "failed"
}

func (r *testRunReceipt) captureWakeEvidence(recorder *testProxyRecorder) {
	evidence := recorder.snapshot()
	r.Evidence = &evidence
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

type testWakeBootClient interface {
	ListWakeTimeline(context.Context, string, string, string, int) (api.WakeTimelineResponse, error)
}

type testServiceWakeClient interface {
	testWakeBootClient
	GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error)
}

func verifyTestProfile(ctx context.Context, client testWakeBootClient, slug, profile string, evidence *testWakeEvidence) error {
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
	method, err := verifyWakeBootMethod(ctx, client, slug, evidence.WakeID, want)
	evidence.Method = method
	return err
}

func verifyTestServiceWake(ctx context.Context, client testServiceWakeClient, slug, profile string, baseline map[string]bool) (testServiceWakeEvidence, error) {
	want := "restore"
	if profile == "cold" {
		want = "cold_boot"
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		timeline, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
		if err != nil {
			return testServiceWakeEvidence{}, err
		}
		if wakeID := firstNewTestWakeID(timeline.Rows, baseline); wakeID != "" {
			evidence := testServiceWakeEvidence{WakeID: wakeID}
			evidence.Method, err = verifyWakeBootMethod(ctx, client, slug, wakeID, want)
			return evidence, err
		}
		select {
		case <-ctx.Done():
			return testServiceWakeEvidence{}, ctx.Err()
		case <-deadline.C:
			return testServiceWakeEvidence{}, fmt.Errorf("no %s wake observed after scenario trigger", profile)
		case <-ticker.C:
		}
	}
}

type testServiceHotClient interface {
	GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error)
	ListAppDebugRequestsWithOptions(context.Context, string, api.DebugTelemetryListOptions) (api.DebugTelemetryListResponse, error)
}

func testTelemetryRequestKey(request api.DebugTelemetryRequestItem) string {
	if request.ID != "" {
		return request.ID
	}
	return request.RequestID
}

func firstNewTestServiceRequest(requests []api.DebugTelemetryRequestItem, baseline map[string]bool, cutoff time.Time) (api.DebugTelemetryRequestItem, bool) {
	// The debug API returns newest first. An app-handled row has an instance
	// ID; edge rejections cannot attest that the sibling VM served traffic.
	// ReceivedAt excludes a deployment smoke whose telemetry arrived late.
	for i := len(requests) - 1; i >= 0; i-- {
		request := requests[i]
		receivedAt, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
		if err != nil || !receivedAt.After(cutoff) {
			continue
		}
		if key := testTelemetryRequestKey(request); key != "" && !baseline[key] && request.InstanceID != "" {
			return request, true
		}
	}
	return api.DebugTelemetryRequestItem{}, false
}

func verifyTestServiceHot(ctx context.Context, client testServiceHotClient, slug string,
	wakeBaseline, requestBaseline map[string]bool, cutoff time.Time) (testServiceHotEvidence, error) {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		wakes, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
		if err != nil {
			return testServiceHotEvidence{}, err
		}
		if wakeID := firstNewTestWakeID(wakes.Rows, wakeBaseline); wakeID != "" {
			return testServiceHotEvidence{}, fmt.Errorf("service woke during warm scenario (wake %s)", wakeID)
		}
		requests, err := client.ListAppDebugRequestsWithOptions(ctx, slug, api.DebugTelemetryListOptions{Since: "1h", Limit: 200})
		if err != nil {
			return testServiceHotEvidence{}, err
		}
		if request, ok := firstNewTestServiceRequest(requests.Requests, requestBaseline, cutoff); ok {
			evidence := testServiceHotEvidence{RequestID: request.RequestID, InstanceID: request.InstanceID, Status: request.Status}
			if evidence.RequestID == "" {
				evidence.RequestID = request.ID
			}
			if request.ColdBoot {
				return evidence, fmt.Errorf("service request %s required a wake during warm scenario", evidence.RequestID)
			}
			return evidence, nil
		}
		select {
		case <-ctx.Done():
			return testServiceHotEvidence{}, ctx.Err()
		case <-deadline.C:
			return testServiceHotEvidence{}, errors.New("no app-handled service request observed during warm scenario")
		case <-ticker.C:
		}
	}
}

func firstNewTestWakeID(rows []api.WakeTimelineJSONRow, baseline map[string]bool) string {
	// The API returns newest first. Verify the first post-trigger wake, not
	// a later successful retry that could conceal a failed restore.
	for i := len(rows) - 1; i >= 0; i-- {
		if id := rows[i].WakeID; id != "" && !baseline[id] {
			return id
		}
	}
	return ""
}

func verifyWakeBootMethod(ctx context.Context, client testWakeBootClient, slug, wakeID, want string) (string, error) {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	observed := ""
	for {
		timeline, err := client.ListWakeTimeline(ctx, slug, wakeID, "", 100)
		if err != nil {
			return observed, err
		}
		for _, event := range timeline.Events {
			if event.Kind != "wake.boot_completed" {
				continue
			}
			method, _ := event.Data["method"].(string)
			if method == want {
				return method, nil
			}
			if method != "" {
				observed = method
			}
		}
		select {
		case <-ctx.Done():
			return observed, ctx.Err()
		case <-deadline.C:
			return observed, fmt.Errorf("expected %s boot; observed %q for wake %s", want, observed, wakeID)
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

func testCommandBaseEnv() []string {
	base := os.Environ()
	clean := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if key != "FAAS_TOKEN" && !strings.HasPrefix(key, "GREGALE_TEST_") {
			clean = append(clean, entry)
		}
	}
	return clean
}

type testOutputClient interface {
	QueueState(context.Context, string) (api.QueueStateResponse, error)
	ListBucketObjects(context.Context, string, string, string, string, int) (api.BucketObjectPage, error)
}

func waitForTestOutputs(ctx context.Context, client testOutputClient, slugs []string, conditions testWaitFor, buckets map[string]testBucketRef, runID string) ([]testOutputEvidence, error) {
	if !conditions.QueueIdle && len(conditions.Objects) == 0 {
		return nil, nil
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	idleSamples := 0
	for {
		ready := true
		if conditions.QueueIdle {
			allIdle := true
			for _, slug := range slugs {
				state, err := client.QueueState(ctx, slug)
				if err != nil {
					return nil, fmt.Errorf("read queue state for %s: %w", slug, err)
				}
				if state.Depth != 0 || state.InFlight != 0 {
					allIdle = false
				}
			}
			if allIdle {
				idleSamples++
			} else {
				idleSamples = 0
			}
			ready = idleSamples >= 2
		}
		observed := make([]testOutputEvidence, 0, len(conditions.Objects))
		for _, condition := range conditions.Objects {
			bucket := buckets[condition.Bucket]
			prefix := strings.ReplaceAll(condition.Prefix, "${GREGALE_TEST_RUN_ID}", runID)
			output := testOutputEvidence{Bucket: bucket.Name, Prefix: prefix}
			cursor := ""
			for pages := 0; pages < 100; pages++ {
				page, err := client.ListBucketObjects(ctx, bucket.AppSlug, bucket.Name, prefix, cursor, 100)
				if err != nil {
					return observed, fmt.Errorf("list bucket %s: %w", bucket.Name, err)
				}
				for _, object := range page.Items {
					output.Count++
					output.TotalBytes += object.SizeBytes
				}
				if page.NextCursor == "" {
					break
				}
				if pages == 99 {
					return observed, fmt.Errorf("bucket %s output listing exceeded 10000 objects", bucket.Name)
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

type testDeliveryClient interface {
	GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error)
}

func waitForTestDelivery(ctx context.Context, client testDeliveryClient, slug, serviceURL, token, profile string,
	baseline map[string]bool, condition testDeliveryOutput) (testDeliveryEvidence, error) {
	evidence := testDeliveryEvidence{Service: condition.Service}
	url := strings.TrimRight(serviceURL, "/") + "/__gregale_test__/attempts"
	httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	awake := profile == "warm"
	lastIssue := ""
	for {
		if !awake {
			timeline, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
			if err != nil {
				return evidence, fmt.Errorf("read sink wake timeline: %w", err)
			}
			awake = firstNewTestWakeID(timeline.Rows, baseline) != ""
		}
		if awake {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return evidence, err
			}
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := httpClient.Do(request)
			if err != nil {
				lastIssue = err.Error()
			} else if response.StatusCode >= 500 {
				lastIssue = fmt.Sprintf("HTTP %d", response.StatusCode)
				_ = response.Body.Close()
			} else {
				if response.StatusCode != http.StatusOK {
					_ = response.Body.Close()
					return evidence, fmt.Errorf("read sink attempts: HTTP %d", response.StatusCode)
				}
				var payload struct {
					Attempts []struct {
						Status int `json:"status"`
					} `json:"attempts"`
				}
				err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload)
				_ = response.Body.Close()
				if err != nil {
					return evidence, fmt.Errorf("decode sink attempts: %w", err)
				}
				evidence.Statuses = evidence.Statuses[:0]
				for _, attempt := range payload.Attempts {
					evidence.Statuses = append(evidence.Statuses, attempt.Status)
				}
				evidence.Attempts = len(evidence.Statuses)
				lastIssue = fmt.Sprintf("%d attempts", evidence.Attempts)
				if evidence.Attempts >= condition.MinAttempts &&
					(condition.LastStatus == 0 || evidence.Statuses[evidence.Attempts-1] == condition.LastStatus) {
					return evidence, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return evidence, fmt.Errorf("delivery condition not reached (last read: %s): %w", lastIssue, ctx.Err())
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
