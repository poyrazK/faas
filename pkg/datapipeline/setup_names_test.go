package datapipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSetupMirrorNamesProvisionForAllAcceptedSlugs(t *testing.T) {
	for _, name := range []string{"123-orders", strings.Repeat("9", 40), "orders", strings.Repeat("a", 40)} {
		t.Run(name, func(t *testing.T) {
			s, store, account, _, request := setupFixture(t)
			request.Name = name
			job, err := s.StartSetup(t.Context(), account, request)
			if err != nil {
				t.Fatal(err)
			}
			assertSetupMirrorNameProvisions(t, s, store, job)
		})
	}
}

func TestSetupLegacyDigitMirrorNameRecoversOnRetry(t *testing.T) {
	s, store, account, _, request := setupFixture(t)
	request.Name = "123-orders"
	id := uuid.NewString()
	// Preserve the exact pre-fix persisted definition and its reserved resource
	// identity. It could never have provisioned this invalid database name.
	job, err := store.EnqueueSetup(t.Context(), Setup{ID: id, AccountID: account, DatabaseID: uuid.NewString(), Request: request,
		MirrorName: request.Name + "-mirror-" + id[:8], MirrorPlan: MirrorPlan{Region: "test-region"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(t.Context(), `UPDATE data_pipeline_setups SET state='blocked',phase='provisioning',reason='setup_configuration_invalid' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	retried, err := store.ControlSetup(t.Context(), account, id, job.Revision, "retry")
	if err != nil || retried.DatabaseID != job.DatabaseID {
		t.Fatal("legacy retry changed the reserved database", err)
	}
	assertSetupMirrorNameProvisions(t, s, store, retried)
}

func assertSetupMirrorNameProvisions(t *testing.T, s *Service, store *PostgresStore, job Setup) {
	t.Helper()
	if !managedpostgres.ValidName(job.MirrorName) {
		t.Fatal("generated name violates managed PostgreSQL's name contract", job.MirrorName)
	}
	called := false
	s.EnsureMirror = func(_ context.Context, candidate Setup) error {
		called = true
		if candidate.DatabaseID != job.DatabaseID || candidate.MirrorName != job.MirrorName || !managedpostgres.ValidName(candidate.MirrorName) {
			t.Error("provisioning lost the saved resource identity", candidate.DatabaseID, candidate.MirrorName)
			return ErrInvalid
		}
		return nil
	}
	for range 2 {
		job = setupTick(t, s, store, job.ID)
		if job.State == "blocked" {
			t.Fatal("valid setup remained blocked", job.Reason)
		}
		if called {
			break
		}
	}
	if !called || job.Phase != "checking_mirror" {
		t.Fatal("mirror provisioning did not finish", called, job.Phase)
	}
}
