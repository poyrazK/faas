package bindingrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

type contractCase struct {
	Name        string
	Compact     bool
	Graph       bool
	Actions     []string
	Concurrency int
	Fault       string
	Resume      bool
	Passed      bool
	Admits      int
}
type fakeClient struct {
	Client
	scenario          contractCase
	root              snapshot
	admits, maxActive int
	lost              bool
	calls             []string
}

func detached(s snapshot) snapshot {
	data, _ := json.Marshal(s.app)
	if s.app != nil {
		var r api.BindingVerificationRun
		_ = json.Unmarshal(data, &r)
		return snapshot{app: &r}
	}
	data, _ = json.Marshal(s.graph)
	var r api.ProjectBindingVerificationRun
	_ = json.Unmarshal(data, &r)
	return snapshot{graph: &r}
}
func newFake(scenario contractCase) *fakeClient {
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	expires := created.Add(time.Minute)
	if scenario.Fault == "expired" {
		created = created.Add(-2 * time.Minute)
		expires = created.Add(time.Minute)
	}
	member := func(name string, n int) api.BindingVerificationRun {
		return api.BindingVerificationRun{ID: fmt.Sprintf("11111111-1111-4111-8111-%012d", n), App: name, DeploymentID: "22222222-2222-4222-8222-222222222222", Scope: "production", Status: "pending", CreatedAt: created, ExpiresAt: expires, ReviewDigest: strings.Repeat("a", 64), QueuePublish: true, Policy: api.BindingReleasePolicy{AppID: "33333333-3333-4333-8333-333333333333", Scope: "production", Mode: "enforce", Revision: 1, MaxVerificationAge: "5m"}, Inventory: api.AppBindingInventory{App: name, GeneratedAt: created, Complete: true, Bindings: []api.AppBindingInventoryItem{}}, Steps: []api.BindingVerificationRunStep{}}
	}
	a, b := member("api", 1), member("billing", 2)
	if scenario.Compact {
		a.DeploymentID = strings.ReplaceAll(a.DeploymentID, "-", "")
		b.DeploymentID = strings.ReplaceAll(b.DeploymentID, "-", "")
	}
	f := &fakeClient{scenario: scenario}
	if scenario.Graph {
		id := "99999999-9999-4999-8999-999999999999"
		a.GraphRunID = id
		b.GraphRunID = id
		f.root = snapshot{graph: &api.ProjectBindingVerificationRun{ID: id, Project: "project", Environment: "production", Status: "pending", CreatedAt: created, ExpiresAt: expires, ReviewDigest: strings.Repeat("c", 64), QueuePublish: true, Manifest: api.CheckedProjectReleaseManifest{Project: "project", Environment: "production", TTLSeconds: 60, Deployments: map[string]string{"api": canonicalDeploymentID(a.DeploymentID), "billing": canonicalDeploymentID(b.DeploymentID)}, ExpectedActiveReleaseID: new(string), ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{"api": 1, "billing": 1}, ServiceTargets: map[string]map[string]string{"api": {}, "billing": {}}}, Members: map[string]api.BindingVerificationRun{"api": a, "billing": b}}}
	} else {
		f.root = snapshot{app: &a}
	}
	for i, action := range scenario.Actions {
		name := "api"
		if scenario.Graph && i%2 == 1 {
			name = "billing"
		}
		r := a
		if f.root.graph != nil {
			r = f.root.graph.Members[name]
		} else {
			r = *f.root.app
		}
		kind := "service"
		if action == "queue-canary" || action == "wait-queue" {
			kind = "queue"
		}
		step := api.BindingVerificationRunStep{Plan: api.BindingVerificationPlanStep{Type: kind, Name: fmt.Sprintf("binding-%d", i), Binding: fmt.Sprintf("binding-%d", i), Scope: "production", Action: action}, Status: "planned"}
		if action == "wait" || action == "wait-queue" {
			step.ResourceID = fmt.Sprintf("44444444-4444-4444-8444-%012d", i+100)
			step.ResourceKind = expectedKind(step)
			step.Status = "queued"
			if kind == "queue" {
				step.Status = "published"
			}
		}
		r.Steps = append(r.Steps, step)
		if f.root.graph != nil {
			f.root.graph.Members[name] = r
		} else {
			*f.root.app = r
		}
	}
	return f
}
func (f *fakeClient) Read(poll bool) snapshot {
	f.calls = append(f.calls, "resume")
	if !poll {
		return detached(f.root)
	}
	f.calls[len(f.calls)-1] = "get"
	for _, r := range f.root.members() {
		for i, step := range r.Steps {
			if step.ResourceID != "" {
				step.Status = "succeeded"
				if f.scenario.Fault == "failed" {
					step.Status = "failed"
				}
				if f.scenario.Fault == "resource_changed" {
					step.ResourceID = "77777777-7777-4777-8777-777777777777"
				}
				f.root.setStep(r.App, i, step)
			}
		}
	}
	if f.scenario.Fault == "blocked" {
		if f.root.app != nil {
			f.root.app.Status = "blocked"
		} else {
			f.root.graph.Status = "blocked"
		}
	}
	if f.scenario.Fault == "digest_changed" {
		if f.root.app != nil {
			f.root.app.ReviewDigest = strings.Repeat("b", 64)
		} else {
			f.root.graph.ReviewDigest = strings.Repeat("b", 64)
		}
	}
	if f.scenario.Fault == "deadline_changed" {
		if f.root.app != nil {
			f.root.app.ExpiresAt = f.root.app.ExpiresAt.Add(time.Second)
		} else {
			f.root.graph.ExpiresAt = f.root.graph.ExpiresAt.Add(time.Second)
			for name, r := range f.root.graph.Members {
				r.ExpiresAt = f.root.graph.ExpiresAt
				f.root.graph.Members[name] = r
			}
		}
	}
	if f.scenario.Fault == "selection_changed" {
		target := map[string]string{"billing": "22222222-2222-4222-8222-222222222222"}
		f.root.graph.Manifest.ServiceTargets["api"] = target
		r := f.root.graph.Members["api"]
		r.Inventory.ServiceTargets = target
		f.root.graph.Members["api"] = r
	}
	return detached(f.root)
}
func (f *fakeClient) ResumeBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error) {
	return *f.Read(false).app, nil
}
func (f *fakeClient) GetBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error) {
	return *f.Read(true).app, nil
}
func (f *fakeClient) ResumeProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error) {
	return *f.Read(false).graph, nil
}
func (f *fakeClient) GetProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error) {
	return *f.Read(true).graph, nil
}
func (f *fakeClient) AdmitBindingVerificationRunStep(_ context.Context, app, id string, index int) (api.BindingVerificationRunStep, error) {
	f.calls = append(f.calls, "admit")
	if (f.scenario.Fault == "budget_once" || f.scenario.Fault == "budget_after_admit" && f.admits == 1) && !f.lost {
		f.lost = true
		return api.BindingVerificationRunStep{}, fmt.Errorf("admission: %w", &api.APIError{Problem: *api.NewProblem(429, "binding_verification_budget_full", "Budget full", "Wait")})
	}
	if f.scenario.Fault == "capacity" {
		return api.BindingVerificationRunStep{}, &api.APIError{Problem: *api.NewProblem(429, "binding_verification_run_limit", "Capacity", "Stop")}
	}
	f.admits++
	var selected api.BindingVerificationRun
	for _, member := range f.root.members() {
		if member.App == app && member.ID == id {
			selected = member
		}
	}
	step := selected.Steps[index]
	if step.ResourceID != "" {
		return step, errors.New("runner attempted replacement admission")
	}
	step.ResourceID = fmt.Sprintf("55555555-5555-4555-8555-%012d", f.admits)
	step.ResourceKind = expectedKind(step)
	step.Owned = true
	step.Status = "queued"
	if step.ResourceKind == "queue" {
		step.Status = "published"
	}
	if f.scenario.Fault == "unknown_status" {
		step.Status = "unknown"
	}
	f.root.setStep(app, index, step)
	active := 0
	for _, r := range f.root.members() {
		for _, s := range r.Steps {
			if s.ResourceID != "" && s.Status != "succeeded" {
				active++
			}
		}
	}
	f.maxActive = max(f.maxActive, active)
	if f.scenario.Fault == "admit_once" && !f.lost {
		f.lost = true
		return api.BindingVerificationRunStep{}, errors.New("admission response lost after commit")
	}
	return step, nil
}
func (f *fakeClient) Complete() (snapshot, error) {
	f.calls = append(f.calls, "complete")
	for _, r := range f.root.members() {
		for _, step := range r.Steps {
			if step.ResourceID != "" && step.Status != "succeeded" {
				return f.root, errors.New("completion before terminal success")
			}
		}
	}
	if f.root.app != nil {
		f.root.app.Status = "passed"
	} else {
		f.root.graph.Status = "passed"
		for name, r := range f.root.graph.Members {
			r.Status = "passed"
			if f.scenario.Fault == "member_pending" && name == "billing" {
				r.Status = "pending"
			}
			f.root.graph.Members[name] = r
		}
	}
	if f.scenario.Fault == "false_complete" {
		if f.root.app != nil {
			f.root.app.Status = "blocked"
		} else {
			f.root.graph.Status = "blocked"
		}
	}
	if f.scenario.Fault == "complete_once" && !f.lost {
		f.lost = true
		return snapshot{}, errors.New("completion response lost after commit")
	}
	return detached(f.root), nil
}
func (f *fakeClient) CompleteBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error) {
	s, err := f.Complete()
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	return *s.app, nil
}
func (f *fakeClient) CompleteProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error) {
	s, err := f.Complete()
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	return *s.graph, nil
}
func runFake(ctx context.Context, f *fakeClient, o Options) (string, error) {
	if f.root.graph != nil {
		r, err := RunProject(ctx, f, "project", "production", f.root.id(), o)
		return r.Status, err
	}
	r, err := RunApp(ctx, f, "api", f.root.id(), o)
	return r.Status, err
}

