// Package acceptance exercises the durable platform boundary without a VM.
// Native VM identity, networking and secret delivery remain a rollout gate.
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	objruntime "github.com/onebox-faas/faas/pkg/rehearsalobj/runtime"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type fixture struct {
	nodeID         string
	store          state.Store
	pool           *pgxpool.Pool
	account        state.Account
	run            string
	apps           map[string]state.App
	identities     map[string]rehearsalrecorder.Identity
	recorders      map[string]string
	objects        *objruntime.Service
	collector      *objruntime.Collector
	postgres       *pgruntime.Service
	connection     *pgx.Conn
	access, secret string
	workerSession  *rehearsalrecorder.Session
}

type fixtureFeatures struct{ cold, termination, unbooted, resume, running bool }

func newFixture(t *testing.T, backend string, resources bool, terminate ...bool) *fixture {
	t.Helper()
	return newFeatureFixture(t, backend, resources, fixtureFeatures{termination: len(terminate) > 0 && terminate[0]})
}
func newFixtureWithCold(t *testing.T, backend string, resources, cold bool, terminate ...bool) *fixture {
	t.Helper()
	return newFeatureFixture(t, backend, resources, fixtureFeatures{cold: cold, termination: len(terminate) > 0 && terminate[0]})
}
func newFeatureFixture(t *testing.T, backend string, resources bool, features fixtureFeatures) *fixture {
	t.Helper()
	ctx := t.Context()
	f := &fixture{run: strings.ReplaceAll(uuid.NewString(), "-", ""), apps: map[string]state.App{}, identities: map[string]rehearsalrecorder.Identity{}, recorders: map[string]string{}, access: "GRFIXTUREACCESS123456", secret: "fixture-only-secret"}
	if backend == "postgres" {
		f.pool = pgtest.OpenMigrated(t)
		f.store = state.NewPgStore(f.pool)
	} else {
		f.store = state.NewMemStore()
	}
	var err error
	f.account, err = f.store.CreateAccount(ctx, uuid.NewString()+"@workers.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"primary", "worker", "reader", "unbound"}
	participants := make([]rehearsalwork.Participant, 0, len(names))
	members := make([]state.ScenarioTestMember, 0, len(names))
	expires := time.Now().Add(time.Hour)
	for _, name := range names {
		a, e := f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: name + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, PreviewOfSlug: name, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if e != nil {
			t.Fatal(e)
		}
		f.apps[name] = a
		p := rehearsalwork.Participant{Workload: name, Async: name == "worker"}
		if resources && (name == "primary" || name == "worker") {
			p.Postgres, p.Objects = "read_write", "read_write"
		}
		if resources && name == "reader" {
			p.Objects = "read"
		}
		participants = append(participants, p)
	}
	plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: participants}
	withTermination := features.termination
	if withTermination {
		plan.WorkerTermination = &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}
	}
	work, _, e := rehearsalwork.Normalize(plan, names)
	if e != nil {
		t.Fatal(e)
	}
	fixtures := []api.ScenarioSimulationFixture{{Workload: "worker", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}
	if withTermination || features.running {
		fixtures = append(fixtures, api.ScenarioSimulationFixture{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}})
	}
	simulation, _, e := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{Version: 1, Fixtures: fixtures}, names)
	if e != nil {
		t.Fatal(e)
	}
	coldJSON := ""
	if features.cold {
		raw, _, err := rehearsalcold.Normalize(rehearsalcold.Spec{Version: 1, WithinMS: 120000}, names)
		if err != nil {
			t.Fatal(err)
		}
		coldJSON = string(raw)
	}
	resumeJSON := ""
	if features.resume || features.running {
		spec := rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"worker"}, PauseMS: 1, WithinMS: 1000}
		if features.running {
			spec.Checkpoint = "managed_job"
			spec.WorkerCheckpoint = "after-commit"
		}
		raw, _, err := rehearsalresume.Normalize(spec, names)
		if err != nil {
			t.Fatal(err)
		}
		resumeJSON = string(raw)
	}
	for _, name := range names {
		members = append(members, state.ScenarioTestMember{SuspendResumeJSON: resumeJSON, ColdStartJSON: coldJSON, AppID: f.apps[name].ID, Workload: name, NetworkMode: "contained", WebhookMode: "capture", SimulationJSON: string(simulation), ManagedWorkJSON: string(work)})
	}
	if e = f.store.RegisterScenarioTestMembers(ctx, f.account.ID, f.run, members); e != nil {
		t.Fatal(e)
	}
	if m, e := f.store.ScenarioTestMemberByApp(ctx, f.apps["worker"].ID); e != nil || m.ManagedWorkJSON != string(work) {
		t.Fatalf("participant round trip: %+v %v", m, e)
	}
	if resources {
		f.createResources(t)
	}
	node, e := f.store.CreateComputeNode(ctx, state.ComputeNode{Name: "workers-" + uuid.NewString(), TargetURL: "unix:///tmp/rehearsal-worker-test.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Lifecycle: state.NodeLifecycleActive})
	if e != nil {
		t.Fatal(e)
	}
	f.nodeID = node.ID
	for _, name := range names {
		a := f.apps[name]
		dep, e := f.store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:worker-fixture", Status: state.DeployPending})
		if e != nil {
			t.Fatal(e)
		}
		if e = f.store.MarkDeploymentLive(ctx, dep.ID); e != nil {
			t.Fatal(e)
		}
		if features.unbooted {
			f.identities[name] = rehearsalrecorder.Identity{AccountID: f.account.ID, RunID: f.run, AppID: a.ID, DeploymentID: dep.ID}
			continue
		}
		i, e := f.store.CreateInstance(ctx, a.ID, dep.ID, string(state.StateColdBooting), 128, node.ID, "")
		if e != nil {
			t.Fatal(e)
		}
		id := rehearsalrecorder.Identity{AccountID: f.account.ID, RunID: f.run, AppID: a.ID, DeploymentID: dep.ID, InstanceID: i.ID}
		f.identities[name] = id
		if (withTermination || features.running) && name == "worker" {
			f.workerSession, e = rehearsalrecorder.Start(ctx, ctx, f.store.(rehearsalrecorder.Store), id)
			if e != nil {
				t.Fatal(e)
			}
			f.workerSession.Activate()
			t.Cleanup(f.workerSession.Stop)
			if withTermination {
				continue
			}
		} else {
			session, e := f.store.(rehearsalrecorder.Store).StartScenarioRecorder(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			f.recorders[name] = session
		}
		if features.resume || features.running {
			values, readErr := f.store.RuntimeAppValuesForDeployment(ctx, f.account.ID, a.ID, dep.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			fence, fenceErr := state.NewRuntimeAppConfigFence(values)
			if fenceErr != nil {
				t.Fatal(fenceErr)
			}
			host := map[string]string{"primary": "10.100.0.2", "worker": "10.100.0.6", "reader": "10.100.0.10", "unbound": "10.100.0.14"}[name]
			if _, publishErr := f.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: f.account.ID, AppID: a.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Fence: fence.SecretFence, ConfigFence: fence, Netns: "fc-" + i.ID, HostIP: host, GuestUID: 20001}); publishErr != nil {
				t.Fatal(publishErr)
			}
		}
	}
	return f
}

func (f *fixture) createResources(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	catalog := f.store.(rehearsalobj.Catalog)
	f.objects = objruntime.NewService(catalog)
	f.collector = objruntime.NewCollector(catalog)
	collectorCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go f.collector.Run(collectorCtx)
	initial := []rehearsalobj.Data{}
	snapshot, _ := rehearsalobj.Snapshot(initial)
	_, e := f.objects.Create(ctx, f.account.ID, f.run, f.apps["primary"].ID, f.access, []byte("sealed-fixture-only"), rehearsalobj.CreateRequest{Spec: rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(snapshot)}, Objects: initial})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = f.objects.Delete(context.Background(), f.account.ID, f.run) })
	dsn := os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL")
	if dsn == "" {
		t.Fatal("resource acceptance requires a dedicated fixture test cluster")
	}
	b, e := pgruntime.NewPostgres(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	f.postgres = pgruntime.NewService(f.store.(rehearsalpg.Catalog), b)
	sql := "CREATE TABLE public.imports (item text PRIMARY KEY);"
	_, _, e = f.postgres.Create(ctx, f.account.ID, f.run, f.apps["primary"].ID, "primary.svc.gregale", rehearsalpg.CreateRequest{SQL: sql, Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(sql)), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 1000}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := f.postgres.Delete(context.Background(), f.account.ID, f.run); e != nil {
			t.Errorf("database cleanup: %v", e)
		}
	})
	workerURL, e := f.postgres.BindParticipant(ctx, f.account.ID, f.run, f.apps["worker"].ID, "worker.svc.gregale")
	if e != nil {
		t.Fatal(e)
	}
	config, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	proxy := rehearsalpg.Proxy{Target: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))), Resolve: func(ctx context.Context, _ string) (rehearsalpg.Resource, error) {
		return f.store.(rehearsalpg.Catalog).ScenarioPostgresForApp(ctx, f.apps["worker"].ID)
	}}
	proxyCtx, stopProxy := context.WithCancel(ctx)
	t.Cleanup(stopProxy)
	go func() { _ = proxy.Serve(proxyCtx, listener) }()
	u, e := url.Parse(workerURL)
	if e != nil {
		t.Fatal(e)
	}
	u.Host = listener.Addr().String()
	f.connection, e = pgx.Connect(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = f.connection.Close(context.Background()) })
}

