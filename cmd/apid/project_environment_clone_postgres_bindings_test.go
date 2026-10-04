//go:build !no_pg

// adr: 568
package main

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePostgresPreparationFailureStore struct {
	*state.PgStore
	loseReservation, losePreparation bool
}

func (s *clonePostgresPreparationFailureStore) ReserveProjectEnvironmentClonePostgresBinding(ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string) (state.ProjectEnvironmentClonePostgresBindingTarget, bool, error) {
	target, created, err := s.PgStore.ReserveProjectEnvironmentClonePostgresBinding(ctx, lease, sourceID)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return state.ProjectEnvironmentClonePostgresBindingTarget{}, false, errors.New("binding reservation acknowledgement lost")
	}
	return target, created, err
}

func (s *clonePostgresPreparationFailureStore) PrepareProjectEnvironmentClonePostgresBinding(ctx context.Context, lease state.ProjectEnvironmentCloneLease, request state.ProjectEnvironmentClonePostgresBindingRequest) (state.ProjectEnvironmentClonePostgresBindingPreparation, error) {
	prepared, err := s.PgStore.PrepareProjectEnvironmentClonePostgresBinding(ctx, lease, request)
	if err == nil && s.losePreparation {
		s.losePreparation = false
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, errors.New("binding preparation acknowledgement lost")
	}
	return prepared, err
}

