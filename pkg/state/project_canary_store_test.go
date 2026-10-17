package state

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

type canaryStoreFixture struct {
	store                               Store
	account, project, attempt, baseline string
	apps                                []App
	old                                 []ProjectReleaseMember
	previous                            ProjectEnvironmentConfig
}

func newCanaryStoreFixture(t *testing.T, store Store) canaryStoreFixture {
	return newCanaryStoreFixtureWithWorker(t, store, false)
}
func newCanaryStoreFixtureWithWorker(t *testing.T, store Store, worker bool) canaryStoreFixture {
	t.Helper()
	ctx := t.Context()
	a, err := store.CreateAccount(ctx, "canary@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: a.ID, Slug: "canary"})
	if err != nil {
		t.Fatal(err)
	}
	f := canaryStoreFixture{store: store, account: a.ID, project: project.ID, attempt: strings.Repeat("c", 32)}
	for _, name := range []string{"api", "web"} {
		manifest := AppManifest{RevisionPinTTLSeconds: 3600}
		class := WorkloadClassHTTP
		if worker && name == "web" {
			manifest.ExecutionMode = api.ExecutionModeWorker
			class = WorkloadClassWorker
		}
		app, err := store.CreateApp(ctx, App{AccountID: a.ID, ProjectID: project.ID, Slug: "canary-" + name, WorkloadName: name, WorkloadClass: class, RAMMB: 256, MaxConcurrency: 5, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "MODE", "old"); err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/old.ext4", "layers/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		f.apps = append(f.apps, app)
		f.old = append(f.old, ProjectReleaseMember{app.ID, d.ID})
	}
	values, hash, _ := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	f.previous, err = store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: a.ID, ProjectID: project.ID, EnvironmentSlug: "production", Values: values, ConfigHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, a.ID, project.ID, "production", 1800, f.old)
	if err != nil {
		t.Fatal(err)
	}
	f.baseline = base.ID
	request := json.RawMessage(`{"env":{"api":{"MODE":"new"},"web":{"MODE":"web-new"}}}`)
	cfg, err := store.(ProjectReleaseConfigurationStore).PreviewProjectReleaseConfiguration(ctx, a.ID, project.ID, base.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: base.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: cfg, Verification: &api.ProjectReleaseVerification{Progressive: true, Probes: []api.ProjectReleaseProbe{{AppID: f.apps[0].ID, Slug: f.apps[0].Slug, Method: "GET", Path: "/health"}}}}
	if worker {
		_, err := store.(QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, QueueBinding{AccountID: a.ID, AppID: f.apps[1].ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: WorkloadClassWorker, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		safe.Worker, err = store.(ProjectReleaseWorkerStore).PreviewProjectReleaseWorker(ctx, a.ID, project.ID, base.ID)
		if err != nil {
			t.Fatal(err)
		}
		safe.Verification.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
	}
	receipt := api.ProjectSubmissionReceipt{AttemptID: f.attempt, ProjectSlug: "canary", Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
	subs := store.(ProjectSubmissionStore)
	if _, err = subs.ReserveProjectSubmission(ctx, a.ID, f.attempt, strings.Repeat("a", 64), receipt); err != nil {
		t.Fatal(err)
	}
	if err = store.(ProjectReleaseConfigurationStore).StageProjectReleaseConfiguration(ctx, a.ID, f.attempt, project.ID, safe, request); err != nil {
		t.Fatal(err)
	}
	for _, app := range f.apps {
		now := time.Now()
		stage, _ := json.Marshal(StageState{Current: StageReadiness, CurrentStartedAt: &now})
		candidateCtx := ContextWithProjectReleaseConfigurationAttempt(ContextWithDeploymentSubmission(ctx, ProjectWorkloadAttempt(f.attempt, app.ID)), f.attempt)
		if worker && app.ID == f.apps[1].ID {
			candidateCtx = ContextWithProjectReleaseWorkerAttempt(candidateCtx, f.attempt)
		}
		d, err := store.CreateDeployment(candidateCtx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stage})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/candidate.ext4", "layers/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CloseDeploymentStage(ctx, d.ID, StageReadiness, time.Now()); err != nil {
			t.Fatal(err)
		}
		receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{AppID: app.ID, DeploymentID: d.ID, Slug: app.Slug, BuildID: "accepted"})
	}
	if err = subs.WriteProjectSubmission(ctx, a.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if worker {
		node, err := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		for i, depID := range []string{f.old[1].DeploymentID, receipt.Apply.Builds[1].DeploymentID} {
			ins, err := store.CreateInstanceWithMode(ctx, f.apps[1].ID, depID, string(StateColdBooting), 256, node.ID, uuid.NewString(), string(InstanceModeWorker))
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, a.ID, f.apps[1].ID, depID)
			if err != nil {
				t.Fatal(err)
			}
			fence, err := NewRuntimeAppConfigFence(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			inputs := RuntimeConfigInputs{Scope: "production", Boundary: time.Now(), Variables: map[string]string{"MODE": "old"}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
			if _, err = store.(RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, RuntimeInstancePublication{AccountID: a.ID, AppID: f.apps[1].ID, InstanceID: ins.ID, NodeID: node.ID, WakeID: ins.WakeID, ExpectedState: ins.State, Netns: "fc-" + ins.ID, HostIP: fmt.Sprintf("10.100.0.%d", 8+i), GuestUID: 20008 + i, Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = store.(ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, a.ID, f.attempt); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f canaryStoreFixture) progress(t *testing.T) *api.ProjectSafeRelease {
	t.Helper()
	r, err := f.store.(ProjectSubmissionStore).ProjectSubmission(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	return r.Apply.SafeRelease
}

// Only test clocks are moved. Policy identity, graph members, configuration
// version and observed generation remain the real stored transaction output.
func (f canaryStoreFixture) seedRound(t *testing.T) ProjectReleaseVerificationRound {
	t.Helper()
	safe := f.progress(t)
	p, err := f.store.(ProjectCanaryStore).ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-2 * time.Minute)
	if p.CheckedAt == nil {
		p.StageStartedAt = past
		p.Deadline = past.Add(api.ProjectReleaseVerificationTimeout)
	} else {
		p.CheckedAt = &past
		if p.StageStartedAt.After(past) {
			p.StageStartedAt = past.Add(-time.Minute)
			p.Deadline = p.StageStartedAt.Add(api.ProjectReleaseVerificationTimeout)
		}
	}
	syncCanaryVerification(safe, p)
	switch s := f.store.(type) {
	case *MemStore:
		s.mu.Lock()
		key := f.account + "\x00" + f.attempt
		c := s.projectCanaries[key]
		c.Policy = p
		s.projectCanaries[key] = c
		entry := s.projectSubmissions[key]
		var r api.ProjectSubmissionReceipt
		if err = json.Unmarshal(entry.Payload, &r); err != nil {
			t.Fatal(err)
		}
		r.Apply.SafeRelease = safe
		entry.Payload, _ = json.Marshal(r)
		s.projectSubmissions[key] = entry
		s.mu.Unlock()
	case *PgStore:
		raw, _ := json.Marshal(p)
		safeRaw, _ := json.Marshal(safe)
		if _, err = s.pool.Exec(t.Context(), "UPDATE project_release_canaries SET policy=$3::jsonb WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = s.pool.Exec(t.Context(), "UPDATE project_submissions SET payload=jsonb_set(payload,'{apply,safe_release}',$3::jsonb) WHERE account_id=$1 AND attempt_id=$2", f.account, f.attempt, safeRaw); err != nil {
			t.Fatal(err)
		}
	}
	return ProjectReleaseVerificationRound{Generation: p.Generation, PreviousCheckedAt: p.CheckedAt, Evidence: []api.ProjectReleaseProbeEvidence{{Probe: safe.Verification.Probes[0], CandidateRequests: 10, StableRequests: 10}}}
}

func (f canaryStoreFixture) advance(t *testing.T, regressed bool) *api.ProjectSafeRelease {
	t.Helper()
	round := f.seedRound(t)
	if regressed {
		round.Evidence[0].CandidateErrors = 1
	}
	safe, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round)
	if err != nil {
		t.Fatal(err)
	}
	return safe
}

func TestProjectCanaryStoreMemory(t *testing.T) {
	testProjectCanaryStore(t, func(*testing.T) Store { return NewMemStore() })
}

func TestProjectCanaryWorkersAndPinnedRetryMemory(t *testing.T) {
	testProjectCanaryWorkersAndPinnedRetry(t, NewMemStore())
}
func testProjectCanaryWorkersAndPinnedRetry(t *testing.T, store Store) {
	f := newCanaryStoreFixtureWithWorker(t, store, true)
	ctx := t.Context()
	safe := f.progress(t)
	if safe.Status != "verifying" || safe.Verification.Percent != 5 {
		t.Fatalf("worker staging=%+v", safe)
	}
	targets, err := store.(ProjectPinnedWorkerStore).PinnedProjectReleaseWorkers(ctx, f.apps[1].ID)
	if err != nil || len(targets) != 2 {
		t.Fatalf("cohort workers not eligible: %v %v", targets, err)
	}
	p, err := store.(ProjectCanaryStore).ProjectCanaryCheckpoint(ctx, f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	var inv Invocation
	for range 1000 {
		inv, err = store.EnqueueInvocation(ctx, Invocation{AccountID: f.account, AppID: f.apps[1].ID, Source: InvocationQueue, QueueBindingID: safe.Worker.BindingID, QueueName: "orders", DeploymentScope: "production", Payload: json.RawMessage(`{}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := SelectProjectCanaryRelease(p, inv.ID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if selected == p.CandidateReleaseID {
			break
		}
		if err = store.CancelInvocation(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		inv = Invocation{}
	}
	if inv.ID == "" {
		t.Fatal("no candidate cohort found")
	}
	inv, err = store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	_, version, err := ResolveInvocationVersion(ctx, store, inv)
	if err != nil || version.ReleaseID != safe.ReleaseID || version.DeploymentID != safe.Worker.DeploymentID {
		t.Fatalf("job routed outside cohort: %+v %v", version, err)
	}
	id, _, err := store.(ProjectReleaseSetStore).ResolveServiceRelease(ctx, f.apps[1].ID, version.DeploymentID, f.apps[0].ID, version.ReleaseID)
	if err != nil || id != version.ReleaseID {
		t.Fatalf("worker service call split graph: %s %v", id, err)
	}
	// An operator abort stops unassigned candidate admissions, while a retry
	// of an already pinned job continues with its immutable graph and config.
	if err = store.FailInvocation(ctx, inv.ID, "retry", time.Nanosecond, 3, WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account, ProjectID: f.project, EnvironmentSlug: "production", Values: f.previous.Values, ConfigHash: f.previous.ConfigHash}); err != nil {
		t.Fatal(err)
	}
	if f.advance(t, false).Status != "conflicted" {
		t.Fatal("operator change did not stop canary")
	}
	inv, err = store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	_, retry, err := ResolveInvocationVersion(ctx, store, inv)
	if err != nil || retry != version {
		t.Fatalf("aborted canary retargeted retry: %+v %v", retry, err)
	}
}
func testProjectCanaryStore(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"success", "abort", "rollback", "operator"} {
		t.Run(outcome, func(t *testing.T) {
			f := newCanaryStoreFixture(t, factory(t))
			ctx := t.Context()
			reader := f.store.(ProjectReleaseSetReader)
			canary := f.store.(ProjectCanaryStore)
			safe := f.progress(t)
			candidate := safe.ReleaseID
			if safe.Status != "verifying" || safe.Verification.Percent != 5 {
				t.Fatalf("staging=%+v", safe)
			}
			active, err := reader.ActiveProjectReleaseSet(ctx, f.account, f.project, "production")
			if err != nil || active.ID != f.baseline {
				t.Fatalf("stage changed default: %+v %v", active, err)
			}
			head, err := f.store.ProjectEnvironmentConfigLatest(ctx, f.account, f.project, "production")
			if err != nil || head.Version != f.previous.Version {
				t.Fatalf("stage changed config: %+v %v", head, err)
			}
			p, err := canary.ProjectCanaryCheckpoint(ctx, f.account, f.attempt)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"request-a", "request-b", "request-c"} {
				wanted, err := SelectProjectCanaryRelease(p, key, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				for _, app := range f.apps {
					id, _, err := canary.ResolveProjectCanary(ctx, app.ID, "production", key)
					if err != nil || id != wanted {
						t.Fatalf("split graph: %s %s %v", id, wanted, err)
					}
				}
			}
			if outcome == "abort" {
				f.advance(t, true)
				safe = f.advance(t, true)
				if safe.Status != "rolled_back" || safe.RestoredReleaseID != f.baseline {
					t.Fatalf("preactivation abort=%+v", safe)
				}
				head, _ = f.store.ProjectEnvironmentConfigLatest(ctx, f.account, f.project, "production")
				if head.Version != f.previous.Version {
					t.Fatal("abort changed configuration")
				}
				return
			}
			f.advance(t, false)
			safe = f.advance(t, false)
			if safe.Verification.Percent != 25 {
				t.Fatal("missing 25 percent checkpoint")
			}
			// Repeated delivery of an old stage round must not advance the new stage.
			stale := ProjectReleaseVerificationRound{Generation: 1, Evidence: []api.ProjectReleaseProbeEvidence{{Probe: safe.Verification.Probes[0], CandidateRequests: 10, StableRequests: 10}}}
			if _, err := f.store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, f.account, f.attempt, &stale); err != nil {
				t.Fatal(err)
			}
			if f.progress(t).Verification.Percent != 25 {
				t.Fatal("stale stage promoted")
			}
			if outcome == "operator" {
				if _, err := f.store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account, ProjectID: f.project, EnvironmentSlug: "production", Values: f.previous.Values, ConfigHash: f.previous.ConfigHash}); err != nil {
					t.Fatal(err)
				}
				safe = f.advance(t, false)
				if safe.Status != "conflicted" {
					t.Fatalf("operator ABA overwritten: %+v", safe)
				}
				return
			}
			f.advance(t, false)
			safe = f.advance(t, false)
			if safe.Verification.Percent != 100 || safe.ReleaseID != candidate {
				t.Fatalf("activation changed cohort graph: %+v", safe)
			}
			active, err = reader.ActiveProjectReleaseSet(ctx, f.account, f.project, "production")
			if err != nil || active.ID != candidate {
				t.Fatalf("candidate not active: %+v %v", active, err)
			}
			head, err = f.store.ProjectEnvironmentConfigLatest(ctx, f.account, f.project, "production")
			if err != nil || head.Version != f.previous.Version+1 || head.ConfigHash != safe.Configuration.TargetHash {
				t.Fatalf("graph/config disagreement: %+v %v", head, err)
			}
			values, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account, f.apps[0].ID, f.old[0].DeploymentID)
			if err != nil || len(values.Values) != 1 || values.Values[0].Value != "old" {
				t.Fatalf("baseline config moved: %+v %v", values.Values, err)
			}
			if outcome == "rollback" {
				f.advance(t, true)
				safe = f.advance(t, true)
				if safe.Status != "rolled_back" {
					t.Fatalf("full rollback=%+v", safe)
				}
				head, _ = f.store.ProjectEnvironmentConfigLatest(ctx, f.account, f.project, "production")
				if head.Version != f.previous.Version+2 || head.ConfigHash != f.previous.ConfigHash {
					t.Fatal("rollback did not restore paired head")
				}
				return
			}
			f.advance(t, false)
			safe = f.advance(t, false)
			if safe.Status != "succeeded" {
				t.Fatalf("full traffic not verified: %+v", safe)
			}
		})
	}
}