func TestRunnerSharedRecoveryContracts(t *testing.T) {
	data, err := os.ReadFile("../../../binding-runner-tests/contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct{ Cases []contractCase }
	if err = json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range suite.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			f := newFake(scenario)
			deadline := f.root.expires()
			events := []Progress{}
			options := Options{PollInterval: time.Millisecond, Concurrency: scenario.Concurrency, OnProgress: func(p Progress) error { events = append(events, p); return nil }}
			status, err := runFake(testContext(t), f, options)
			if scenario.Resume {
				var recovery *RecoveryError
				if !errors.As(err, &recovery) || recovery.RunID != f.root.id() {
					t.Fatalf("lost response did not preserve root identity: %v", err)
				}
				status, err = runFake(testContext(t), f, options)
				if !f.root.expires().Equal(deadline) {
					t.Fatal("resume renewed original expiry")
				}
			}
			if scenario.Passed != (err == nil && status == "passed") {
				t.Fatalf("status=%s err=%v", status, err)
			}
			if err != nil {
				var recovery *RecoveryError
				if !errors.As(err, &recovery) || recovery.RunID != f.root.id() {
					t.Fatalf("missing recovery identity: %v", err)
				}
			}
			if f.admits != scenario.Admits {
				t.Fatalf("admissions=%d want=%d calls=%v", f.admits, scenario.Admits, f.calls)
			}
			limit := scenario.Concurrency
			if limit == 0 {
				limit = 2
			}
			if f.maxActive > limit {
				t.Fatalf("global budget exceeded: %d", f.maxActive)
			}
			if scenario.Passed {
				found := false
				for _, event := range events {
					found = found || event.Step == -1 && event.Status == "passed"
				}
				if !found {
					t.Fatalf("missing qualified progress: %v", events)
				}
			}
		})
	}
}
func TestRunnerLocalStopsPreserveWork(t *testing.T) {
	for _, callbackError := range []bool{false, true} {
		t.Run(fmt.Sprint(callbackError), func(t *testing.T) {
			f := newFake(contractCase{Graph: true, Actions: []string{"probe", "queue-canary"}})
			ctx, cancel := context.WithCancel(testContext(t))
			defer cancel()
			sentinel := errors.New("persist progress failed")
			_, err := runFake(ctx, f, Options{PollInterval: time.Millisecond, OnProgress: func(p Progress) error {
				if p.ResourceID != "" {
					if callbackError {
						return sentinel
					}
					cancel()
				}
				return nil
			}})
			want := error(context.Canceled)
			if callbackError {
				want = sentinel
			}
			if !errors.Is(err, want) || f.admits != 1 {
				t.Fatalf("stopping replaced or lost work: %v admits=%d", err, f.admits)
			}
			if f.root.graph.Members["api"].Steps[0].ResourceID == "" || f.root.graph.Status != "pending" {
				t.Fatal("stop cancelled server work")
			}
		})
	}
}