func capturedClonePostgresBindingWorkerContract(t *testing.T, srv *server, store *state.PgStore, pool *pgxpool.Pool, databases *managedpostgres.PostgresStore, provider *cloneDatabaseDeadlineProvider, lease state.ProjectEnvironmentCloneLease) state.ProjectEnvironmentCloneLease {
	t.Helper()
	ctx := context.Background()
	views, err := store.ProjectEnvironmentCloneBindings(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil || len(views) != 2 {
		t.Fatalf("binding catalogue: %v", err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previousRecipient, previousHMAC := setSecretRecipient, hostHMACKey
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	hostHMACKey = func() []byte { return []byte("0123456789abcdef0123456789abcdef") }
	defer func() { setSecretRecipient, hostHMACKey = previousRecipient, previousHMAC }()
	stamps := map[string]time.Time{}
	oldCiphertexts := map[string][]byte{}
	for _, view := range views {
		stamp, ok, err := store.AppRuntimeConfigChangedAt(ctx, view.AppID)
		if err != nil || !ok {
			t.Fatalf("source runtime stamp: %v", err)
		}
		stamps[view.AppID] = stamp
		for _, binding := range view.Postgres {
			var ciphertext []byte
			if err := pool.QueryRow(ctx, "select ciphertext from app_secrets where managed_postgres_binding_id=$1", binding.ID).Scan(&ciphertext); err != nil {
				t.Fatal(err)
			}
			oldCiphertexts[binding.ID] = ciphertext
			if _, err := pool.Exec(ctx, "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", binding.ID, []byte("unopenable-production-secret")); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() {
		for id, ciphertext := range oldCiphertexts {
			if _, err := pool.Exec(ctx, "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", id, ciphertext); err != nil {
				t.Error(err)
			}
		}
	}()
	beforeIssues := len(provider.issued)
	failing := &clonePostgresPreparationFailureStore{PgStore: store, loseReservation: true, losePreparation: true}
	previousStore := srv.store
	srv.store = failing
	defer func() { srv.store = previousStore }()
	lease, _, _, err = srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease)
	if err == nil || !strings.Contains(err.Error(), "reservation acknowledgement lost") || len(provider.issued) != beforeIssues {
		t.Fatalf("reservation loss reached provider: %v", err)
	}
	first := views[0].Postgres[0]
	reserved, prepared, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, first.ID)
	if err != nil || prepared != nil || reserved.ID == first.ID || reserved.DatabaseID != lease.Operation.Resources[0].TargetID || reserved.State != "provisioning" {
		t.Fatalf("durable reservation: %+v, %v", reserved, err)
	}
	if _, err := srv.managedPostgresBindings.Reconcile(ctx, reserved.AccountID, reserved.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("ordinary reconciliation accessed private target: %v", err)
	}
	if due, err := databases.DueBindings(ctx, true, 100, time.Now().Add(time.Minute)); err != nil || len(due) != 0 {
		t.Fatalf("private binding entered ordinary sweeps: %+v, %v", due, err)
	}
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: reserved.AccountID, AppID: reserved.AppID, Scope: reserved.Scope, Key: reserved.EnvironmentKey,
		Ciphertext: []byte("forged"), Kid: "forged", ManagedPostgresBindingID: reserved.ID, ManagedCredentialRef: "forged", ManagedCredentialGeneration: 1}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ordinary sink wrote private envelope: %v", err)
	}
	for _, fault := range []string{"token", "revision", "status"} {
		bad := lease
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision++
		case "status":
			bad.Operation.Status = state.CloneOperationPublishing
		}
		if _, _, err := store.ReserveProjectEnvironmentClonePostgresBinding(ctx, bad, first.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s reserved binding: %v", fault, err)
		}
		if _, _, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, bad, first.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s read preparation: %v", fault, err)
		}
	}
	request := state.ProjectEnvironmentClonePostgresBindingRequest{SourceBindingID: first.ID, TargetBindingID: reserved.ID, ProviderIdentityID: "private-provider-identity", MaxSecretsPerApp: 1}
	binding := managedpostgres.Binding{ID: reserved.ID, AccountID: reserved.AccountID, AppID: reserved.AppID, Scope: reserved.Scope, EnvironmentKey: reserved.EnvironmentKey, CredentialGeneration: 1}
	request.CredentialRef, _ = managedPostgresCredentialRef(binding)
	request.Secret = state.AppSecret{AccountID: reserved.AccountID, AppID: reserved.AppID, Scope: reserved.Scope, Key: reserved.EnvironmentKey, Ciphertext: []byte("fixture-sealed"), Kid: "fixture-key", ValueHash: "0123456789abcdef",
		ManagedPostgresBindingID: reserved.ID, ManagedCredentialRef: request.CredentialRef, ManagedCredentialGeneration: 1}
	if _, err := store.PrepareProjectEnvironmentClonePostgresBinding(ctx, lease, request); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("preparation bypassed secret quota: %v", err)
	}
	if target, p, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, first.ID); err != nil || p != nil || target.State != "provisioning" {
		t.Fatalf("quota failure partially prepared binding: %+v, %v", target, err)
	}
	for _, fault := range []string{"scope", "key", "owner", "ref", "generation", "mixed_owner", "class", "version", "shared_identity"} {
		bad := request
		bad.MaxSecretsPerApp = 100
		switch fault {
		case "scope":
			bad.Secret.Scope = "default"
		case "key":
			bad.Secret.Key = "WRONG_KEY"
		case "owner":
			bad.Secret.ManagedPostgresBindingID = first.ID
		case "ref":
			bad.Secret.ManagedCredentialRef = first.CredentialRef
		case "generation":
			bad.Secret.ManagedCredentialGeneration++
		case "mixed_owner":
			bad.Secret.ManagedObjectStorageCredentialID = uuid.NewString()
		case "class":
			bad.Secret.SecretClass = state.SecretClassEphemeral
		case "version":
			bad.Secret.SecretVersion = 2
		case "shared_identity":
			bad.TargetBindingID = first.ID
		}
		if _, err := store.PrepareProjectEnvironmentClonePostgresBinding(ctx, lease, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s preparation accepted: %v", fault, err)
		}
	}
	// The provider may finish an accepted request after clone ownership changes.
	// Its old worker must not commit a secret or preparation receipt. The next
	// worker retries the same deterministic provider identity.
	var takeover state.ProjectEnvironmentCloneLease
	provider.onIssue = func(managedpostgres.CredentialRequest) error {
		if err := store.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
			return err
		}
		var err error
		takeover, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
		return err
	}
	if _, _, _, err := srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease); !errors.Is(err, state.ErrConflict) || len(provider.issued) != beforeIssues+1 {
		t.Fatalf("old credential worker committed after takeover: %v", err)
	}
	provider.onIssue = nil
	if target, prepared, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, takeover, first.ID); err != nil || prepared != nil || target.ID != reserved.ID || target.State != "provisioning" {
		t.Fatalf("stale worker partially prepared target: %+v, %v", target, err)
	}
	lease = takeover
	lease, _, _, err = srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease)
	if err == nil || !strings.Contains(err.Error(), "preparation acknowledgement lost") || len(provider.issued) != beforeIssues+2 {
		t.Fatalf("preparation loss: %v, issues %d", err, len(provider.issued))
	}
	if provider.issued[beforeIssues] != provider.issued[beforeIssues+1] {
		t.Fatal("takeover changed deterministic provider credential request")
	}
	lease, ids, count, err := srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease)
	if err != nil || len(ids) != 2 || count != 2 || len(provider.issued) != beforeIssues+3 {
		t.Fatalf("credential retry changed identities/count: %v, %v, %d", ids, err, len(provider.issued))
	}
	for _, view := range views {
		for _, source := range view.Postgres {
			target, prepared, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, source.ID)
			if err != nil || prepared == nil || target.ID == source.ID || target.CredentialRef == source.CredentialRef || target.DatabaseID != reserved.DatabaseID || target.Scope != lease.Operation.TargetEnvironment || target.EnvironmentKey != source.EnvironmentKey || target.Access != source.Access || target.CredentialGeneration != 1 {
				t.Fatalf("fresh binding lost identity/rights/scope: %+v, %v", target, err)
			}
			opened, err := secretbox.Open(identity, prepared.Secret.Ciphertext)
			if err != nil || len(opened) != 1 {
				t.Fatalf("target credential did not decrypt independently: %v", err)
			}
			connection, err := url.Parse(opened[target.EnvironmentKey])
			if err != nil {
				t.Fatal(err)
			}
			identityKey := strings.TrimPrefix(target.ProviderIdentityID, "identity-")
			password, _ := connection.User.Password()
			if connection.Hostname() != target.DatabaseProviderResourceID+".db.example.com" || connection.User.Username() != "u_"+identityKey || password != "password-"+identityKey || connection.Query().Get("sslmode") != "require" || prepared.Secret.Kid != identity.Recipient().String() || len(prepared.Secret.ValueHash) != 16 {
				t.Fatal("sealed credential addresses wrong provider placement or identity")
			}
			if _, err := srv.managedPostgresBindings.Get(ctx, target.AccountID, target.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
				t.Fatalf("prepared binding became customer-visible: %v", err)
			}
			good := state.ProjectEnvironmentClonePostgresBindingRequest{SourceBindingID: source.ID, TargetBindingID: target.ID, ProviderIdentityID: target.ProviderIdentityID, CredentialRef: target.CredentialRef, Secret: prepared.Secret, MaxSecretsPerApp: 1}
			if replay, err := store.PrepareProjectEnvironmentClonePostgresBinding(ctx, lease, good); err != nil || replay.Hash != prepared.Hash {
				t.Fatalf("prepared replay required new quota: %v", err)
			}
			if _, err := pool.Exec(ctx, "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", target.ID, []byte("altered-preparation")); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, source.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("altered envelope accepted: %v", err)
			}
			if _, err := pool.Exec(ctx, "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", target.ID, prepared.Secret.Ciphertext); err != nil {
				t.Fatal(err)
			}
		}
		if after, ok, err := store.AppRuntimeConfigChangedAt(ctx, view.AppID); err != nil || !ok || !after.Equal(stamps[view.AppID]) {
			t.Fatalf("private preparation invalidated production runtime: %v", err)
		}
	}
	oldBindings := srv.managedPostgresBindings
	srv.managedPostgresBindings = nil
	setSecretRecipient, hostHMACKey = nil, nil
	if replayed, replayedIDs, n, err := srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease); err != nil || n != count || !reflect.DeepEqual(replayedIDs, ids) || replayed.Operation.Revision != lease.Operation.Revision || len(provider.issued) != beforeIssues+3 {
		t.Fatalf("completed replay depended on current registry/keys: %v", err)
	}
	srv.managedPostgresBindings = oldBindings
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := srv.prepareProjectEnvironmentClonePostgresBindings(ctx, lease); !errors.Is(err, state.ErrConflict) || len(provider.issued) != beforeIssues+3 {
		t.Fatalf("stale worker reissued credentials: %v", err)
	}
	resumed, resumedIDs, n, err := srv.prepareProjectEnvironmentClonePostgresBindings(ctx, replacement)
	if err != nil || n != count || !reflect.DeepEqual(resumedIDs, ids) || len(provider.issued) != beforeIssues+3 {
		t.Fatalf("takeover did not reuse prepared credentials: %v", err)
	}
	capturedClonePostgresMaterializationContract(t, store, pool, resumed, resumedIDs, n)
	return resumed
}
