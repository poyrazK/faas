// adr: 20261026150000 — sealing a credential records its database's endpoints.
package credentialdelivery_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

type secretStore struct{}

func (secretStore) PutManagedPostgresSecret(context.Context, state.AppSecret) error { return nil }
func (secretStore) DeleteManagedPostgresSecret(context.Context, string) error       { return nil }

type recordingStore struct {
	secretStore
	recorded []string
	err      error
}

func (r *recordingStore) RecordManagedPostgresDatabaseEndpoint(_ context.Context, databaseID, host string, port int) error {
	r.recorded = append(r.recorded, databaseID+" "+host+" "+strconv.Itoa(port))
	return r.err
}

func sealFixture(t *testing.T, store credentialdelivery.SecretStore) (*credentialdelivery.Sink, managedpostgres.Binding, managedpostgres.CredentialMaterial) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := credentialdelivery.New(store, identity.Recipient, func() []byte { return []byte("fixture-hmac-key-32-bytes-long!!") })
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{ID: "binding-1", AccountID: "account-1", AppID: "app-1", DatabaseID: "database-1", Scope: "production",
		EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialDataAPI, CredentialGeneration: 1}
	material := managedpostgres.CredentialMaterial{ProviderIdentityID: "role-1", Username: "role_1", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointPooled, Host: "EP-1-pooler.example.com", Port: 5432},
			{Role: managedpostgres.EndpointDirect, Host: "ep-1.example.com", Port: 5432},
			{Role: managedpostgres.EndpointReadOnly, Host: "198.51.100.4", Port: 5432},
		}}
	return sink, binding, material
}

func TestSealCredentialRecordsTheDatabaseEndpoints(t *testing.T) {
	store := &recordingStore{}
	sink, binding, material := sealFixture(t, store)
	if _, err := sink.SealCredential(t.Context(), binding, material); err != nil {
		t.Fatalf("SealCredential: %v", err)
	}
	// Host names only: the resolver opens ports to resolved names, never to
	// an address literal. The store normalizes case.
	if want := []string{"database-1 EP-1-pooler.example.com 5432", "database-1 ep-1.example.com 5432"}; !reflect.DeepEqual(store.recorded, want) {
		t.Fatalf("recorded = %v, want %v", store.recorded, want)
	}
	store.err, store.recorded = errors.New("database down"), nil
	if _, err := sink.SealCredential(t.Context(), binding, material); err == nil {
		t.Fatal("a failed record must fail the seal so the caller retries")
	}
	plain, _, _ := sealFixture(t, secretStore{})
	if _, err := plain.SealCredential(t.Context(), binding, material); err != nil {
		t.Fatalf("a store without endpoint records still seals: %v", err)
	}
}
