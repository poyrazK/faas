// adr: 20261018000200
package acceptance

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/state"
)

func waitPostgresFault(t *testing.T, f *fixture, id, want string) rehearsalfault.Row {
	t.Helper()
	s := f.store.(rehearsalfault.Catalog)
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		r, err := s.ScenarioPostgresFault(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == want {
			return r
		}
		if r.State == "failed" {
			t.Fatalf("fault failed: %+v", r)
		}
		select {
		case <-deadline.C:
			t.Fatalf("fault stayed %s", r.State)
		case <-ticker.C:
		}
	}
}

// This uses both real metadata implementations, real database connections via
// the private proxy, signed object operations and the independent collectors.
// It does not qualify native guest identity, packets or secret delivery.
func TestPostgresFaultRealFixturesRecoverAndDetectDuplicateNotification(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires a separate disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool) (api.ScenarioActionSnapshot, rehearsalpg.Evidence, rehearsalobj.Evidence) {
				f := newFeatureFixture(t, backend, true, fixtureFeatures{postgresFault: true})
				j := f.claimWithDeferredBinding(t, f.enqueue(t).ID)
				if _, err := f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import')"); err != nil {
					t.Fatal(err)
				}
				if w := f.object(t, "worker", "PUT", "exports/result.txt", []byte("same export")); w.Code != 200 {
					t.Fatalf("object write: %d", w.Code)
				}
				f.notify(t)
				config := f.connection.Config()
				d := f.checkpoint(t)
				if d.PostgresFaultID == "" || d.WorkerFaultID != "" || d.SuspendResumeID != "" {
					t.Fatalf("wrong checkpoint decision: %+v", d)
				}
				waitPostgresFault(t, f, d.PostgresFaultID, "active")
				if _, err := f.connection.Exec(t.Context(), "SELECT 1"); err == nil {
					t.Fatal("pre-existing connection survived the scoped disconnect")
				}
				denied, err := pgx.ConnectConfig(t.Context(), config)
				if err == nil {
					_ = denied.Close(t.Context())
					t.Fatal("new login was accepted during the outage")
				}
				if err = f.store.FailInvocation(t.Context(), j.ID, "database access unavailable", time.Nanosecond, 3, state.WithInvocationClaim(j)); err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.ClaimInvocation(t.Context(), j.ID, uuid.NewString(), 60); err == nil {
					t.Fatal("retry accepted a replacement worker")
				}
				j = f.claimWithDeferredBinding(t, j.ID)
				proof := waitPostgresFault(t, f, d.PostgresFaultID, "recovered")
				if err = proof.Validate(); err != nil {
					t.Fatal(err)
				}
				f.recorders["worker"] = proof.RecorderSessionID
				f.connection, err = pgx.ConnectConfig(t.Context(), config)
				if err != nil {
					t.Fatalf("same participant credentials did not recover: %v", err)
				}
				if _, err = f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import') ON CONFLICT DO NOTHING"); err != nil {
					t.Fatal(err)
				}
				if duplicate {
					f.notify(t)
				}
				f.complete(t, j)
				s := f.finish(t, "")
				if _, err = actioncompare.ValidateSnapshot(&s); err != nil {
					t.Fatal(err)
				}
				pg, err := f.postgres.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				obj, err := f.objects.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				return s, pg, obj
			}
			a, pgA, objA := run(false)
			b, pgB, objB := run(true)
			if rehearsalpg.Compare(&pgA, &pgB).Status != "passed" || rehearsalobj.Compare(&objA, &objB).Status != "passed" {
				t.Fatal("same final data or object effects did not compare equally")
			}
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			if c := actioncompare.Compare(wrap(a), wrap(a)); c.Status != "passed" {
				t.Fatalf("control did not pass: %+v", c)
			}
			if c := actioncompare.Compare(wrap(a), wrap(b)); c.Status != "failed" {
				t.Fatalf("duplicate after database recovery was missed: %+v", c)
			}
		})
	}
}

func TestPostgresFaultInterruptedCheckpointRestoresButCannotPass(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires a separate disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFeatureFixture(t, backend, true, fixtureFeatures{postgresFault: true})
			j := f.claim(t, f.enqueue(t).ID)
			id := f.identities["worker"]
			req := api.ScenarioSimulationRequest{RecorderRequestID: f.workerRequest(t), AccountID: f.account.ID, RunID: f.run, AppID: id.AppID, InstanceID: id.InstanceID, Integration: "checkpoint", Method: "POST", Path: "/after-commit", Body: []byte(`{}`)}
			s := f.store.(state.ScenarioSimulationStore)
			d, err := s.BeginScenarioSimulation(t.Context(), req)
			if err != nil || d.PostgresFaultID == "" {
				t.Fatalf("checkpoint not admitted: %v", err)
			}
			c := f.store.(rehearsalfault.Catalog)
			r, err := c.ScenarioPostgresFault(t.Context(), d.PostgresFaultID)
			if err != nil {
				t.Fatal(err)
			}
			f.recorders["worker"] = r.RecorderSessionID
			forged := r
			forged.ResourceID = uuid.NewString()
			forged.State = "cutting"
			if c.TransitionScenarioPostgresFault(t.Context(), forged, "requested") == nil {
				t.Fatal("fault adopted another resource")
			}
			if err = s.FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, "interrupted"); err != nil {
				t.Fatal(err)
			}
			r = waitPostgresFault(t, f, d.PostgresFaultID, "failed")
			if !r.Recovered || r.Error == "" || r.Validate() == nil {
				t.Fatalf("interruption lost cleanup or acquired passing proof: %+v", r)
			}
			f.complete(t, j)
			if got := f.finish(t, ""); got.Status == "complete" {
				t.Fatal("interrupted checkpoint passed")
			}
		})
	}
}
