package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

// Bounds on the opt-in version 1 file format, not account resource quotas.
const (
	testBehaviorMaxFiles  = 32
	testBehaviorFileLimit = 64 << 20
)

type testBehaviorSpec struct {
	Version      int                    `yaml:"version"`
	Fixtures     []string               `yaml:"fixtures"`
	Observers    []string               `yaml:"observers"`
	Observations []behaviorcompare.Spec `yaml:"observations"`
}

type testBehaviorFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type testBehaviorSession struct {
	spec         *testBehaviorSpec
	sourceDir    string
	dir          string
	outputPath   string
	evidence     *behaviorcompare.Evidence
	recipeSHA256 string
	caseSHA256   string
	finished     bool
}

func validateTestBehavior(spec *testBehaviorSpec) error {
	if spec == nil {
		return nil
	}
	if spec.Version != behaviorcompare.Version {
		return errors.New("behavior version must be 1")
	}
	if err := behaviorcompare.ValidateSpecs(spec.Observations); err != nil {
		return err
	}
	for _, files := range [][]string{spec.Fixtures, spec.Observers} {
		if len(files) == 0 || len(files) > testBehaviorMaxFiles {
			return fmt.Errorf("behavior requires 1 to %d fixture files and observer files", testBehaviorMaxFiles)
		}
		seen := make(map[string]bool, len(files))
		for _, name := range files {
			if name == "." || path.Clean(name) != name || !filepath.IsLocal(name) || strings.Contains(name, "\\") || seen[name] {
				return errors.New("behavior files must be unique, clean paths relative to the scenario source")
			}
			seen[name] = true
		}
	}
	return nil
}

func testBehaviorIdentities(sourceDir string, spec *testBehaviorSpec) (contract, fixture string, err error) {
	if err := validateTestBehavior(spec); err != nil {
		return "", "", err
	}
	fixtures, err := hashTestBehaviorFiles(sourceDir, spec.Fixtures)
	if err != nil {
		return "", "", fmt.Errorf("behavior fixture files: %w", err)
	}
	observers, err := hashTestBehaviorFiles(sourceDir, spec.Observers)
	if err != nil {
		return "", "", fmt.Errorf("behavior observer files: %w", err)
	}
	contract, fixture = testBehaviorFileIdentities(spec, fixtures, observers)
	return contract, fixture, nil
}

func testBehaviorFileIdentities(spec *testBehaviorSpec, fixtures, observers []testBehaviorFile) (string, string) {
	specs := append([]behaviorcompare.Spec(nil), spec.Observations...)
	sort.Slice(specs, func(i, j int) bool { return specs[i].Key < specs[j].Key })
	contract := testBehaviorDigest(struct {
		Version      int                    `json:"version"`
		Observations []behaviorcompare.Spec `json:"observations"`
		Observers    []testBehaviorFile     `json:"observers"`
	}{spec.Version, specs, observers})
	return contract, testBehaviorDigest(fixtures)
}

