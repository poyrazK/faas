// adr: 20261019121546 — managed object storage credential receipts admit enforced candidates.
package pgintegration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidateStorageTestStore interface {
	candidateBindingTestStore
	state.EnvironmentQualificationObjectStorageStore
	state.ObjectBucketStore
	state.ObjectS3CredentialBindingStore
	GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error)
}

var storageSuffixes = []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"}

// seedCandidateStorageBinding creates a ready bucket and an active managed S3
// credential with its six sealed deliveries in the candidate scope.
func seedCandidateStorageBinding(t *testing.T, basic gitOpsTestStore, accountID, appID, prefix, permission string) state.ObjectS3Credential {
	t.Helper()
	store := basic.(candidateStorageTestStore)
	bucketID := uuid.NewString()
	bucket, err := store.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: bucketID, AccountID: accountID, AppID: appID,
		Name: strings.ToLower(strings.ReplaceAll(prefix, "_", "-")), Scope: "production", Region: "eu", BackendID: "test",
		BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "candidate-" + strings.ReplaceAll(bucketID, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(t.Context(), accountID, appID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(t.Context(), bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	access, secret, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	credentialID := uuid.NewString()
	request := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: credentialID, AccountID: accountID,
		BucketID: bucket.ID, AccessKeyID: access, SecretSealed: []byte(secret), KID: "age1test", Label: "compute", Permission: permission,
		Status: "active", ManagedAppID: appID, ManagedScope: "production", ManagedPrefix: prefix}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 30}
	for _, suffix := range storageSuffixes {
		request.Secrets = append(request.Secrets, state.AppSecret{AccountID: accountID, AppID: appID, Scope: "production", Key: prefix + suffix,
			Ciphertext: []byte(fmt.Sprintf("sealed-%s", suffix)), Kid: "age1test", ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: credentialID})
	}
	created, err := store.CreateObjectS3ComputeBinding(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// storageDeliveries reads the scoped delivery versions a candidate receives.
func storageDeliveries(t *testing.T, basic gitOpsTestStore, accountID, appID, prefix string) map[string]int64 {
	t.Helper()
	versions := map[string]int64{}
	for _, suffix := range storageSuffixes {
		s, err := basic.(candidateStorageTestStore).GetAppSecretInScope(t.Context(), accountID, appID, "production", prefix+suffix)
		if err != nil || s.DeliveryVersion < 1 {
			t.Fatal("storage delivery", err)
		}
		versions["production/"+prefix+suffix] = s.DeliveryVersion
	}
	return versions
}

// storageCandidateFixture prepares an enforced cohort whose HTTP candidate has
// one managed S3 binding with the given permission and policy requirement.
func storageCandidateFixture(t *testing.T, basic gitOpsTestStore, permission string, requirements map[string]string, seed bool) (state.EnvironmentGitOpsLease, environmentsync.Plan, []state.EnvironmentWorkloadQualificationRequest, state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	lease, plan, requests, placement := candidateBindingFixtureWith(t, basic, "", "", func(accountID, appID, resource string) map[string]string {
		if resource == "workload/worker" {
			return nil
		}
		if seed {
			seedCandidateStorageBinding(t, basic, accountID, appID, "GREGALE_S3_ASSETS", permission)
		}
		return requirements
	})
	web := httpCandidate(t, requests)
	var versions map[string]int64
	if seed {
		versions = storageDeliveries(t, basic, lease.Source.AccountID, web.AppID, "GREGALE_S3_ASSETS")
	}
	completeCandidateBindingCohortWithSecrets(t, basic.(candidateBindingTestStore), requests, placement, true, map[string]map[string]int64{web.AppID: versions})
	return lease, plan, requests, web
}

func TestCandidateBindingObjectStorageReceiptsQualifyRequirement(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, web := storageCandidateFixture(t, basic, "read_write", map[string]string{"GREGALE_S3_ASSETS": "write"}, true)
		store := basic.(candidateStorageTestStore)
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("unverified bucket credential activated: %v %v", active, err)
		}
		assertCandidatesHeld(t, basic, requests)
		checks, err := store.PendingEnvironmentQualificationObjectStorageChecks(t.Context(), web.GraphID)
		if err != nil || len(checks) != 1 {
			t.Fatalf("pending checks: %+v %v", checks, err)
		}
		c := checks[0]
		if c.RequestID != web.ID || c.Attempt != web.Attempt || c.Prefix != "GREGALE_S3_ASSETS" || c.Permission != "read_write" || c.Mode != "write" || len(c.Secrets) != 6 {
			t.Fatalf("check is not bound to the candidate delivery: %+v", c)
		}
		stale := c
		stale.Secrets = append([]state.EnvironmentQualificationObjectStorageSecret(nil), c.Secrets...)
		stale.Secrets[0].DeliveryVersion++
		if err := store.RecordEnvironmentQualificationObjectStorageReceipt(t.Context(), stale); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatal("receipt accepted a different delivery", err)
		}
		if err := store.RecordEnvironmentQualificationObjectStorageReceipt(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		if again, err := store.PendingEnvironmentQualificationObjectStorageChecks(t.Context(), web.GraphID); err != nil || len(again) != 0 {
			t.Fatalf("verified credential still pending: %+v %v", again, err)
		}
		release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !active || len(release.Members) != 2 {
			t.Fatalf("checked activation with object storage receipt: %+v %v %v", release, active, err)
		}
	})
}

func TestCandidateBindingObjectStorageUnsatisfiedRequirementStaysHeld(t *testing.T) {
	for name, tc := range map[string]struct {
		permission   string
		requirements map[string]string
		seed         bool
	}{
		"insufficient permission": {"read", map[string]string{"GREGALE_S3_ASSETS": "write"}, true},
		"unbound requirement":     {"read", map[string]string{"GREGALE_S3_MISSING": "read"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, plan, requests, web := storageCandidateFixture(t, basic, tc.permission, tc.requirements, tc.seed)
				store := basic.(candidateStorageTestStore)
				if checks, err := store.PendingEnvironmentQualificationObjectStorageChecks(t.Context(), web.GraphID); err != nil || len(checks) != 0 {
					t.Fatalf("unsatisfiable credential was offered for verification: %+v %v", checks, err)
				}
				if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
					t.Fatalf("unsatisfied requirement activated: %v %v", active, err)
				}
				assertCandidatesHeld(t, basic, requests)
			})
		})
	}
}

func TestPgCandidateObjectStorageReceiptsAreImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, _, _, web := storageCandidateFixture(t, store, "read", nil, true)
	checks, err := store.PendingEnvironmentQualificationObjectStorageChecks(t.Context(), web.GraphID)
	if err != nil || len(checks) != 1 || checks[0].Mode != "read" {
		t.Fatalf("pending checks: %+v %v", checks, err)
	}
	if err := store.RecordEnvironmentQualificationObjectStorageReceipt(t.Context(), checks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_qualification_object_storage_receipts SET mode='read_write' WHERE request_id=$1", web.ID); err == nil {
		t.Fatal("object storage receipt was mutable")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM environment_qualification_object_storage_receipts WHERE request_id=$1", web.ID); err == nil {
		t.Fatal("object storage receipt was deletable")
	}
	source, err := os.ReadFile("../../../migrations/20261019121645048_candidate_object_storage_binding_qualification.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded object storage receipt history")
	}
}
