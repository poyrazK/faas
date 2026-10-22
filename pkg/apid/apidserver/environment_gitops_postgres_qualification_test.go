// adr: 20261019072523 — apid verifies enforced candidates' sealed PostgreSQL deliveries.
package apidserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidatePostgresIntent struct {
	state.EnvironmentGitOpsIntentStore
	checks    []state.EnvironmentQualificationPostgresCheck
	recordErr map[string]error
	recorded  []string
}

func (s *candidatePostgresIntent) PendingEnvironmentQualificationPostgresChecks(context.Context, string) ([]state.EnvironmentQualificationPostgresCheck, error) {
	return s.checks, nil
}

func (s *candidatePostgresIntent) RecordEnvironmentQualificationPostgresReceipt(_ context.Context, c state.EnvironmentQualificationPostgresCheck) error {
	if err := s.recordErr[c.BindingID]; err != nil {
		return err
	}
	s.recorded = append(s.recorded, c.BindingID)
	return nil
}

func TestCandidatePostgresCredentialStepRecordsOnlyVerifiedDeliveries(t *testing.T) {
	intent := &candidatePostgresIntent{checks: []state.EnvironmentQualificationPostgresCheck{
		{BindingID: "verified"}, {BindingID: "rejected"}, {BindingID: "changed"},
	}, recordErr: map[string]error{"changed": state.ErrBindingPromotionChanged}}
	err := apidserver.VerifyCandidatePostgresCredentialsForTest(t.Context(), intent, uuid.NewString(),
		func(_ context.Context, c state.EnvironmentQualificationPostgresCheck) error {
			if c.BindingID == "rejected" {
				return managedpostgres.ErrConflict
			}
			return nil
		})
	if err != nil || len(intent.recorded) != 1 || intent.recorded[0] != "verified" {
		t.Fatalf("recorded %v, err %v", intent.recorded, err)
	}
	// A store failure other than a changed delivery is surfaced to the caller.
	intent = &candidatePostgresIntent{checks: []state.EnvironmentQualificationPostgresCheck{{BindingID: "broken"}},
		recordErr: map[string]error{"broken": errors.New("store unavailable")}}
	if err := apidserver.VerifyCandidatePostgresCredentialsForTest(t.Context(), intent, uuid.NewString(),
		func(context.Context, state.EnvironmentQualificationPostgresCheck) error { return nil }); err == nil {
		t.Fatal("record failure was swallowed")
	}
}

func TestCandidatePostgresCredentialVerifierChecksSealedDelivery(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	hmac := []byte(strings.Repeat("h", 32))
	check := state.EnvironmentQualificationPostgresCheck{RequestID: uuid.NewString(), Attempt: 1, AccountID: uuid.NewString(),
		AppID: uuid.NewString(), Scope: "production", BindingID: uuid.NewString(), DatabaseID: uuid.NewString(),
		EnvironmentKey: "DATABASE_URL", Access: "read_write", CredentialGeneration: 3, DeliveryVersion: 4, PostgresMajor: 17}
	binding := managedpostgres.Binding{ID: check.BindingID, AccountID: check.AccountID, DatabaseID: check.DatabaseID, AppID: check.AppID,
		Scope: check.Scope, EnvironmentKey: check.EnvironmentKey, Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 3}
	sealed, err := credentialdelivery.Seal(binding, managedpostgres.CredentialMaterial{ProviderIdentityID: "identity", Username: "runtime",
		Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "db.example.test", Port: 5432}}}, identity.Recipient(), hmac)
	if err != nil {
		t.Fatal(err)
	}
	check.CredentialRef, check.Ciphertext, check.Kid, check.ValueHash = sealed.Ref, sealed.Ciphertext, sealed.Kid, sealed.ValueHash

	var probed []string
	probe := func(_ context.Context, dsn string, access managedpostgres.CredentialAccess, major int) error {
		if access != managedpostgres.CredentialReadWrite || major != 17 || !strings.Contains(dsn, "db.example.test") {
			t.Fatalf("probe input: %s %d", access, major)
		}
		probed = append(probed, dsn)
		return nil
	}
	ids := []*age.X25519Identity{identity}
	if err := apidserver.VerifyCandidatePostgresCredentialForTest(t.Context(), check, ids, hmac, probe); err != nil || len(probed) != 1 {
		t.Fatal("exact delivery was not verified", err)
	}
	for name, mutate := range map[string]func(*state.EnvironmentQualificationPostgresCheck){
		"generation": func(c *state.EnvironmentQualificationPostgresCheck) { c.CredentialGeneration = 2 },
		"value hash": func(c *state.EnvironmentQualificationPostgresCheck) { c.ValueHash = "wrong" },
		"ciphertext": func(c *state.EnvironmentQualificationPostgresCheck) { c.Ciphertext = []byte("wrong") },
		"key":        func(c *state.EnvironmentQualificationPostgresCheck) { c.EnvironmentKey = "OTHER_URL" },
	} {
		changed := check
		mutate(&changed)
		if err := apidserver.VerifyCandidatePostgresCredentialForTest(t.Context(), changed, ids, hmac, probe); err == nil {
			t.Fatalf("%s substitution was verified", name)
		}
	}
	if len(probed) != 1 {
		t.Fatal("a substituted delivery reached the SQL probe")
	}
	failed := func(context.Context, string, managedpostgres.CredentialAccess, int) error {
		return managedpostgres.ErrConflict
	}
	if err := apidserver.VerifyCandidatePostgresCredentialForTest(t.Context(), check, ids, hmac, failed); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("probe failure was hidden or leaked the credential", err)
	}
}
