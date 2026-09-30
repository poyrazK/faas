package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultTestCIWorkflow = ".github/workflows/gregale-test.yml"
	defaultTestCIBudget   = "gregale-test-budget.yaml"
	testCIBaselineName    = "gregale-test-baseline"
	testCIWorkflowName    = "Gregale scenario tests"
	testCIVMWorkflowName  = "Gregale real VM scenario tests"
)

const starterTestCIBudget = `version: 1

runs:
  require_passing: true
  require_same_runs: true
`

type generatedTestCIWorkflow struct {
	Name        string                        `yaml:"name"`
	On          generatedTestCIEvents         `yaml:"on"`
	Permissions map[string]string             `yaml:"permissions"`
	Concurrency *generatedTestCIConcurrency   `yaml:"concurrency,omitempty"`
	Jobs        map[string]generatedTestCIJob `yaml:"jobs"`
}

type generatedTestCIEvents struct {
	Push             *generatedTestCIBranchEvent      `yaml:"push,omitempty"`
	PullRequest      *generatedTestCIBranchEvent      `yaml:"pull_request,omitempty"`
	WorkflowDispatch *generatedTestCIWorkflowDispatch `yaml:"workflow_dispatch,omitempty"`
}

type generatedTestCIBranchEvent struct {
	Branches []string `yaml:"branches"`
}

type generatedTestCIWorkflowDispatch struct {
	Inputs map[string]generatedTestCIWorkflowInput `yaml:"inputs,omitempty"`
}

type generatedTestCIWorkflowInput struct {
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Type        string   `yaml:"type"`
	Default     string   `yaml:"default"`
	Options     []string `yaml:"options,omitempty"`
}

type generatedTestCIConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
}

type generatedTestCIStrategy struct {
	FailFast    bool           `yaml:"fail-fast"`
	MaxParallel int            `yaml:"max-parallel"`
	Matrix      map[string]any `yaml:"matrix"`
}

type generatedTestCIJob struct {
	RunsOn         string                        `yaml:"runs-on"`
	TimeoutMinutes int                           `yaml:"timeout-minutes"`
	Environment    string                        `yaml:"environment,omitempty"`
	Strategy       *generatedTestCIStrategy      `yaml:"strategy,omitempty"`
	Env            map[string]string             `yaml:"env,omitempty"`
	Steps          []generatedTestCIWorkflowStep `yaml:"steps"`
}

type generatedTestCIWorkflowStep struct {
	Name  string            `yaml:"name,omitempty"`
	Uses  string            `yaml:"uses,omitempty"`
	If    string            `yaml:"if,omitempty"`
	Run   string            `yaml:"run,omitempty"`
	Shell string            `yaml:"shell,omitempty"`
	With  map[string]string `yaml:"with,omitempty"`
	Env   map[string]string `yaml:"env,omitempty"`
}

func cmdTestCI(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(osStderr, "usage: gregale test ci init [--manifest PATH] [--suite NAME] [--engine local|simulated|real-vm] [--profiles LIST] [--repeat N] [--max-workload-minutes N] [--environment NAME] [--workflow PATH] [--branch NAME] [--baseline-max-age-days N] [--budget PATH]", "test")
		return 1
	}
	if args[0] == "init" {
		return cmdTestCIInit(args[1:])
	}
	return printErr("Unknown test CI subcommand", fmt.Errorf("unknown test ci command %q; use 'gregale test ci init'", args[0]))
}