func testBehaviorDigest(value any) string {
	body, _ := json.Marshal(value) // Inputs contain only strings, integers and slices.
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func hashTestBehaviorFiles(sourceDir string, paths []string) ([]testBehaviorFile, error) {
	root, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve scenario source: %w", err)
	}
	result := make([]testBehaviorFile, 0, len(paths))
	for _, name := range paths {
		filePath, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("resolve declared file %q: %w", name, err)
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, errors.New("declared behavior file resolves outside the scenario source")
		}
		digest, err := hashTestBehaviorFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("hash declared file %q: %w", name, err)
		}
		result = append(result, testBehaviorFile{Path: name, SHA256: digest})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func hashTestBehaviorFile(filePath string) (string, error) {
	file, err := openCustomerFile(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	count, err := io.Copy(digest, io.LimitReader(file, testBehaviorFileLimit+1))
	if err != nil {
		return "", err
	}
	if count > testBehaviorFileLimit {
		return "", errors.New("declared behavior file exceeds 64 MiB")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func prepareTestBehavior(sourceDir string, scenario testScenario, receipt *testRunReceipt) (*testBehaviorSession, error) {
	spec := scenario.Behavior
	if spec == nil {
		return nil, nil
	}
	evidence := newTestBehaviorEvidence(spec)
	receipt.Behavior = evidence
	contract, fixture, err := testBehaviorIdentities(sourceDir, spec)
	if err != nil {
		return nil, err
	}
	session := &testBehaviorSession{spec: spec, sourceDir: sourceDir, evidence: evidence}
	// Pin the executed operation recipe without treating the application source or
	// runtime configuration under comparison as a required invariant.
	var commandCleanup [][]string
	if scenario.CommandExecution != nil {
		commandCleanup = scenario.Cleanup
	}
	recipe, err := json.Marshal(struct {
		SuspendResume     *testSuspendResumeSpec      `json:"suspend_resume,omitempty"`
		ManagedWorkRoutes map[string][]testAsyncRoute `json:"managed_work_routes,omitempty"`
		ManagedWork       *rehearsalwork.Plan         `json:"managed_work,omitempty"`
		Actions           *testActionSpec             `json:"actions,omitempty"`
		CommandExecution  *testCommandExecutionSpec   `json:"command_execution,omitempty"`
		ObjectsFixture    *testObjectSpec             `json:"objects_fixture,omitempty"`
		PostgresFixture   *testPostgresSpec           `json:"postgres_fixture,omitempty"`
		Cleanup           [][]string                  `json:"cleanup,omitempty"`
		Setup             [][]string
		Trigger           []string
		Command           []string
		Simulation        []string
		Requests          []testHTTPRequest
		Checks            []testHTTPRequest
		Steps             []testScenarioStep
		WaitFor           testWaitFor
		Load              *testLoadSpec
	}{scenario.SuspendResume, testManagedWorkRoutes(scenario), scenario.ManagedWork, scenario.Actions, scenario.CommandExecution, scenario.ObjectsFixture, scenario.PostgresFixture, commandCleanup, scenario.Setup, scenario.Trigger, scenario.Command, scenario.Simulation, scenario.Requests, scenario.Checks, scenario.Steps, scenario.WaitFor, scenario.Load})
	if err != nil {
		return nil, errors.New("behavior operation recipe must be JSON encodable")
	}
	session.recipeSHA256 = testBehaviorDigest(json.RawMessage(recipe))
	evidence.ContractSHA256, evidence.FixtureSHA256 = session.contractIdentity(contract), fixture
	dir, err := os.MkdirTemp("", "gregale-test-behavior-*")
	if err != nil {
		return nil, fmt.Errorf("create behavior output directory: %w", err)
	}
	session.dir, session.outputPath = dir, filepath.Join(dir, "observations.json")
	return session, nil
}

func newTestBehaviorEvidence(spec *testBehaviorSpec) *behaviorcompare.Evidence {
	if spec == nil {
		return nil
	}
	return &behaviorcompare.Evidence{
		Version: behaviorcompare.Version, Status: "incomplete", Provenance: behaviorcompare.Provenance,
		Specs: append([]behaviorcompare.Spec(nil), spec.Observations...),
	}
}

func (session *testBehaviorSession) contractIdentity(files string) string {
	return testBehaviorDigest([]string{files, session.recipeSHA256})
}

func (session *testBehaviorSession) bindCaseData(body []byte) {
	if session != nil {
		digest := sha256.Sum256(body)
		session.caseSHA256 = hex.EncodeToString(digest[:])
		session.evidence.FixtureSHA256 = session.fixtureIdentity(session.evidence.FixtureSHA256)
	}
}

func (session *testBehaviorSession) fixtureIdentity(files string) string {
	if session.caseSHA256 == "" {
		return files
	}
	return testBehaviorDigest([]string{files, session.caseSHA256})
}

func (session *testBehaviorSession) commandEnv(env []string) []string {
	if session == nil {
		return env
	}
	return withTestCommandEnv(env, "GREGALE_TEST_BEHAVIOR_OUTPUT", session.outputPath)
}

// finish is idempotent and also runs on error/interruption, before fixture cleanup.
func (session *testBehaviorSession) finish(receipt *testRunReceipt) {
	if session == nil || session.finished {
		return
	}
	session.finished = true
	defer func() {
		if err := os.RemoveAll(session.dir); err != nil {
			receipt.addCleanupError("remove private behavior evidence output")
		}
	}()
	observations, err := session.read()
	if err == nil {
		session.evidence.Status, session.evidence.Observations = "complete", observations
		return
	}
	session.evidence.Status, session.evidence.Error = "incomplete", err.Error()
	if receipt.Error != "" {
		receipt.Error += "; "
	}
	receipt.Error += "behavior evidence: " + err.Error()
	receipt.Status = "failed"
}

func (session *testBehaviorSession) read() ([]behaviorcompare.Observation, error) {
	contract, fixture, err := testBehaviorIdentities(session.sourceDir, session.spec)
	if err != nil || session.contractIdentity(contract) != session.evidence.ContractSHA256 || session.fixtureIdentity(fixture) != session.evidence.FixtureSHA256 {
		return nil, errors.New("declared behavior fixture or observer files changed or became unavailable during execution")
	}
	file, err := openCustomerFile(session.outputPath)
	if err != nil {
		return nil, errors.New("scenario did not produce a regular behavior output file")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, behaviorcompare.MaxDocumentBytes+1))
	if err != nil {
		return nil, errors.New("could not read behavior output")
	}
	return behaviorcompare.Decode(body, session.spec.Observations)
}

func compareTestBehavior(before, after testRunReceipt) *behaviorcompare.Comparison {
	comparison := behaviorcompare.Compare(before.Behavior, after.Behavior)
	if comparison != nil && (!testExecutionCompleteForEvidence(before) || !testExecutionCompleteForEvidence(after)) {
		comparison.Status, comparison.Reason = "inconclusive", "one or both executions or cleanup steps did not pass"
	}
	return comparison
}

func testBehaviorComparisonStatus(runs []testRunComparison) string {
	status := ""
	for _, run := range runs {
		if run.Behavior == nil {
			continue
		}
		if run.Behavior.Status == "inconclusive" {
			return "inconclusive"
		}
		if status == "" || run.Behavior.Status == "failed" {
			status = run.Behavior.Status
		}
	}
	return status
}

func formatTestBehaviorValue(value json.RawMessage) string {
	if len(value) == 0 {
		return "—"
	}
	text := []rune(string(value))
	if len(text) > 512 {
		return string(text[:512]) + "… (see JSON evidence)"
	}
	return string(text)
}

func printTestBehaviorComparison(out io.Writer, comparison *behaviorcompare.Comparison) {
	if comparison == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  behavior: %s\n", comparison.Status)
	if comparison.Reason != "" {
		_, _ = fmt.Fprintf(out, "    %s\n", comparison.Reason)
	}
	for _, change := range comparison.Changes {
		_, _ = fmt.Fprintf(out, "    %s (%s): %s", change.Key, change.Kind, change.Status)
		if change.Status == "changed" {
			_, _ = fmt.Fprintf(out, " · %s → %s", formatTestBehaviorValue(change.Before), formatTestBehaviorValue(change.After))
		}
		_, _ = fmt.Fprintln(out)
	}
}

func (session *testBehaviorSession) verifyCommandFiles(files map[string][]byte) error {
	lists := make([][]testBehaviorFile, 2)
	for i, paths := range [][]string{session.spec.Fixtures, session.spec.Observers} {
		for _, name := range paths {
			data, ok := files[name]
			if !ok {
				return errors.New("contained command is missing a declared behavior input")
			}
			sum := sha256.Sum256(data)
			lists[i] = append(lists[i], testBehaviorFile{Path: name, SHA256: hex.EncodeToString(sum[:])})
		}
		sort.Slice(lists[i], func(a, b int) bool { return lists[i][a].Path < lists[i][b].Path })
	}
	contract, fixture := testBehaviorFileIdentities(session.spec, lists[0], lists[1])
	if session.contractIdentity(contract) != session.evidence.ContractSHA256 || fixture != session.evidence.FixtureSHA256 {
		return errors.New("contained command input bytes differ from the pinned behavior contract")
	}
	return nil
}
