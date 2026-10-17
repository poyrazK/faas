package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
)

type objectClientTest struct {
	request                       rehearsalobj.CreateRequest
	ack                           rehearsalobj.Receipt
	evidence                      rehearsalobj.Evidence
	lost, cleanupFailure, deleted bool
}

func (c *objectClientTest) CreateScenarioObjects(_ context.Context, _ string, r rehearsalobj.CreateRequest) (rehearsalobj.Receipt, error) {
	c.request = r
	if c.lost {
		return c.ack, errors.New("lost response")
	}
	return c.ack, nil
}
func (c *objectClientTest) SealScenarioObjects(context.Context, string, string) (rehearsalobj.Evidence, error) {
	return c.evidence, nil
}
func (c *objectClientTest) DeleteScenarioObjects(context.Context, string) error {
	c.deleted = true
	if c.cleanupFailure {
		return errors.New("cleanup failure")
	}
	return nil
}
func objectScenarioForTest() testScenario {
	return testScenario{CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained"}, ObjectsFixture: &testObjectSpec{Version: 1, Bucket: "imports", Fixtures: []testObjectFixture{{Key: "customers/seed", File: "seed.bin"}}}}
}
func TestObjectFixturePinsBinaryBytesAndCleansAmbiguousCreate(t *testing.T) {
	dir := t.TempDir()
	body := []byte{0, 1, 255, 10}
	write := func(b []byte) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, "seed.bin"), b, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	write(body)
	r := testRunReceipt{RunID: strings.Repeat("a", 32), AppSlug: "test-app"}
	session, e := prepareTestObjects(dir, objectScenarioForTest(), &r)
	if e != nil {
		t.Fatal(e)
	}
	write([]byte("changed"))
	c := &objectClientTest{lost: true}
	if e = session.create(t.Context(), c, &r, "app"); e == nil {
		t.Fatal("lost response passed")
	}
	session.cleanup(t.Context(), c, &r)
	if !c.deleted || string(c.request.Objects[0].Body) != string(body) || c.request.Validate() != nil {
		t.Fatal("fixture bytes were not pinned or cleanup was skipped")
	}
}
func TestObjectFixtureRefusesUnsafeRecipesAndPaths(t *testing.T) {
	for _, name := range []string{"host", "ordinary buckets", "cleanup", "traversal", "symlink", "directory", "credentials", "endpoint", "duplicate key", "large"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := objectScenarioForTest()
			if e := os.WriteFile(filepath.Join(dir, "seed.bin"), []byte("data"), 0o600); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "host":
				s.CommandExecution = nil
			case "ordinary buckets":
				s.Buckets = []testBucket{{Name: "live"}}
			case "cleanup":
				s.Cleanup = [][]string{{"true"}}
			case "traversal":
				s.ObjectsFixture.Fixtures[0].File = "../seed.bin"
			case "symlink":
				if e := os.Symlink("seed.bin", filepath.Join(dir, "alias")); e != nil {
					t.Fatal(e)
				}
				s.ObjectsFixture.Fixtures[0].File = "alias"
			case "directory":
				s.ObjectsFixture.Fixtures[0].File = "folder"
				if e := os.Mkdir(filepath.Join(dir, "folder"), 0o700); e != nil {
					t.Fatal(e)
				}
			case "credentials":
				s.Secrets = map[string]string{"aws_access_key_id": "production"}
			case "endpoint":
				s.CommandExecution.Env = map[string]string{"AWS_ENDPOINT_URL": "https://production.invalid"}
			case "duplicate key":
				s.ObjectsFixture.Fixtures = append(s.ObjectsFixture.Fixtures, s.ObjectsFixture.Fixtures[0])
			case "large":
				if e := os.WriteFile(filepath.Join(dir, "seed.bin"), make([]byte, rehearsalobj.MaxObjectBytes+1), 0o600); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := prepareTestObjects(dir, s, &testRunReceipt{}); e == nil {
				t.Fatal("unsupported fixture was admitted")
			}
		})
	}
}
func objectEvidenceForTest(duplicates int) *rehearsalobj.Evidence {
	before := []rehearsalobj.Object{}
	spec := rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(before)}
	objects, _ := rehearsalobj.Snapshot([]rehearsalobj.Data{{Key: "exports/result", Body: []byte("same"), ContentType: "text/plain"}})
	mutations := make([]rehearsalobj.Mutation, 0, duplicates)
	for i := range duplicates {
		mutations = append(mutations, rehearsalobj.Mutation{Sequence: i + 1, Method: "PUT", Key: objects[0].Key, Status: 200, Object: &objects[0]})
	}
	return &rehearsalobj.Evidence{Receipt: rehearsalobj.Receipt{Version: 1, RunID: "run", AppID: "app", ResourceID: "resource", CollectorID: "collector", FixtureSHA256: spec.FixtureSHA256, ContractSHA256: spec.ContractSHA256(), InitialSHA256: spec.FixtureSHA256}, Spec: spec, Status: "complete", Provenance: rehearsalobj.Provenance, DeploymentID: "deployment", Before: before, After: objects, Mutations: mutations, CleanupConfirmed: true, CollectorClosed: true}
}
func TestObjectComparisonReportsDuplicatesAndGatesIncompleteCleanup(t *testing.T) {
	a := testRunReceipt{Scenario: "import", Engine: "real-vm", Profile: "warm", Attempt: 1, Status: "passed", Objects: objectEvidenceForTest(1)}
	b := a
	b.Objects = objectEvidenceForTest(2)
	c, e := compareTestReports("base", "candidate", []testRunReceipt{a}, []testRunReceipt{b})
	if e != nil || c.ObjectStatus != "failed" {
		t.Fatalf("duplicate missed: %+v %v", c, e)
	}
	markdown := renderTestComparisonMarkdown(c)
	if !strings.Contains(markdown, "Object storage comparison: FAILED") || !strings.Contains(markdown, "1 → 2 mutations") || !strings.Contains(markdown, "exports/result") {
		t.Fatalf("missing object evidence: %s", markdown)
	}
	b.CleanupError = "unconfirmed"
	c, _ = compareTestReports("base", "candidate", []testRunReceipt{a}, []testRunReceipt{b})
	if c.ObjectStatus != "inconclusive" {
		t.Fatal("cleanup failure passed")
	}
	client := &objectClientTest{cleanupFailure: true}
	session := &testObjectSession{created: true}
	session.cleanup(t.Context(), client, &a)
	if a.Objects.CleanupConfirmed || a.Objects.Status != "incomplete" {
		t.Fatal("cleanup receipt remained complete")
	}
}
func TestObjectFixturesAreCapturedAsTrustedRehearsalInputs(t *testing.T) {
	o, _ := rehearsalTestRepo(t)
	body, e := os.ReadFile(o.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	body = append(body, []byte("    actions:\n      webhooks: capture\n      network: contained\n      simulation:\n        version: 1\n        fixtures:\n          - workload: rehearsal-app\n            name: notify\n            integration: email\n            adapter: resend\n            method: POST\n            path: /emails\n            responses: [{status: 200}]\n    requests: [{name: health, method: GET, path: /, expect: {status: 200}}]\n    command_execution: {version: 1, runtime: contained}\n    objects_fixture:\n      version: 1\n      bucket: imports\n      fixtures: [{key: seed, file: seed.bin}]\n")...)
	if e = os.WriteFile(o.Manifest, body, 0o600); e != nil {
		t.Fatal(e)
	}
	fixture := filepath.Join(filepath.Dir(o.Manifest), "seed.bin")
	if e = os.WriteFile(fixture, []byte("trusted object"), 0o600); e != nil {
		t.Fatal(e)
	}
	trusted := filepath.Join(t.TempDir(), "trusted")
	if e = os.Mkdir(trusted, 0o700); e != nil {
		t.Fatal(e)
	}
	session := testRehearsalSession{repo: o.Repo, trusted: trusted}
	if e = session.captureInputs(o); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(fixture, []byte("candidate changed"), 0o600); e != nil {
		t.Fatal(e)
	}
	relative, e := filepath.Rel(o.Repo, fixture)
	if e != nil {
		t.Fatal(e)
	}
	pinned, e := os.ReadFile(filepath.Join(trusted, relative))
	if e != nil || string(pinned) != "trusted object" {
		t.Fatal("object fixture was not pinned independently of source revision")
	}
}