func (f *fixture) enqueue(t *testing.T) state.Invocation {
	t.Helper()
	j, e := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.account.ID, AppID: f.apps["worker"].ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/import", Payload: json.RawMessage(`{"customer":9007199254740993}`), DueAt: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func (f *fixture) claim(t *testing.T, id string) state.Invocation {
	t.Helper()
	j, e := f.store.ClaimInvocation(t.Context(), id, f.identities["worker"].InstanceID, 60)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func (f *fixture) complete(t *testing.T, j state.Invocation) {
	t.Helper()
	if e := f.store.(state.InvocationClaimCompletionStore).CompleteInvocationClaim(t.Context(), j.ID, state.InvocationClaim{Attempt: j.Attempts, ReplayGeneration: j.ReplayGeneration}, json.RawMessage(`{"ok":true}`)); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) seal(t *testing.T) api.ScenarioActionSnapshot {
	t.Helper()
	s, e := f.store.(state.ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), f.account.ID, f.run)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func (f *fixture) finish(t *testing.T, missing string) api.ScenarioActionSnapshot {
	t.Helper()
	_ = f.seal(t)
	for name, id := range f.identities {
		if name == missing {
			continue
		}
		if _, e := f.store.(rehearsalrecorder.Store).SettleScenarioRecorder(t.Context(), id, f.recorders[name], false, true); e != nil {
			t.Fatal(e)
		}
	}
	return f.seal(t)
}

func (f *fixture) notify(t *testing.T) {
	t.Helper()
	id := f.identities["worker"]
	request := f.workerRequest(t)
	r := api.ScenarioSimulationRequest{RecorderRequestID: request, AccountID: f.account.ID, RunID: f.run, AppID: id.AppID, InstanceID: id.InstanceID, Integration: "email", Method: "POST", Path: "/emails", Body: []byte(`{"from":"sender@example.invalid","to":["fixture@example.invalid"],"subject":"Imported","text":"Complete"}`)}
	store := f.store.(state.ScenarioSimulationStore)
	d, e := store.BeginScenarioSimulation(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	if d.Attempt.Status != "pending" {
		t.Fatalf("simulation not admitted: %+v", d)
	}
	if e = store.FinishScenarioSimulation(t.Context(), r, d.Attempt.ID, "completed"); e != nil {
		t.Fatal(e)
	}
}

func (f *fixture) workerRequest(t *testing.T) string {
	t.Helper()
	return f.workerRequestContext(t.Context(), t)
}

func (f *fixture) workerRequestContext(ctx context.Context, t *testing.T) string {
	t.Helper()
	var request string
	var err error
	if f.workerSession != nil {
		request, err = f.workerSession.Begin(ctx)
	} else {
		request, err = f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(ctx, f.identities["worker"], f.recorders["worker"])
	}
	if err != nil {
		t.Fatal(err)
	}
	return request
}
func (f *fixture) object(t *testing.T, workload, method, key string, body []byte, credentialWorkload ...string) *httptest.ResponseRecorder {
	t.Helper()
	id := f.identities[workload]
	r := httptest.NewRequest(method, "http://"+workload+".svc.gregale:10084/imports/"+key, bytes.NewReader(body))
	r.Header.Set("X-Amz-Content-Sha256", rehearsalobj.HashBytes(body))
	credentialFor := workload
	if len(credentialWorkload) > 0 {
		credentialFor = credentialWorkload[0]
	}
	access, secret := f.access, f.secret
	if credentialFor != "primary" {
		access, secret = rehearsalwork.ObjectCredentials(f.secret, f.identities[credentialFor].AppID)
	}
	if e := awsv4.NewSigner().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: access, SecretAccessKey: secret}, r, rehearsalobj.HashBytes(body), "s3", rehearsalobj.Region, time.Now()); e != nil {
		t.Fatal(e)
	}
	h := objruntime.Handler{Catalog: f.store.(rehearsalobj.Catalog), Collector: f.collector, Resolve: func(context.Context, string) (string, string, error) { return id.AppID, id.DeploymentID, nil }, OpenSecret: func([]byte) (string, error) { return f.secret, nil }}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestManagedWorkerJournalLossCannotPass(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFixture(t, backend, false)
			j := f.claim(t, f.enqueue(t).ID)
			f.complete(t, j)
			if f.pool != nil {
				if _, e := f.pool.Exec(t.Context(), `DELETE FROM scenario_managed_work WHERE invocation_id=$1`, j.ID); e == nil {
					t.Fatal("journal was deleted outside invocation ownership")
				}
				if _, e := f.pool.Exec(t.Context(), `UPDATE scenario_managed_work SET payload='{}' WHERE invocation_id=$1`, j.ID); e == nil {
					t.Fatal("journal intent was rewritten")
				}
			}
			if n, e := f.store.DeleteInvocationsByIDs(t.Context(), []string{j.ID}); e != nil || n != 1 {
				t.Fatalf("remove fixture invocation: %d %v", n, e)
			}
			if s := f.finish(t, ""); s.Status == "complete" {
				t.Fatal("lost invocation evidence passed")
			}
		})
	}
}