func cmdTestCIInit(args []string) int {
	fs := newFlagSet("test ci init", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "gregale-test.yaml", "scenario manifest path")
	suiteName := fs.String("suite", "", "suite from the manifest (inferred when exactly one exists)")
	engine := fs.String("engine", "", "override suite engine (local, simulated, or real-vm)")
	load := fs.Bool("load", false, "include local HTTP load execution")
	repeat := fs.Int("repeat", 0, "runs per scenario (default 1; default 3 with --load)")
	profilesFlag := fs.String("profiles", "all", "real-VM lifecycle profiles (all or comma-separated warm,cold,restored)")
	maxWorkloadMinutes := fs.Int("max-workload-minutes", 0, "required real-VM workload-minute limit per matrix profile")
	environment := fs.String("environment", "gregale-test", "GitHub environment containing real-VM credentials")
	branch := fs.String("branch", "main", "branch that publishes and supplies the baseline")
	baselineMaxAgeDays := fs.Int("baseline-max-age-days", 30, "maximum age of a CI baseline artifact (0 disables the age check)")
	workflowPath := fs.String("workflow", defaultTestCIWorkflow, "new GitHub Actions workflow path")
	budgetPath := fs.String("budget", defaultTestCIBudget, "comparison budget path; creates a starter budget when absent")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale test ci init [--manifest PATH] [--suite NAME] [--engine local|simulated|real-vm] [--profiles LIST] [--repeat N] [--max-workload-minutes N] [--environment NAME] [--workflow PATH] [--branch NAME] [--baseline-max-age-days N] [--budget PATH]", "test")
		return 1
	}
	if *manifestPath == "" || *workflowPath == "" {
		return printErr("Invalid test CI paths", errors.New("--manifest and --workflow must be non-empty paths"))
	}
	if *engine != "" && *engine != "local" && *engine != "simulated" && *engine != "real-vm" {
		return printErr("Invalid CI engine", errors.New("--engine must be local, simulated, or real-vm"))
	}
	repeatSupplied, profilesSupplied, maxWorkloadSupplied := false, false, false
	environmentSupplied, branchSupplied, budgetSupplied := false, false, false
	baselineMaxAgeSupplied := false
	fs.Visit(func(selected *flag.Flag) {
		switch selected.Name {
		case "repeat":
			repeatSupplied = true
		case "profiles":
			profilesSupplied = true
		case "max-workload-minutes":
			maxWorkloadSupplied = true
		case "environment":
			environmentSupplied = true
		case "branch":
			branchSupplied = true
		case "budget":
			budgetSupplied = true
		case "baseline-max-age-days":
			baselineMaxAgeSupplied = true
		}
	})
	if repeatSupplied && (*repeat < 1 || *repeat > 20) {
		return printErr("Invalid repeat count", errors.New("--repeat must be between 1 and 20"))
	}
	repeatCount := *repeat
	if !repeatSupplied {
		repeatCount = 1
		if *load {
			repeatCount = 3
		}
	}
	if *load && *engine == "simulated" {
		return printErr("Invalid CI load options", errors.New("--load requires --engine local"))
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		return printErr("Could not locate repository", err)
	}
	manifestRel, manifestAbs, err := testCIRepositoryPath(repoRoot, *manifestPath, "manifest")
	if err != nil {
		return printErr("Invalid test manifest path", err)
	}
	manifest, _, err := readTestManifestDocument(manifestAbs, func(_ string, scenario testScenario) []string {
		return testHTTPDataFields(scenario)
	})
	if err != nil {
		return printErr("Invalid test manifest", err)
	}
	selectedSuite := strings.TrimSpace(*suiteName)
	if selectedSuite == "" {
		if len(manifest.Suites) == 0 {
			return printErr("Select a test suite", errors.New("the manifest declares no suites; add a suite under 'suites'"))
		}
		if len(manifest.Suites) > 1 {
			return printErr("Select a test suite", fmt.Errorf("the manifest declares %d suites; pass --suite NAME", len(manifest.Suites)))
		}
		for name := range manifest.Suites {
			selectedSuite = name
		}
	}
	suite, ok := manifest.Suites[selectedSuite]
	if !ok {
		return printErr("Invalid test suite", fmt.Errorf("suite %q is not declared in %s", selectedSuite, manifestRel))
	}
	effectiveEngine := *engine
	if effectiveEngine == "" {
		var err error
		effectiveEngine, err = inferTestCIEngine(suite)
		if err != nil {
			return printErr("Choose a CI engine", err)
		}
	}
	if effectiveEngine != "local" && effectiveEngine != "simulated" && effectiveEngine != "real-vm" {
		return printErr("Unsupported CI engine", fmt.Errorf("suite %q resolves to %q; select --engine local, --engine simulated, or --engine real-vm", selectedSuite, effectiveEngine))
	}
	vmProfiles := []string(nil)
	if effectiveEngine == "real-vm" {
		var err error
		vmProfiles, err = parseTestCIProfiles(*profilesFlag)
		if err != nil {
			return printErr("Invalid VM profiles", err)
		}
		if *maxWorkloadMinutes < 1 {
			return printErr("Missing VM workload limit", errors.New("real-vm CI requires --max-workload-minutes to bound each profile job"))
		}
		if repeatCount > 3 {
			return printErr("Invalid VM repeat count", errors.New("real-vm CI supports a default repeat count from 1 to 3"))
		}
		if !validTestCIEnvironment(*environment) {
			return printErr("Invalid GitHub environment", errors.New("--environment must be 1..64 letters, digits, dots, underscores, or dashes"))
		}
		if branchSupplied || budgetSupplied || baselineMaxAgeSupplied {
			return printErr("Invalid VM CI options", errors.New("--branch, --baseline-max-age-days, and --budget apply only to local or simulated workflows; real-vm CI is manually dispatched and stores per-profile evidence"))
		}
	} else {
		if profilesSupplied || maxWorkloadSupplied || environmentSupplied {
			return printErr("Invalid CI options", errors.New("--profiles, --max-workload-minutes, and --environment apply only to real-vm workflows"))
		}
		if *budgetPath == "" {
			return printErr("Invalid comparison budget path", errors.New("--budget must be a non-empty path"))
		}
		if *baselineMaxAgeDays < 0 || *baselineMaxAgeDays > 30 {
			return printErr("Invalid baseline age", errors.New("--baseline-max-age-days must be between 0 and 30; 0 disables the age check"))
		}
		if !validTestCIBranch(*branch) {
			return printErr("Invalid baseline branch", errors.New("--branch must be a simple Git branch name without whitespace, '..', or shell metacharacters"))
		}
	}
	if *load && effectiveEngine != "local" {
		return printErr("Invalid CI load options", errors.New("--load requires a local suite"))
	}
	suiteOptions := testSuiteOptions{
		Engine: effectiveEngine, EngineSet: true, Repeat: repeatCount, Load: *load, Validate: true,
	}
	if effectiveEngine == "real-vm" {
		suiteOptions.Profile, suiteOptions.ProfileSet = vmProfiles[0], true
		suiteOptions.MaxWorkloadMinutes = *maxWorkloadMinutes
	}
	prepared, err := prepareTestSuite(manifestAbs, selectedSuite, suiteOptions)
	if err != nil {
		return printErr("Cannot prepare CI suite", err)
	}
	if effectiveEngine == "local" {
		for _, scenario := range prepared {
			if scenario.Scenario.Local == nil {
				return printErr("CI suite needs a managed local app", fmt.Errorf("scenario %q has no local.command; this workflow generator only starts app processes declared in the manifest", scenario.Name))
			}
		}
	}

	workflowRel, workflowAbs, err := testCIWorkflowPath(repoRoot, *workflowPath)
	if err != nil {
		return printErr("Invalid workflow path", err)
	}
	var budgetRel, budgetAbs string
	if effectiveEngine != "real-vm" {
		budgetRel, budgetAbs, err = testCIRepositoryPath(repoRoot, *budgetPath, "budget")
		if err != nil {
			return printErr("Invalid comparison budget path", err)
		}
		aliases, err := testReportPathAliases(workflowAbs, budgetAbs)
		if err != nil {
			return printErr("Invalid test CI output paths", err)
		}
		if aliases {
			return printErr("Invalid test CI output paths", errors.New("workflow and budget paths must be different files"))
		}
	}
	for _, output := range []string{workflowAbs, budgetAbs} {
		if output == "" {
			continue
		}
		aliases, err := testReportPathAliases(manifestAbs, output)
		if err != nil {
			return printErr("Invalid test CI output paths", err)
		}
		if aliases {
			return printErr("Invalid test CI output paths", errors.New("generated files must not overwrite the test manifest"))
		}
	}

	workflowExists, err := testCIFileExists(workflowAbs)
	if err != nil {
		return printErr("Could not inspect workflow path", err)
	}
	if workflowExists {
		return printErr("Refusing to overwrite workflow", fmt.Errorf("%s already exists; choose a new --workflow path", workflowRel))
	}
	budgetExists := true
	var budgetBody []byte
	if effectiveEngine != "real-vm" {
		budgetExists, err = testCIFileExists(budgetAbs)
		if err != nil {
			return printErr("Could not inspect budget path", err)
		}
		budgetBody = []byte(starterTestCIBudget)
		if budgetExists {
			if _, err := readTestComparisonBudget(budgetAbs); err != nil {
				return printErr("Invalid existing comparison budget", err)
			}
			budgetBody, err = os.ReadFile(budgetAbs)
			if err != nil {
				return printErr("Could not read comparison budget", err)
			}
		}
	}

	consumers := map[string]string(nil)
	if effectiveEngine == "local" {
		consumers = testCIConsumerKeys(prepared)
	}
	workflowBody, err := renderTestCIWorkflow(generatedTestCIOptions{
		Manifest: manifestRel, Suite: selectedSuite, Engine: effectiveEngine,
		Repeat: repeatCount, Load: *load, Branch: *branch,
		BaselineMaxAgeDays: *baselineMaxAgeDays,
		Budget:             budgetRel, WorkflowFile: filepath.Base(workflowRel), Consumers: consumers,
		NeedsNode: testCINeedsNode(prepared), Profiles: vmProfiles,
		MaxWorkloadMinutes: *maxWorkloadMinutes, Environment: *environment,
	})
	if err != nil {
		return printErr("Could not render GitHub Actions workflow", err)
	}

	if !budgetExists {
		if err := writeNewTestCIFile(budgetAbs, budgetBody); err != nil {
			return printErr("Could not create comparison budget", err)
		}
	}
	if err := writeNewTestCIFile(workflowAbs, workflowBody); err != nil {
		if !budgetExists {
			_ = os.Remove(budgetAbs)
		}
		return printErr("Could not create GitHub Actions workflow", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Created %s for suite %s (%s, repeat %d).\n", workflowRel, selectedSuite, effectiveEngine, repeatCount)
	if effectiveEngine == "real-vm" {
		_, _ = fmt.Fprintf(osStdout, "The workflow runs only when manually dispatched. Configure environment %q with variable GREGALE_TEST_API_URL and secret GREGALE_TEST_TOKEN.\n", *environment)
		_, _ = fmt.Fprintf(osStdout, "Profiles: %s; workload limit: %d minutes per profile job.\n", strings.Join(vmProfiles, ", "), *maxWorkloadMinutes)
	} else if !budgetExists {
		_, _ = fmt.Fprintf(osStdout, "Created %s with passing-status and run-identity checks. Add latency and error-rate limits for performance gates.\n", budgetRel)
	}
	if effectiveEngine != "real-vm" {
		if *baselineMaxAgeDays == 0 {
			_, _ = fmt.Fprintln(osStdout, "Pull request comparison skips only until the first baseline is published; push to the baseline branch or run the workflow manually there. Later missing or expired baselines fail the gate. Baseline age checking is disabled.")
		} else {
			_, _ = fmt.Fprintf(osStdout, "Pull request comparison skips only until the first baseline is published; push to the baseline branch or run the workflow manually there. Later missing, expired, or older than %d-day baselines fail the gate.\n", *baselineMaxAgeDays)
		}
	}
	if effectiveEngine == "local" {
		if len(testCIConsumerKeys(prepared)) > 0 {
			_, _ = fmt.Fprintln(osStdout, "Local consumer keys are disposable CI values; replace them if your fixture needs seeded credentials.")
		}
		_, _ = fmt.Fprintln(osStdout, "Add app dependency installation steps to the workflow if your local commands need them.")
	}
	if effectiveEngine == "real-vm" && testCINeedsNode(prepared) {
		_, _ = fmt.Fprintln(osStdout, "Add Node dependency installation steps if your trigger or assertion scripts use packages.")
	}
	return 0
}

type generatedTestCIOptions struct {
	Manifest           string
	Suite              string
	Engine             string
	Repeat             int
	Load               bool
	Branch             string
	BaselineMaxAgeDays int
	Budget             string
	WorkflowFile       string
	Consumers          map[string]string
	NeedsNode          bool
	Profiles           []string
	MaxWorkloadMinutes int
	Environment        string
}

func renderTestCIWorkflow(options generatedTestCIOptions) ([]byte, error) {
	if options.Engine == "real-vm" {
		return renderTestCIVMWorkflow(options)
	}
	steps := []generatedTestCIWorkflowStep{{Name: "Check out repository", Uses: "actions/checkout@v7"}}
	if options.NeedsNode {
		steps = append(steps, generatedTestCIWorkflowStep{
			Name: "Set up Node.js", Uses: "actions/setup-node@v7", With: map[string]string{"node-version": "22"},
		})
	}
	steps = append(steps,
		generatedTestCIWorkflowStep{
			Name:  "Install Gregale CLI",
			Run:   `curl -fsSL https://get.gregale.dev | sh -s -- --dir "$RUNNER_TEMP/gregale"`,
			Shell: "bash",
		},
		generatedTestCIWorkflowStep{
			Name:  "Run Gregale test suite",
			Run:   generatedTestCIRunCommand(options.Load),
			Shell: "bash",
		},
		generatedTestCIWorkflowStep{
			Name:  "Compare with branch baseline",
			If:    "github.event_name == 'pull_request'",
			Env:   map[string]string{"GH_TOKEN": "${{ github.token }}"},
			Run:   generatedTestCICompareCommand(),
			Shell: "bash",
		},
		generatedTestCIWorkflowStep{
			Name: "Upload test reports",
			If:   "always()",
			Uses: "actions/upload-artifact@v6",
			With: map[string]string{
				"name":              "gregale-test-reports",
				"path":              "${{ runner.temp }}/gregale-test-results.json\n${{ runner.temp }}/gregale-test-results.xml\n${{ runner.temp }}/gregale-test-results.html\n${{ runner.temp }}/gregale-test-comparison.html\n${{ runner.temp }}/gregale-test-comparison.md",
				"if-no-files-found": "ignore",
				"retention-days":    "7",
			},
		},
		generatedTestCIWorkflowStep{
			Name: "Publish baseline report",
			If:   "success() && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref_name == env.GREGALE_CI_BASE_BRANCH",
			Uses: "actions/upload-artifact@v6",
			With: map[string]string{
				"name":              testCIBaselineName,
				"path":              "${{ runner.temp }}/gregale-test-results.json",
				"if-no-files-found": "error",
				"retention-days":    "30",
			},
		},
	)

	jobEnv := map[string]string{
		"GREGALE_CI_MANIFEST":              options.Manifest,
		"GREGALE_CI_SUITE":                 options.Suite,
		"GREGALE_CI_ENGINE":                options.Engine,
		"GREGALE_CI_REPEAT":                fmt.Sprint(options.Repeat),
		"GREGALE_CI_BASE_BRANCH":           options.Branch,
		"GREGALE_CI_BASELINE_MAX_AGE_DAYS": fmt.Sprint(options.BaselineMaxAgeDays),
		"GREGALE_CI_BUDGET":                options.Budget,
		"GREGALE_CI_WORKFLOW_FILE":         options.WorkflowFile,
	}
	for name, value := range options.Consumers {
		jobEnv[name] = value
	}
	workflow := generatedTestCIWorkflow{
		Name: testCIWorkflowName,
		On: generatedTestCIEvents{
			Push:             &generatedTestCIBranchEvent{Branches: []string{options.Branch}},
			PullRequest:      &generatedTestCIBranchEvent{Branches: []string{options.Branch}},
			WorkflowDispatch: &generatedTestCIWorkflowDispatch{},
		},
		Permissions: map[string]string{"contents": "read", "actions": "read"},
		Jobs: map[string]generatedTestCIJob{
			"scenarios": {
				RunsOn: "ubuntu-latest", TimeoutMinutes: 30, Env: jobEnv, Steps: steps,
			},
		},
	}
	body, err := yaml.Marshal(workflow)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by `gregale test ci init`; review and customize for your project.\n"), body...), nil
}

func renderTestCIVMWorkflow(options generatedTestCIOptions) ([]byte, error) {
	profileJSON := `[` + `"` + strings.Join(options.Profiles, `","`) + `"` + `]`
	profileMatrix := "${{ inputs.profile == 'all' && fromJSON('" + profileJSON + "') || fromJSON(format('[{0}]', toJSON(inputs.profile))) }}"
	inputs := map[string]generatedTestCIWorkflowInput{
		"profile": {
			Description: "Lifecycle profiles to run",
			Required:    true,
			Type:        "choice",
			Default:     "all",
			Options:     append([]string{"all"}, options.Profiles...),
		},
		"repeat": {
			Description: "Independent attempts per profile",
			Required:    true,
			Type:        "choice",
			Default:     fmt.Sprint(options.Repeat),
			Options:     []string{"1", "2", "3"},
		},
	}
	steps := []generatedTestCIWorkflowStep{{Name: "Check out repository", Uses: "actions/checkout@v7"}}
	if options.NeedsNode {
		steps = append(steps, generatedTestCIWorkflowStep{
			Name: "Set up Node.js", Uses: "actions/setup-node@v7", With: map[string]string{"node-version": "22"},
		})
	}
	steps = append(steps,
		generatedTestCIWorkflowStep{
			Name:  "Install Gregale CLI",
			Run:   `curl -fsSL https://get.gregale.dev | sh -s -- --dir "$RUNNER_TEMP/gregale"`,
			Shell: "bash",
		},
		generatedTestCIWorkflowStep{
			Name:  "Check Gregale credentials",
			Run:   generatedTestCIVMConfigCommand(),
			Shell: "bash",
			Env: map[string]string{
				"FAAS_API":          "${{ vars.GREGALE_TEST_API_URL }}",
				"FAAS_TOKEN":        "${{ secrets.GREGALE_TEST_TOKEN }}",
				"GREGALE_CI_REPEAT": "${{ inputs.repeat }}",
			},
		},
		generatedTestCIWorkflowStep{
			Name:  "Check account capacity",
			Run:   generatedTestCIVMRunCommand(true),
			Shell: "bash",
			Env:   generatedTestCIVMCommandEnv(options),
		},
		generatedTestCIWorkflowStep{
			Name:  "Run Gregale test suite",
			Run:   generatedTestCIVMRunCommand(false),
			Shell: "bash",
			Env:   generatedTestCIVMCommandEnv(options),
		},
		generatedTestCIWorkflowStep{
			Name: "Upload profile evidence",
			If:   "always()",
			Uses: "actions/upload-artifact@v6",
			With: map[string]string{
				"name":              "gregale-test-${{ matrix.profile }}-${{ github.run_id }}",
				"path":              "${{ runner.temp }}/gregale-test-results-${{ matrix.profile }}.*",
				"if-no-files-found": "ignore",
				"retention-days":    "14",
			},
		},
	)
	jobEnv := map[string]string{
		"GREGALE_CI_MANIFEST":             options.Manifest,
		"GREGALE_CI_SUITE":                options.Suite,
		"GREGALE_CI_PROFILE":              "${{ matrix.profile }}",
		"GREGALE_CI_REPEAT":               "${{ inputs.repeat }}",
		"GREGALE_CI_MAX_WORKLOAD_MINUTES": fmt.Sprint(options.MaxWorkloadMinutes),
	}
	workflow := generatedTestCIWorkflow{
		Name: testCIVMWorkflowName,
		On: generatedTestCIEvents{
			WorkflowDispatch: &generatedTestCIWorkflowDispatch{Inputs: inputs},
		},
		Permissions: map[string]string{"contents": "read"},
		Concurrency: &generatedTestCIConcurrency{
			Group: "gregale-real-vm-${{ github.repository }}-${{ github.ref }}", CancelInProgress: false,
		},
		Jobs: map[string]generatedTestCIJob{
			"scenarios": {
				RunsOn: "ubuntu-latest", TimeoutMinutes: 240, Environment: options.Environment,
				Strategy: &generatedTestCIStrategy{
					FailFast: false, MaxParallel: 1, Matrix: map[string]any{"profile": profileMatrix},
				},
				Env: jobEnv, Steps: steps,
			},
		},
	}
	body, err := yaml.Marshal(workflow)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by `gregale test ci init`; review and customize for your project.\n"), body...), nil
}

func generatedTestCIVMCommandEnv(options generatedTestCIOptions) map[string]string {
	return map[string]string{
		"FAAS_API":                        "${{ vars.GREGALE_TEST_API_URL }}",
		"FAAS_TOKEN":                      "${{ secrets.GREGALE_TEST_TOKEN }}",
		"GREGALE_CI_MANIFEST":             options.Manifest,
		"GREGALE_CI_SUITE":                options.Suite,
		"GREGALE_CI_PROFILE":              "${{ matrix.profile }}",
		"GREGALE_CI_REPEAT":               "${{ inputs.repeat }}",
		"GREGALE_CI_MAX_WORKLOAD_MINUTES": fmt.Sprint(options.MaxWorkloadMinutes),
	}
}

func generatedTestCIVMConfigCommand() string {
	return `set -euo pipefail
if [ -z "${FAAS_API:-}" ]; then
  echo '::error::Set GREGALE_TEST_API_URL in the configured GitHub environment.'
  exit 1
fi
if [ -z "${FAAS_TOKEN:-}" ]; then
  echo '::error::Set GREGALE_TEST_TOKEN in the configured GitHub environment.'
  exit 1
fi
case "$GREGALE_CI_REPEAT" in
  1|2|3) ;;
  *) echo '::error::repeat must be 1, 2, or 3.'; exit 1 ;;
esac
api="${FAAS_API%/}"
curl --fail --silent --show-error --max-time 15 "$api/healthz" >/dev/null`
}

func generatedTestCIVMRunCommand(preflight bool) string {
	command := `"$RUNNER_TEMP/gregale/gregale" test \
  --manifest "$GREGALE_CI_MANIFEST" \
  --suite "$GREGALE_CI_SUITE" \
  --engine real-vm \
  --profile "$GREGALE_CI_PROFILE" \
  --repeat "$GREGALE_CI_REPEAT" \
  --max-workload-minutes "$GREGALE_CI_MAX_WORKLOAD_MINUTES"`
	if preflight {
		return command + ` \
  --preflight`
	}
	return command + ` \
  --report "$RUNNER_TEMP/gregale-test-results-$GREGALE_CI_PROFILE.json" \
  --junit "$RUNNER_TEMP/gregale-test-results-$GREGALE_CI_PROFILE.xml" \
  --html "$RUNNER_TEMP/gregale-test-results-$GREGALE_CI_PROFILE.html"`
}

func generatedTestCIRunCommand(load bool) string {
	command := `"$RUNNER_TEMP/gregale/gregale" test \
  --manifest "$GREGALE_CI_MANIFEST" \
  --suite "$GREGALE_CI_SUITE" \
  --engine "$GREGALE_CI_ENGINE" \
  --repeat "$GREGALE_CI_REPEAT" \
  --report "$RUNNER_TEMP/gregale-test-results.json" \
  --junit "$RUNNER_TEMP/gregale-test-results.xml" \
  --html "$RUNNER_TEMP/gregale-test-results.html"`
	if load {
		command += " \\" + "\n  --load"
	}
	return command
}

func generatedTestCICompareCommand() string {
	return `set -euo pipefail
latest_baseline_for_event() {
  gh run list --repo "$GITHUB_REPOSITORY" \
  --workflow "$GREGALE_CI_WORKFLOW_FILE" \
  --branch "$GREGALE_CI_BASE_BRANCH" \
  --event "$1" --status success --limit 1 \
  --json databaseId,createdAt \
  --jq 'if length == 0 then empty else .[0] | [.databaseId, .createdAt] | @tsv end'
}
push_baseline="$(latest_baseline_for_event push)"
manual_baseline="$(latest_baseline_for_event workflow_dispatch)"
baseline="$push_baseline"
if [ -z "$baseline" ]; then
  baseline="$manual_baseline"
elif [ -n "$manual_baseline" ]; then
  push_created_at="${push_baseline#*$'\t'}"
  manual_created_at="${manual_baseline#*$'\t'}"
  if [[ "$manual_created_at" > "$push_created_at" ]]; then
    baseline="$manual_baseline"
  fi
fi
write_baseline_error() {
  {
    echo '## Gregale test comparison'
    echo
    echo "$1"
  } >> "$GITHUB_STEP_SUMMARY"
  echo "::error::$1"
}
if [ -z "$baseline" ]; then
  {
    echo '## Gregale test comparison'
    echo
    echo "No successful baseline exists for '$GREGALE_CI_BASE_BRANCH' yet. Push to that branch or run the workflow manually on it to publish one."
  } >> "$GITHUB_STEP_SUMMARY"
  exit 0
fi
if [[ "$baseline" != *$'\t'* ]]; then
  write_baseline_error 'The latest successful baseline run returned incomplete metadata; the comparison cannot be trusted.'
  exit 1
fi
run_id="${baseline%%$'\t'*}"
baseline_created_at="${baseline#*$'\t'}"
if [[ ! "$run_id" =~ ^[0-9]+$ || -z "$baseline_created_at" || "$baseline_created_at" == *$'\t'* ]]; then
  write_baseline_error 'The latest successful baseline run returned invalid metadata; the comparison cannot be trusted.'
  exit 1
fi
if [[ ! "$GREGALE_CI_BASELINE_MAX_AGE_DAYS" =~ ^[0-9]+$ || "$GREGALE_CI_BASELINE_MAX_AGE_DAYS" -gt 30 ]]; then
  write_baseline_error 'The configured baseline age limit is invalid; set GREGALE_CI_BASELINE_MAX_AGE_DAYS to a value from 0 to 30.'
  exit 1
fi
baseline_max_age_days=$((10#$GREGALE_CI_BASELINE_MAX_AGE_DAYS))
if (( baseline_max_age_days > 0 )); then
  baseline_epoch="$(date -u -d "$baseline_created_at" +%s 2>/dev/null || true)"
  now_epoch="$(date -u +%s)"
  if [[ ! "$baseline_epoch" =~ ^[0-9]+$ || ! "$now_epoch" =~ ^[0-9]+$ ]]; then
    write_baseline_error 'Could not determine the age of the latest successful baseline run.'
    exit 1
  fi
  baseline_age_seconds=$((now_epoch - baseline_epoch))
  if (( baseline_age_seconds < 0 || baseline_age_seconds > baseline_max_age_days * 86400 )); then
    write_baseline_error "The latest successful baseline is older than the configured ${baseline_max_age_days}-day limit. Push to '$GREGALE_CI_BASE_BRANCH' or run the workflow manually on it to refresh the baseline."
    exit 1
  fi
fi
if ! gh run download "$run_id" --repo "$GITHUB_REPOSITORY" \
  --name gregale-test-baseline --dir "$RUNNER_TEMP/gregale-baseline"; then
  write_baseline_error "The latest successful baseline artifact is unavailable or expired. Push to '$GREGALE_CI_BASE_BRANCH' or run the workflow manually on it to refresh the baseline."
  exit 1
fi
if [ ! -f "$RUNNER_TEMP/gregale-baseline/gregale-test-results.json" ]; then
  write_baseline_error 'The latest successful baseline artifact did not contain gregale-test-results.json; push a successful run to refresh it.'
  exit 1
fi
compare_status=0
"$RUNNER_TEMP/gregale/gregale" test compare \
  "$RUNNER_TEMP/gregale-baseline/gregale-test-results.json" \
  "$RUNNER_TEMP/gregale-test-results.json" \
  --budget "$GREGALE_CI_BUDGET" \
  --html "$RUNNER_TEMP/gregale-test-comparison.html" \
  --markdown "$RUNNER_TEMP/gregale-test-comparison.md" \
  --github-summary || compare_status=$?
exit "$compare_status"`
}

func inferTestCIEngine(suite testSuite) (string, error) {
	engines := make(map[string]struct{})
	for _, member := range suite.Scenarios {
		engine := member.Engine
		if engine == "" {
			engine = "real-vm"
		}
		engines[engine] = struct{}{}
	}
	if len(engines) != 1 {
		return "", errors.New("suite members use different engines; pass --engine local, --engine simulated, or --engine real-vm to choose one for CI")
	}
	for engine := range engines {
		return engine, nil
	}
	return "", errors.New("suite does not declare any scenarios")
}

func parseTestCIProfiles(value string) ([]string, error) {
	if value == "all" {
		return []string{"warm", "cold", "restored"}, nil
	}
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("--profiles must be 'all' or a comma-separated list of warm, cold, and restored")
	}
	profiles := strings.Split(value, ",")
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if profile != "warm" && profile != "cold" && profile != "restored" {
			return nil, fmt.Errorf("profile %q is invalid; use warm, cold, restored, or all", profile)
		}
		if seen[profile] {
			return nil, fmt.Errorf("profile %q is listed more than once", profile)
		}
		seen[profile] = true
	}
	return profiles, nil
}

