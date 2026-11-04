// adr: 20261103171917
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Deliberately has no HTTP idempotency cache: the resource reservation itself
// must survive losing a successful create/bind response.
type nativeProvisionFixture struct {
	mu                     sync.Mutex
	base                   *localFixture
	database               api.ManagedPostgresDatabase
	binding                api.ManagedPostgresBinding
	fail                   string
	creates, binds, writes int
	creationIDs            []string
	waiting                chan struct{}
	ready                  bool
	deny                   bool
}

func (f *nativeProvisionFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/postgres/") {
		f.base.serveHTTP(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.deny {
		api.WriteProblem(w, api.NewProblem(403, "forbidden", "Revoked", "credential revoked"))
		return
	}
	fail := func(step string) bool {
		if f.fail != step {
			return false
		}
		f.fail = ""
		api.WriteProblem(w, api.NewProblem(503, "unavailable", "Interrupted", "response lost after commit"))
		return true
	}
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodGet:
		items := []api.ManagedPostgresDatabase{}
		if f.database.ID != "" {
			items = append(items, f.database)
		}
		write(api.ManagedPostgresDatabaseList{Items: items})
	case r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodPost:
		var req api.CreateManagedPostgresDatabaseRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.RequestID == "" {
			api.WriteProblem(w, api.ErrValidation("missing reserved identity"))
			return
		}
		f.writes++
		f.creationIDs = append(f.creationIDs, req.RequestID)
		if f.database.ID == "" {
			f.creates++
			f.database = api.ManagedPostgresDatabase{ID: req.RequestID, Name: req.Name, Region: req.Region, State: "ready"}
		} else if f.database.ID != req.RequestID {
			api.WriteProblem(w, api.NewProblem(409, "conflict", "Conflict", "different reservation"))
			return
		}
		if !fail("create") {
			write(f.database)
		}
	case strings.HasSuffix(r.URL.Path, "/bindings") && r.Method == http.MethodPost:
		var req api.CreateManagedPostgresBindingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.writes++
		if f.binding.ID == "" {
			f.binds++
			f.binding = api.ManagedPostgresBinding{ID: "binding-one", DatabaseID: f.database.ID, AppID: req.AppID, Scope: req.Scope, EnvironmentKey: req.EnvironmentKey, Access: req.Access, State: "ready"}
		}
		if !fail("bind") {
			write(f.binding)
		}
	case strings.HasSuffix(r.URL.Path, "/bindings"):
		write(api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{f.binding}})
	case strings.HasPrefix(r.URL.Path, "/v1/postgres/bindings/"):
		if !fail("waiting_binding") {
			write(f.binding)
		}
	case strings.HasPrefix(r.URL.Path, "/v1/postgres/databases/"):
		if f.waiting != nil {
			close(f.waiting)
			f.waiting = nil
		}
		if fail("waiting_resource") {
			return
		}
		d := f.database
		if !f.ready {
			d.State = "provisioning"
		}
		write(d)
	default:
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Missing", r.URL.Path))
	}
}

func nativeProvisionManagement(t *testing.T, handler http.HandlerFunc) (*management, Options, string, string) {
	t.Helper()
	store, path, key := testStateStore(t)
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "native-provision", Environments: &EnvironmentConsent{DefaultWrite: true, Named: map[string]EnvironmentGrant{}}}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	return m, opts, path, key
}

