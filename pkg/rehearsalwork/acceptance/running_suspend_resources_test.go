// adr: 20261017201015
package acceptance

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

func TestRunningSuspendRealFixturesRetainCommitAndDetectDuplicateFiles(t *testing.T) {
	if os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL") == "" {
		t.Skip("requires dedicated disposable fixture cluster")
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool) (api.ScenarioActionSnapshot, rehearsalpg.Evidence, rehearsalobj.Evidence) {
				f := newFeatureFixture(t, backend, true, fixtureFeatures{running: true})
				job := f.claim(t, f.enqueue(t).ID)
				if _, err := f.connection.Exec(t.Context(), "INSERT INTO public.imports VALUES ('customer-import')"); err != nil {
					t.Fatal(err)
				}
				originalPID := f.connection.PgConn().PID()
				receipt, held, detach := f.holdRunningCheckpoint(t, t.Context())
				defer detach()
				f.resumeRunningCheckpoint(t, receipt, held, 100)
				var count int
				if err := f.connection.QueryRow(t.Context(), "SELECT count(*) FROM public.imports").Scan(&count); err != nil || count != 1 || f.connection.PgConn().PID() != originalPID {
					t.Fatalf("committed data or original fixture session changed: %d %v", count, err)
				}
				export := func() {
					t.Helper()
					if w := f.object(t, "worker", "PUT", "exports/result.txt", []byte("same export")); w.Code != 200 {
						t.Fatalf("export: %d %s", w.Code, w.Body.String())
					}
					f.notify(t)
				}
				export()
				if duplicate {
					export()
				}
				f.complete(t, job)
				detach()
				actions := f.finish(t, "")
				if _, err := actioncompare.ValidateSnapshot(&actions); err != nil {
					t.Fatal(err)
				}
				pg, err := f.postgres.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				objects, err := f.objects.Seal(t.Context(), f.account.ID, f.run, f.identities["primary"].DeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				return actions, pg, objects
			}
			a, pgA, objA := run(false)
			b, pgB, objB := run(true)
			if c := rehearsalpg.Compare(&pgA, &pgB); c.Status != "passed" {
				t.Fatalf("database behavior unexpectedly changed: %+v", c)
			}
			if rehearsalobj.Hash(objA.After) != rehearsalobj.Hash(objB.After) {
				t.Fatal("same exported bytes produced different final object state")
			}
			if c := rehearsalobj.Compare(&objA, &objB); c.Status != "failed" || len(objA.Mutations) != 1 || len(objB.Mutations) != 2 {
				t.Fatalf("duplicate object write escaped: %+v", c)
			}
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			if c := actioncompare.Compare(wrap(a), wrap(b)); c.Status != "failed" {
				t.Fatalf("duplicate notification escaped: %+v", c)
			}
		})
	}
}