func TestManagedWorkerFenceLeavesOrdinaryInvocationsIndependent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFixture(t, backend, false)
			ordinary, e := f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
			if e != nil {
				t.Fatal(e)
			}
			j, e := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.account.ID, AppID: ordinary.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/ordinary", Payload: json.RawMessage(`{}`), DueAt: time.Now()})
			if e != nil || j.State != state.InvocationPending {
				t.Fatalf("ordinary admission changed: %+v %v", j, e)
			}
			if e = f.store.CancelInvocation(t.Context(), j.ID); e != nil {
				t.Fatal(e)
			}
			if n, e := f.store.DeleteInvocationsByIDs(t.Context(), []string{j.ID}); e != nil || n != 1 {
				t.Fatalf("ordinary cleanup changed: %d %v", n, e)
			}
			managed := f.claim(t, f.enqueue(t).ID)
			f.complete(t, managed)
			if s := f.finish(t, ""); s.Status != "complete" {
				t.Fatalf("ordinary work poisoned rehearsal evidence: %+v", s)
			}
		})
	}
}

func TestManagedWorkerObjectCapabilitiesAndCredentialsAreScoped(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires a dedicated disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"read_only", "foreign_credential"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newFixture(t, backend, true)
				workload, credential := "reader", "reader"
				if mode == "foreign_credential" {
					workload, credential = "worker", "primary"
				}
				w := f.object(t, workload, "PUT", "forbidden.txt", []byte("must not persist"), credential)
				if w.Code < 400 {
					t.Fatal("ungranted write succeeded")
				}
				r, e := f.store.(rehearsalobj.Catalog).GetScenarioObjects(t.Context(), f.account.ID, f.run)
				if e != nil || !r.Failed || len(r.Current) != 0 {
					t.Fatalf("denied operation changed data or lost refusal evidence: %+v %v", r, e)
				}
			})
		}
	}
}