type inFlightClient struct {
	*fakeClient
	committed chan struct{}
}

func (c *inFlightClient) AdmitBindingVerificationRunStep(ctx context.Context, app, id string, index int) (api.BindingVerificationRunStep, error) {
	step, err := c.fakeClient.AdmitBindingVerificationRunStep(ctx, app, id, index)
	if err != nil {
		return step, err
	}
	close(c.committed)
	<-ctx.Done()
	return api.BindingVerificationRunStep{}, ctx.Err()
}
func TestRunnerStopDuringAdmissionResumesCommittedResource(t *testing.T) {
	f := newFake(contractCase{Actions: []string{"probe"}})
	c := &inFlightClient{fakeClient: f, committed: make(chan struct{})}
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunApp(ctx, c, "api", f.root.id(), Options{PollInterval: time.Millisecond})
		done <- err
	}()
	select {
	case <-c.committed:
	case <-time.After(time.Second):
		t.Fatal("admission did not commit")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight stop lost context: %v", err)
	}
	deadline := f.root.expires()
	status, err := runFake(testContext(t), f, Options{PollInterval: time.Millisecond})
	if err != nil || status != "passed" || f.admits != 1 || !f.root.expires().Equal(deadline) {
		t.Fatalf("committed admission replaced after stop: %s %v admits=%d", status, err, f.admits)
	}
}