func validTestCIEnvironment(environment string) bool {
	if environment == "" || len(environment) > 64 {
		return false
	}
	for _, r := range environment {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-", r) {
			return false
		}
	}
	return true
}

func testCIConsumerKeys(scenarios []testPreparedScenario) map[string]string {
	keys := make(map[string]string)
	for _, scenario := range scenarios {
		for _, consumer := range scenario.Scenario.Consumers {
			key := "GREGALE_TEST_CONSUMER_" + strings.ToUpper(strings.ReplaceAll(consumer.Name, "-", "_")) + "_KEY"
			keys[key] = "gregale-ci-test-" + consumer.Name
		}
	}
	return keys
}

func testCINeedsNode(scenarios []testPreparedScenario) bool {
	for _, scenario := range scenarios {
		commands := [][]string{scenario.Scenario.Command, scenario.Scenario.Trigger}
		commands = append(commands, scenario.Scenario.Setup...)
		commands = append(commands, scenario.Scenario.Cleanup...)
		if scenario.Scenario.Local != nil {
			commands = append(commands, scenario.Scenario.Local.Command)
		}
		for _, command := range commands {
			if len(command) == 0 {
				continue
			}
			executable := strings.ToLower(filepath.Base(command[0]))
			if executable == "node" || executable == "npm" || executable == "npx" {
				return true
			}
		}
	}
	return false
}