func TestManagedWorkerRetryCompletionAndAdmissionFence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFixture(t, backend, false)
			j := f.claim(t, f.enqueue(t).ID)
			if e := f.store.FailInvocation(t.Context(), j.ID, "retry fixture", time.Nanosecond, 3); e != nil {
				t.Fatal(e)
			}
			if s := f.seal(t); s.Status != "pending" || s.ManagedWork == nil || s.ManagedWork.Status != "pending" {
				t.Fatalf("retry did not block collection: %+v", s)
			}
			j = f.claim(t, j.ID)
			f.complete(t, j)
			s := f.finish(t, "")
			if _, e := actioncompare.ValidateSnapshot(&s); e != nil || len(s.ManagedWork.Jobs) != 1 || len(s.ManagedWork.Jobs[0].Attempts) != 2 {
				raw, _ := json.Marshal(s.ManagedWork)
				t.Fatalf("invalid proof: %s %v", raw, e)
			}
			if _, e := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.account.ID, AppID: f.apps["worker"].ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/late", Payload: json.RawMessage(`{}`)}); e == nil {
				t.Fatal("sealed work admission reopened")
			}
		})
	}
}

func TestManagedWorkerIncompleteEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"late", "cancelled", "unknown", "deployment", "missing_worker", "missing_recorder"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newFixture(t, backend, false)
				if mode == "missing_worker" {
					if s := f.finish(t, ""); s.Status != "incomplete" {
						t.Fatalf("missing worker passed: %+v", s)
					}
					return
				}
				j := f.enqueue(t)
				if mode == "cancelled" {
					if e := f.store.CancelInvocation(t.Context(), j.ID); e != nil {
						t.Fatal(e)
					}
				} else {
					j = f.claim(t, j.ID)
					if mode == "late" {
						_ = f.seal(t)
						late := f.enqueue(t)
						if late.State != state.InvocationCancelled {
							t.Fatal("closing admission was not durably refused")
						}
					}
					if mode == "unknown" {
						if e := f.store.FailInvocation(t.Context(), j.ID, "dispatch lease expired; requeued", time.Nanosecond, 3); e != nil {
							t.Fatal(e)
						}
						j = f.claim(t, j.ID)
					}
					f.complete(t, j)
				}
				if mode == "deployment" {
					if _, e := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.apps["worker"].ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:replacement", Status: state.DeployPending}); e != nil {
						t.Fatal(e)
					}
				}
				missing := ""
				if mode == "missing_recorder" {
					missing = "worker"
				}
				s := f.finish(t, missing)
				if s.Status == "complete" {
					t.Fatalf("%s produced passing evidence: %+v", mode, s)
				}
			})
		}
	}
}

