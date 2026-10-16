package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

type campaignFaultHistoryFixture struct {
	root      string
	dirs      []string
	plan      testCampaignPolicyPlan
	cp        testCampaignCheckpoint
	scenarios map[string]testScenario
	policy    testCampaignPolicy
}

func newCampaignFaultHistoryFixture(t *testing.T) campaignFaultHistoryFixture {
	t.Helper()
	f := campaignFaultHistoryFixture{root: t.TempDir()}
	plan, scenarios, policy := campaignRunnerTestPlan(t, 1)
	plan.Costs.Budget = plan.Costs.Estimated
	scenario := scenarios["checkout"]
	scenario.Consumers = []testConsumer{{Name: "customer"}}
	scenarios["checkout"] = scenario
	plan.Execution.ManifestSHA256 = testCampaignJSONHash(scenarios)
	manifestPath := filepath.Join(f.root, "manifest.yaml")
	body := []byte("version: 1\nscenarios:\n  checkout:\n    project: checkout\n    command: [must-never-run]\n    consumers: [{name: customer}]\n")
	if err := os.WriteFile(manifestPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	document, _, err := readTestManifestDocument(manifestPath, func(string, testScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	scenarios = document.Scenarios
	scenario = scenarios["checkout"]
	plan.Execution.ManifestSHA256 = testCampaignJSONHash(scenarios)
	policyBody, err := yaml.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(f.root, "policy.yaml")
	if err := os.WriteFile(policyPath, policyBody, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err = readTestCampaignPolicy(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	plan.Execution.PolicySHA256 = testCampaignJSONHash(policy)
	f.scenarios, f.policy = scenarios, policy
	var previous testCampaignCheckpoint
	for i := 0; i < 3; i++ {
		dir := filepath.Join(f.root, fmt.Sprintf("generation-%d", i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			plan.Recovery = &testCampaignRecoveryLink{Allocation: plan.Costs.Budget, Remaining: plan.Costs.Budget, ParentDirectory: f.dirs[i-1], ParentPlanSHA256: previous.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(previous), ReconciliationSHA256: strings.Repeat(fmt.Sprint(i), 64), Side: "current", Revision: strings.Repeat("a", 40)}
			mustCampaignFaultJSON(t, filepath.Join(f.dirs[i-1], testCampaignRecoverySealName), testCampaignRecoverySeal{SchemaVersion: 1, ParentPlanSHA256: previous.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(previous), ReconciliationSHA256: plan.Recovery.ReconciliationSHA256, ContinuationSHA256: testCampaignJSONHash(plan)})
			mustCampaignFaultJSON(t, filepath.Join(f.dirs[i-1], testCampaignContinuationOwnerName), testCampaignContinuationOwner{SchemaVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Directory: dir, HostSHA256: testCampaignBundleHost()})
		}
		cp := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Side: "current", Revision: strings.Repeat("a", 40), Recovery: plan.Recovery, Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "running"}}}
		mustCampaignFaultJSON(t, filepath.Join(dir, "checkpoint.json"), cp)
		journal, err := prepareTestCampaignJournal(dir, plan.Batches[0])
		if err != nil {
			t.Fatal(err)
		}
		writer, err := newTestCampaignRunJournal(journal, "checkout", "warm", strings.Repeat(fmt.Sprint(i+1), 32), scenario)
		if err != nil {
			t.Fatal(err)
		}
		app := fmt.Sprintf("app-%d", i)
		if err := writer.update("checkout", app, "provisioned"); err != nil {
			t.Fatal(err)
		}
		if err := writer.registration("registered"); err != nil {
			t.Fatal(err)
		}
		if err := writer.consumer("customer", testCampaignFaultConsumer{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), AppID: app, AppSlug: fmt.Sprintf("preview-%d", i), ExternalRef: "gregale-test-" + writer.journal.RunID + "-customer", ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		cp.Halted = true
		cp.Batches["a"] = testCampaignCheckpointBatch{Status: "uncertain"}
		mustCampaignFaultJSON(t, filepath.Join(dir, "checkpoint.json"), cp)
		f.dirs = append(f.dirs, dir)
		previous = cp
	}
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err != nil {
		t.Fatal(err)
	}
	f.plan, f.cp = plan, previous
	return f
}
func mustCampaignFaultJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeTestCampaignJSON(path, value); err != nil {
		t.Fatal(err)
	}
}
func campaignFaultFrozenBytes(t *testing.T, dirs []string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, dir := range dirs {
		if err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[path] = body
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func assertCampaignFaultFrozen(t *testing.T, before map[string][]byte, dirs []string) {
	t.Helper()
	after := campaignFaultFrozenBytes(t, dirs)
	if len(before) != len(after) {
		t.Fatal("frozen evidence membership changed")
	}
	for path, body := range before {
		if !bytes.Equal(body, after[path]) {
			t.Fatalf("frozen evidence rewritten: %s", path)
		}
	}
}

type campaignFaultHistoryAPI struct {
	registrations                map[string]map[string]string
	sessions                     map[string]string
	consumers                    map[string]api.APIConsumerResponse
	failRun                      string
	revokes, deletes, namespaces int
	onRead                       func()
}

func newCampaignFaultHistoryAPI(t *testing.T, f campaignFaultHistoryFixture) *campaignFaultHistoryAPI {
	t.Helper()
	client := &campaignFaultHistoryAPI{registrations: map[string]map[string]string{}, sessions: map[string]string{}, consumers: map[string]api.APIConsumerResponse{}}
	for i, dir := range f.dirs {
		j, err := readTestCampaignFaultJournals(dir, "a", f.scenarios["checkout"])
		if err != nil {
			t.Fatal(err)
		}
		run := j[0].RunID
		client.registrations[run] = map[string]string{"checkout": fmt.Sprintf("app-%d", i)}
		client.sessions["checkout/"+run] = fmt.Sprintf("app-%d", i)
		c := j[0].Faults.Consumers["customer"]
		client.consumers[c.ID] = api.APIConsumerResponse{ID: c.ID, AppID: c.AppID, ExternalRef: c.ExternalRef, Status: "active"}
	}
	return client
}
func faultHistoryNotFound() error { return &api.APIError{Problem: api.Problem{Status: 404}} }
func (c *campaignFaultHistoryAPI) GetScenarioTestRegistration(_ context.Context, run string) (api.ScenarioTestRegistration, error) {
	if c.onRead != nil {
		fn := c.onRead
		c.onRead = nil
		fn()
	}
	if c.failRun == run {
		return api.ScenarioTestRegistration{}, errors.New("lookup unavailable")
	}
	return api.ScenarioTestRegistration{SchemaVersion: 1, Members: c.registrations[run]}, nil
}
func (c *campaignFaultHistoryAPI) DeleteScenarioTestRegistration(_ context.Context, run, digest string) error {
	if api.ScenarioTestRegistrationSHA256(c.registrations[run]) != digest {
		return errors.New("changed namespace")
	}
	if _, ok := c.sessions["checkout/"+run]; ok {
		return errors.New("session still live")
	}
	c.namespaces++
	c.registrations[run] = map[string]string{}
	return nil
}
func (c *campaignFaultHistoryAPI) GetAPIConsumer(_ context.Context, _ string, id string) (api.APIConsumerResponse, error) {
	v, ok := c.consumers[id]
	if !ok {
		return v, faultHistoryNotFound()
	}
	return v, nil
}
func (c *campaignFaultHistoryAPI) RevokeAPIConsumer(_ context.Context, _ string, id string) (api.APIConsumerResponse, error) {
	c.revokes++
	v := c.consumers[id]
	v.Status = "revoked"
	c.consumers[id] = v
	return v, nil
}
func (c *campaignFaultHistoryAPI) GetDevSession(_ context.Context, project, run string) (api.DevSessionResponse, error) {
	var v api.DevSessionResponse
	id, ok := c.sessions[project+"/"+run]
	if !ok {
		return v, faultHistoryNotFound()
	}
	v.App.ID = id
	return v, nil
}
func (c *campaignFaultHistoryAPI) DestroyDevSession(_ context.Context, project string, run ...string) error {
	if len(run) != 1 || run[0] == "" {
		return errors.New("unscoped deletion")
	}
	delete(c.sessions, project+"/"+run[0])
	c.deletes++
	return nil
}

func TestCampaignFaultHistoryCleanupAcrossGenerations(t *testing.T) {
	// adr: 984 — the active continuation cleans exact ancestor identities without changing frozen evidence.
	f := newCampaignFaultHistoryFixture(t)
	client := newCampaignFaultHistoryAPI(t, f)
	dir := f.dirs[2]
	before := campaignFaultFrozenBytes(t, f.dirs[:2])
	inspected, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, dir, nil, false)
	if err != nil || len(inspected.Sources) != 3 || client.deletes+client.revokes+client.namespaces != 0 {
		t.Fatal("inspection changed resources", err)
	}
	for i := 0; i < 2; i++ {
		result, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, dir, nil, true)
		if err != nil || !result.Complete || len(result.Faults) != 3 {
			t.Fatal("history cleanup failed", err)
		}
	}
	if client.deletes != 3 || client.revokes != 3 || client.namespaces != 3 {
		t.Fatalf("cleanup was unscoped or not idempotent: %+v", client)
	}
	assertCampaignFaultFrozen(t, before, f.dirs[:2])
	completion := testCampaignRecoveryCompletion{ResourceEvidenceComplete: true, KnownSessionsAbsent: true}
	applyTestCampaignFaultCompletion(context.Background(), &completion, client, f.plan, f.cp, f.scenarios, dir, true)
	if !completion.FaultResourcesVerified || !completion.FaultResourcesAbsent || len(completion.FaultSources) != 3 || completion.RecoveryComplete || completion.TestExecutionComplete {
		t.Fatal("full history did not retain execution uncertainty", completion)
	}
	client.sessions["checkout/"+strings.Repeat("1", 32)] = "replacement"
	applyTestCampaignFaultCompletion(context.Background(), &completion, client, f.plan, f.cp, f.scenarios, dir, true)
	if completion.KnownSessionsAbsent {
		t.Fatal("live ancestor session certified absent")
	}
}

func TestCampaignFaultHistoryBlocksBeforeMutation(t *testing.T) {
	// adr: 984 — one unsafe generation blocks the whole mutation preflight.
	for _, kind := range []string{"replacement", "lookup", "lock", "owner", "legacy", "missing", "seal", "orphan", "duplicate-run", "changed-during-read"} {
		t.Run(kind, func(t *testing.T) {
			f := newCampaignFaultHistoryFixture(t)
			client := newCampaignFaultHistoryAPI(t, f)
			switch kind {
			case "replacement":
				client.registrations[strings.Repeat("1", 32)]["checkout"] = "replacement"
			case "lookup":
				client.failRun = strings.Repeat("1", 32)
			case "lock":
				if err := os.WriteFile(filepath.Join(f.dirs[0], "runner.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "owner":
				mustCampaignFaultJSON(t, filepath.Join(f.dirs[0], testCampaignContinuationOwnerName), testCampaignContinuationOwner{Directory: "other"})
			case "legacy":
				path := filepath.Join(f.dirs[0], "batch-a.journal", strings.Repeat("1", 32)+".json")
				var j testCampaignRunJournal
				_ = readTestCampaignJSON(path, &j)
				j.Faults = nil
				mustCampaignFaultJSON(t, path, j)
			case "missing":
				if err := os.RemoveAll(filepath.Join(f.dirs[0], "batch-a.journal")); err != nil {
					t.Fatal(err)
				}
			case "seal":
				mustCampaignFaultJSON(t, filepath.Join(f.dirs[0], testCampaignRecoverySealName), testCampaignRecoverySeal{})
			case "orphan":
				if err := os.Mkdir(filepath.Join(f.dirs[0], "batch-orphan.journal"), 0700); err != nil {
					t.Fatal(err)
				}
			case "duplicate-run":
				oldPath := filepath.Join(f.dirs[0], "batch-a.journal", strings.Repeat("1", 32)+".json")
				var j testCampaignRunJournal
				_ = readTestCampaignJSON(oldPath, &j)
				j.RunID = strings.Repeat("3", 32)
				c := j.Faults.Consumers["customer"]
				c.ExternalRef = "gregale-test-" + j.RunID + "-customer"
				j.Faults.Consumers["customer"] = c
				if err := os.Remove(oldPath); err != nil {
					t.Fatal(err)
				}
				mustCampaignFaultJSON(t, filepath.Join(f.dirs[0], "batch-a.journal", j.RunID+".json"), j)
			case "changed-during-read":
				client.onRead = func() {
					path := filepath.Join(f.dirs[0], "batch-a.journal", strings.Repeat("1", 32)+".json")
					var j testCampaignRunJournal
					_ = readTestCampaignJSON(path, &j)
					j.UpdatedAt = j.UpdatedAt.Add(time.Second)
					mustCampaignFaultJSON(t, path, j)
				}
			}
			_, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, f.dirs[2], nil, true)
			if err == nil || client.revokes+client.deletes+client.namespaces != 0 {
				t.Fatal("unsafe history mutated resources", err)
			}
		})
	}
}

func TestCampaignFaultHistoryPortableWithoutOriginalPaths(t *testing.T) {
	// adr: 984 — repeated handoff uses verified bundle nodes and never reopens sender paths.
	f := newCampaignFaultHistoryFixture(t)
	client := newCampaignFaultHistoryAPI(t, f)
	bundle, err := collectTestCampaignBundle(f.plan, f.cp, f.policy, f.scenarios, f.dirs[2], "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "recipient")
	index, err := importTestCampaignBundle(bundle, root, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(filepath.Join(root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(index.Root, "work")
	frozen := campaignFaultFrozenBytes(t, []string{filepath.Join(root, "artifacts")})
	result, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, work, resolver, true)
	if err != nil || !result.Complete || len(result.Sources) != 3 {
		t.Fatal("portable history cleanup failed", err)
	}
	assertCampaignFaultFrozen(t, frozen, []string{filepath.Join(root, "artifacts")})
	nextBundle, err := collectTestCampaignBundle(f.plan, f.cp, f.policy, f.scenarios, work, "", "", "", resolver)
	if err != nil {
		t.Fatal("re-export imported ancestry", err)
	}
	if err := revokeTestCampaignHandoff(resolver, nextBundle.ID); err != nil {
		t.Fatal(err)
	}
	nextRoot := filepath.Join(t.TempDir(), "next-recipient")
	_, err = importTestCampaignBundle(nextBundle, nextRoot, true, filepath.Dir(index.ClaimPath))
	if err != nil {
		t.Fatal(err)
	}
	nextResolver, err := loadTestCampaignBundleResolver(filepath.Join(nextRoot, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	result, err = cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, filepath.Join(nextRoot, "work"), nextResolver, true)
	if err != nil || !result.Complete || len(result.Sources) != 3 {
		t.Fatal("second portable handoff failed", err)
	}
	index = nextResolver.Index
	resolver = nextResolver
	work = filepath.Join(nextRoot, "work")
	extra := filepath.Join(nextRoot, "artifacts", "nodes", "2", "batch-a.journal", strings.Repeat("f", 32)+".json")
	if err := os.WriteFile(extra, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeMutations := client.revokes + client.deletes + client.namespaces
	if _, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, work, resolver, true); err == nil || client.revokes+client.deletes+client.namespaces != beforeMutations {
		t.Fatal("unindexed ancestor snapshot gained authority")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	var claim testCampaignBundleClaim
	_ = readTestCampaignJSON(index.ClaimPath, &claim)
	claim.Transferred = "another-owner"
	mustCampaignFaultJSON(t, index.ClaimPath, claim)
	if _, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, work, resolver, true); err == nil {
		t.Fatal("transferred claim regained cleanup authority")
	}
}

func TestCampaignFaultHistoryCLIUsesActiveOwner(t *testing.T) {
	// adr: 984 — the public cleanup flags retain active-owner locks and never run fixtures.
	f := newCampaignFaultHistoryFixture(t)
	client := newCampaignFaultHistoryAPI(t, f)
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer history-token" {
			t.Error("unexpected authentication")
			w.WriteHeader(401)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		var out any
		var err error
		switch {
		case len(parts) == 5 && parts[0] == "v1" && parts[1] == "apps" && parts[3] == "consumers":
			if r.Method == "GET" {
				out, err = client.GetAPIConsumer(r.Context(), parts[2], parts[4])
			} else if r.Method == "DELETE" {
				out, err = client.RevokeAPIConsumer(r.Context(), parts[2], parts[4])
			} else {
				err = errors.New("unexpected consumer mutation")
			}
		case len(parts) == 4 && parts[0] == "v1" && parts[1] == "dev" && parts[2] == "sessions":
			run := r.URL.Query().Get("workspace_id")
			if r.Method == "GET" {
				out, err = client.GetDevSession(r.Context(), parts[3], run)
			} else if r.Method == "DELETE" {
				err = client.DestroyDevSession(r.Context(), parts[3], run)
			} else {
				err = errors.New("unexpected provisioning")
			}
		case len(parts) >= 4 && parts[0] == "v1" && parts[1] == "dev" && parts[2] == "test-runs":
			if len(parts) == 4 && r.Method == "GET" {
				out, err = client.GetScenarioTestRegistration(r.Context(), parts[3])
			} else if len(parts) == 5 && parts[4] == "registration" && r.Method == "DELETE" {
				err = client.DeleteScenarioTestRegistration(r.Context(), parts[3], r.URL.Query().Get("members_sha256"))
			} else {
				err = errors.New("unscoped namespace mutation")
			}
		default:
			err = errors.New("unexpected history API request")
		}
		if err != nil {
			if isNotFound(err) {
				w.WriteHeader(404)
			} else {
				t.Error(err)
				w.WriteHeader(409)
			}
			return
		}
		if out == nil {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "history-token")
	planPath := filepath.Join(f.root, "plan.json")
	mustCampaignFaultJSON(t, planPath, f.plan)
	before := campaignFaultFrozenBytes(t, f.dirs[:2])
	args := []string{"--plan", planPath, "--policy", filepath.Join(f.root, "policy.yaml"), "--manifest", filepath.Join(f.root, "manifest.yaml"), "--directory", f.dirs[2], "--journal", "--faults", "--fault-history"}
	if code := cmdTestCampaignControl("cleanup", args); code != 0 {
		t.Fatalf("history inspection: %d", code)
	}
	if client.revokes+client.deletes+client.namespaces != 0 {
		t.Fatal("CLI inspection mutated resources")
	}
	if code := cmdTestCampaignControl("cleanup", append(args, "--execute")); code != 0 {
		t.Fatalf("history execution: %d", code)
	}
	if client.revokes != 3 || client.deletes != 3 || client.namespaces != 3 {
		t.Fatal("CLI did not clean all generations")
	}
	assertCampaignFaultFrozen(t, before, f.dirs[:2])
}