func TestNativeProvisionRecoversEveryCheckpoint(t *testing.T) {
	for _, step := range []string{"create", "waiting_resource", "bind", "waiting_binding", "completed"} {
		t.Run(step, func(t *testing.T) {
			f := &nativeProvisionFixture{base: newLocalFixture(t, true, ""), fail: step, ready: true}
			m, opts, path, key := nativeProvisionManagement(t, f.serveHTTP)
			planned, err := m.planPostgres(t.Context(), planPostgresArgs{App: "alpha", Region: "us-east-1", Reason: "durable setup"})
			if err != nil {
				t.Fatal(err)
			}
			id := planned.(releasePlan).ID
			_, err = m.applyProvision(t.Context(), applyArgs{PlanID: id})
			if (err == nil) != (step == "completed") {
				t.Fatalf("first attempt: %v", err)
			}
			if err := opts.planStore.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore = reopened
			_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			writesBefore := f.writes
			f.mu.Unlock()
			status, err := restored.getProvision(t.Context(), planLookupArgs{PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			want := "interrupted"
			if step == "completed" {
				want = "completed"
			}
			if status.(map[string]any)["status"] != want {
				t.Fatalf("status=%v", status)
			}
			f.mu.Lock()
			writesAfter := f.writes
			f.mu.Unlock()
			if writesAfter != writesBefore {
				t.Fatal("polling mutated native resources")
			}
			if _, err := restored.applyProvision(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creates != 1 || f.binds != 1 {
				t.Fatalf("duplicate creates=%d binds=%d", f.creates, f.binds)
			}
			for _, createdID := range f.creationIDs {
				if createdID != provisionCreationID(restored.plans[id]) {
					t.Fatal("reserved ID changed across recovery")
				}
			}
			if step == "completed" && f.writes != writesBefore {
				t.Fatal("completed receipt repeated writes")
			}
		})
	}
}

func TestNativeProvisionCancellationAndRevocation(t *testing.T) {
	waiting := make(chan struct{})
	f := &nativeProvisionFixture{base: newLocalFixture(t, true, ""), waiting: waiting}
	m, opts, path, key := nativeProvisionManagement(t, f.serveHTTP)
	value, err := m.planPostgres(t.Context(), planPostgresArgs{App: "alpha", Region: "us-east-1", Reason: "cancellation"})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	if _, err := m.applyProvisionRequest(t.Context(), provisionApplyArgs{PlanID: id, Async: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach readiness wait")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := m.getProvision(ctx, planLookupArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.cancelProvision(ctx, planLookupArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		running := m.provisions[id].status == "running"
		m.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker ignored cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := opts.planStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	opts.planStore = reopened
	_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	status, err := restored.getProvision(t.Context(), planLookupArgs{PlanID: id})
	if err != nil || status.(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancelled status=%v err=%v", status, err)
	}
	f.mu.Lock()
	f.deny = true
	f.ready = true
	f.mu.Unlock()
	if _, err := restored.applyProvision(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("revoked credential resumed provisioning")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.binds != 0 || f.creates != 1 {
		t.Fatal("cancellation or revocation created a binding")
	}
}

func TestNativeProvisionOtherResourcesRecoverLostResponses(t *testing.T) {
	for _, kind := range []string{"domain", "cron", "bucket"} {
		t.Run(kind, func(t *testing.T) {
			base := newLocalFixture(t, true, "")
			var mu sync.Mutex
			created, bound, loseCreate, loseBind := false, false, true, true
			creates, binds := 0, 0
			domain := api.CustomDomainResponse{Domain: "api.example.test", AppID: "app-one", CertStatus: "pending", ChallengeToken: "should-never-leak"}
			cron := api.CronResponse{ID: "cron-one", AppID: "app-one", Schedule: "*/15 * * * *", Path: "/tick", Timezone: "UTC", Enabled: true}
			bucket := api.ObjectBucket{ID: "bucket-one", Name: "assets", Scope: "default", State: "ready"}
			binding := api.ObjectStorageComputeBinding{ID: "binding-one", BucketID: bucket.ID, Scope: "default", Prefix: "GREGALE_S3_ASSETS", Credential: api.ObjectS3Credential{Permission: "read_write", Status: "active", AccessKeyID: "should-never-leak"}}
			handler := func(w http.ResponseWriter, r *http.Request) {
				resource := strings.HasPrefix(r.URL.Path, "/v1/domains") || strings.HasPrefix(r.URL.Path, "/v1/crons") || strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/buckets")
				if !resource {
					base.serveHTTP(w, r)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				fail := func() {
					api.WriteProblem(w, api.NewProblem(503, "unavailable", "Lost response", "retry original plan"))
				}
				if r.Method == http.MethodPost {
					if strings.HasSuffix(r.URL.Path, "/compute-bindings") {
						if !bound {
							bound = true
							binds++
						}
						if loseBind {
							loseBind = false
							fail()
							return
						}
						write(binding)
						return
					}
					if !created {
						created = true
						creates++
					}
					if loseCreate {
						loseCreate = false
						fail()
						return
					}
				}
				switch {
				case strings.HasPrefix(r.URL.Path, "/v1/domains"):
					if !created {
						api.WriteProblem(w, api.NewProblem(404, "not_found", "Missing", "no claim"))
						return
					}
					write(domain)
				case r.URL.Path == "/v1/crons" && r.Method == http.MethodGet:
					rows := []api.CronResponse{}
					if created {
						rows = append(rows, cron)
					}
					write(rows)
				case strings.HasPrefix(r.URL.Path, "/v1/crons"):
					write(cron)
				case strings.HasSuffix(r.URL.Path, "/compute-bindings"):
					rows := []api.ObjectStorageComputeBinding{}
					if bound {
						rows = append(rows, binding)
					}
					write(api.ObjectStorageComputeBindingList{Items: rows})
				case r.Method == http.MethodPost:
					write(bucket)
				default:
					rows := []api.ObjectBucket{}
					if created {
						rows = append(rows, bucket)
					}
					write(api.ObjectBucketList{Items: rows})
				}
			}
			m, opts, path, key := nativeProvisionManagement(t, handler)
			var planned any
			var err error
			switch kind {
			case "domain":
				planned, err = m.planDomain(t.Context(), planDomainArgs{App: "alpha", Domain: domain.Domain, Reason: "recovery"})
			case "cron":
				planned, err = m.planCron(t.Context(), planCronArgs{App: "alpha", Schedule: cron.Schedule, Path: cron.Path, Reason: "recovery"})
			case "bucket":
				planned, err = m.planBucket(t.Context(), planBucketArgs{App: "alpha", Name: bucket.Name, Reason: "recovery"})
			}
			if err != nil {
				t.Fatal(err)
			}
			id := planned.(releasePlan).ID
			if _, err = m.applyProvision(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost create response was ignored")
			}
			if err := opts.planStore.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore = reopened
			_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			out, err := restored.applyProvision(t.Context(), applyArgs{PlanID: id})
			if kind == "bucket" {
				if err == nil {
					t.Fatal("lost binding response was ignored")
				}
				out, err = restored.applyProvision(t.Context(), applyArgs{PlanID: id})
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "should-never-leak") {
				t.Fatal("provision receipt exposed credential metadata")
			}
			mu.Lock()
			defer mu.Unlock()
			if creates != 1 || kind == "bucket" && binds != 1 {
				t.Fatalf("duplicates creates=%d binds=%d", creates, binds)
			}
		})
	}
}