func TestManagedWorkerImportDetectsDuplicateEffectsAfterRetry(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires a dedicated disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool) (string, api.ScenarioActionSnapshot, rehearsalpg.Evidence, rehearsalobj.Evidence) {
				f := newFixture(t, backend, true)
				response, job := f.triggerImport(t)
				for attempt := 1; attempt <= 2; attempt++ {
					job = f.claim(t, job.ID)
					if _, e := f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import') ON CONFLICT DO NOTHING"); e != nil {
						t.Fatal(e)
					}
					if duplicate || attempt == 2 {
						if w := f.object(t, "worker", "PUT", "exports/result.txt", []byte("same export")); w.Code != 200 {
							t.Fatalf("worker write: %d %s", w.Code, w.Body.String())
						}
						f.notify(t)
					}
					if attempt == 1 {
						if e := f.store.FailInvocation(t.Context(), job.ID, "retry import", time.Nanosecond, 3); e != nil {
							t.Fatal(e)
						}
					} else {
						f.complete(t, job)
					}
				}
				if w := f.object(t, "reader", "GET", "exports/result.txt", nil); w.Code != 200 || w.Body.String() != "same export" {
					t.Fatal("reader did not share the fixture namespace")
				}
				if _, e := f.store.(rehearsalpg.Catalog).ScenarioPostgresForApp(t.Context(), f.apps["unbound"].ID); e == nil {
					t.Fatal("unbound service accessed database")
				}
				if w := f.object(t, "unbound", "GET", "exports/result.txt", nil); w.Code != 403 {
					t.Fatal("unbound service accessed objects")
				}
				s := f.finish(t, "")
				if _, e := actioncompare.ValidateSnapshot(&s); e != nil {
					t.Fatalf("action evidence: %+v %v", s, e)
				}
				pg, e := f.postgres.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if e != nil {
					t.Fatal(e)
				}
				objects, e := f.objects.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if e != nil {
					t.Fatal(e)
				}
				if len(objects.Mutations) == 0 || objects.Mutations[0].Workload != "worker" {
					t.Fatal("mutation lost worker attribution")
				}
				return response, s, pg, objects
			}
			a, old, oldPG, oldObjects := run(false)
			b, candidate, newPG, newObjects := run(true)
			if a != b || rehearsalpg.Compare(&oldPG, &newPG).Status != "passed" || rehearsalobj.Hash(oldObjects.After) != rehearsalobj.Hash(newObjects.After) {
				t.Fatal("fixture did not preserve API and final data")
			}
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			if c := actioncompare.Compare(wrap(old), wrap(candidate)); c.Status != "failed" {
				t.Fatalf("duplicate notification passed: %+v", c)
			}
			if c := rehearsalobj.Compare(&oldObjects, &newObjects); c.Status != "failed" || c.BeforeMutations != 1 || c.AfterMutations != 2 {
				t.Fatalf("duplicate export passed: %+v", c)
			}
		})
	}
}