func validTestCIBranch(branch string) bool {
	if branch == "" || strings.TrimSpace(branch) != branch ||
		strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.Contains(branch, "@{") {
		return false
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" || component == "." || component == ".." || strings.HasPrefix(component, ".") ||
			strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	for _, r := range branch {
		if (r < 97 || r > 122) && (r < 65 || r > 90) && (r < 48 || r > 57) && !strings.ContainsRune("-_./", r) {
			return false
		}
	}
	return true
}

func testCIRepositoryPath(repoRoot, path, label string) (string, string, error) {
	if strings.Contains(path, "\\") || strings.ContainsAny(path, "\r\n") {
		return "", "", fmt.Errorf("%s path must not contain backslashes or line breaks", label)
	}
	abs := filepath.Clean(path)
	if !filepath.IsAbs(path) {
		var err error
		abs, err = filepath.Abs(filepath.Join(repoRoot, filepath.Clean(path)))
		if err != nil {
			return "", "", err
		}
	}
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s path must stay inside the repository", label)
	}
	return filepath.ToSlash(rel), abs, nil
}

func testCIWorkflowPath(repoRoot, path string) (string, string, error) {
	rel, abs, err := testCIRepositoryPath(repoRoot, path, "workflow")
	if err != nil {
		return "", "", err
	}
	workflowDir := filepath.ToSlash(filepath.Join(".github", "workflows")) + "/"
	if !strings.HasPrefix(rel, workflowDir) || (filepath.Ext(rel) != ".yml" && filepath.Ext(rel) != ".yaml") {
		return "", "", errors.New("--workflow must be a .yml or .yaml file inside .github/workflows")
	}
	return rel, abs, nil
}

func testCIFileExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writeNewTestCIFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
