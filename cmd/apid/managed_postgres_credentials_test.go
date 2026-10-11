// adr: 650 — sealed Data API bindings retain credential custody and recovery.
package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedPostgresCredentialSinkSealsURLAndRecoversUncommittedPut(t *testing.T) {
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialDataAPI} {
		t.Run(string(access), func(t *testing.T) { testManagedPostgresCredentialSinkSealsURL(t, access) })
	}
}

func testManagedPostgresCredentialSinkSealsURL(t *testing.T, access managedpostgres.CredentialAccess) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	sink, err := newAppSecretCredentialSink(
		store,
		func() *age.X25519Recipient { return identity.Recipient() },
		func() []byte { return []byte("0123456789abcdef0123456789abcdef") },
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{
		ID: "binding-a", AccountID: "account-a", AppID: "app-a", Scope: "production",
		EnvironmentKey: "DATABASE_URL", Access: access, CredentialGeneration: 1,
	}
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "role/name", Password: "p@ss:/?#word", Database: "app/db", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointDirect, Host: "direct.db.example", Port: 5432},
			{Role: managedpostgres.EndpointPooled, Host: "pooler.db.example", Port: 6432},
		},
	}

	credentialRef, err := sink.Put(context.Background(), binding, material)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	again, err := sink.Put(context.Background(), binding, material)
	if err != nil || again != credentialRef {
		t.Fatalf("idempotent Put: ref=%q want=%q err=%v", again, credentialRef, err)
	}
	row, err := store.GetAppSecretInScope(context.Background(), binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey)
	if err != nil {
		t.Fatal(err)
	}
	if row.ManagedPostgresBindingID != binding.ID || row.ManagedCredentialRef != credentialRef || row.ManagedCredentialGeneration != 1 {
		t.Fatalf("ownership metadata = %+v", row)
	}
	if row.Kid != identity.Recipient().String() || row.ValueHash == "" {
		t.Fatalf("seal metadata: kid=%q value_hash=%q", row.Kid, row.ValueHash)
	}
	if strings.Contains(string(row.Ciphertext), material.Password) {
		t.Fatal("plaintext password appears in stored ciphertext")
	}
	envelope, err := secretbox.Open(identity, row.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(envelope[binding.EnvironmentKey])
	if err != nil {
		t.Fatal(err)
	}
	password, ok := parsed.User.Password()
	if !ok || parsed.User.Username() != material.Username || password != material.Password || parsed.Hostname() != "pooler.db.example" || parsed.Port() != "6432" || parsed.EscapedPath() != "/app%2Fdb" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("sealed connection URL = %q", envelope[binding.EnvironmentKey])
	}

	// Simulate a crash after Put but before FinishBindingProvision: the
	// catalog has no credential_ref, yet Delete derives the same opaque ref.
	if err := sink.Delete(context.Background(), binding); err != nil {
		t.Fatalf("Delete without committed credential ref: %v", err)
	}
	if _, err := store.GetAppSecretInScope(context.Background(), binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("secret remains after delete: %v", err)
	}
}

func TestManagedPostgresCredentialSinkDeleteRecoversDuringPartialRotation(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	sink, err := newAppSecretCredentialSink(
		store,
		func() *age.X25519Recipient { return identity.Recipient() },
		func() []byte { return []byte("0123456789abcdef0123456789abcdef") },
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{
		ID: "binding-rotation", AccountID: "account-a", AppID: "app-a", Scope: "production",
		EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1,
	}
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "role", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "pool.db.example", Port: 5432}},
	}
	oldRef, err := sink.Put(context.Background(), binding, material)
	if err != nil {
		t.Fatal(err)
	}
	rotating := binding
	rotating.CredentialGeneration = 2
	rotating.RotationPreviousGeneration = 1
	rotating.CredentialRef = oldRef // crash after rotation reservation, before the replacement secret write
	if err := sink.Delete(context.Background(), rotating); err != nil {
		t.Fatalf("delete during partial rotation: %v", err)
	}
	if _, err := store.GetAppSecretInScope(context.Background(), binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old-generation secret survived partial-rotation delete: %v", err)
	}
}

func TestManagedPostgresConnectionURLUsesAccessSpecificEndpoint(t *testing.T) {
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "reader", Password: "secret", Database: "app", TLSMode: "verify-full",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointDirect, Host: "primary.example.test", Port: 5432},
			{Role: managedpostgres.EndpointReadOnly, Host: "replica.example.test", Port: 5432},
		},
	}
	// Migration tools always connect directly, even when pooling is available.
	material.Endpoints = append(material.Endpoints, managedpostgres.Endpoint{Role: managedpostgres.EndpointPooled, Host: "pool.example.test", Port: 6432})
	migrationURL, err := managedPostgresConnectionURL(managedpostgres.CredentialMigration, material)
	if err != nil {
		t.Fatal(err)
	}
	migrationParsed, err := url.Parse(migrationURL)
	if err != nil || migrationParsed.Hostname() != "primary.example.test" {
		t.Fatalf("migration URL endpoint=%s err=%v", migrationParsed.Hostname(), err)
	}
	pooledOnly := material
	pooledOnly.Endpoints = material.Endpoints[2:]
	if _, err := managedPostgresConnectionURL(managedpostgres.CredentialMigration, pooledOnly); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("pooled-only migration accepted: %v", err)
	}
	material.Endpoints = material.Endpoints[:2]
	value, err := managedPostgresConnectionURL(managedpostgres.CredentialReadOnly, material)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() != "replica.example.test" {
		t.Fatalf("read-only URL = %q, %v", value, err)
	}
	material.Endpoints = material.Endpoints[:1]
	for _, expectedHost := range []string{"primary.example.test", "pool.example.test"} {
		if expectedHost == "pool.example.test" {
			material.Endpoints = append(material.Endpoints, managedpostgres.Endpoint{Role: managedpostgres.EndpointPooled, Host: expectedHost, Port: 6432})
		}
		value, err := managedPostgresConnectionURL(managedpostgres.CredentialReadOnly, material)
		if err != nil {
			t.Fatal("read-only role requires no replica", err)
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() != expectedHost {
			t.Fatal("wrong read-only fallback endpoint", err)
		}
	}
}

func TestManagedPostgresConnectionURLFailsClosedOnUnrepresentableTrustOrHost(t *testing.T) {
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "role", Password: "secret", Database: "app", TLSMode: "verify-full",
		RootCertificatePEM: "-----BEGIN CERTIFICATE-----\nsecret\n-----END CERTIFICATE-----",
		Endpoints:          []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "db.example.test", Port: 5432}},
	}
	if _, err := managedPostgresConnectionURL(managedpostgres.CredentialReadWrite, material); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("root certificate was silently discarded: %v", err)
	}
	material.RootCertificatePEM = ""
	material.Endpoints[0].Host = "db.example.test@attacker.example"
	if _, err := managedPostgresConnectionURL(managedpostgres.CredentialReadWrite, material); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("host injection accepted: %v", err)
	}
}

func TestManagedPostgresCredentialSinkFailsClosedWithoutSealKeys(t *testing.T) {
	sink, err := newAppSecretCredentialSink(state.NewMemStore(), func() *age.X25519Recipient { return nil }, func() []byte { return nil })
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{ID: "binding-a", AccountID: "account-a", AppID: "app-a", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1}
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: "provider-role-a", Username: "role", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "db.example.test", Port: 5432}},
	}
	if _, err := sink.Put(context.Background(), binding, material); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("missing seal keys = %v", err)
	}
}

// adr: 464 — sealing for cutover does not publish or replace app secrets.
func TestManagedPostgresCredentialSealerDoesNotPublish(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	sink, err := newAppSecretCredentialSink(store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("0123456789abcdef0123456789abcdef") })
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{ID: "future-binding", AccountID: "account-a", AppID: "app-a", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1}
	material := managedpostgres.CredentialMaterial{ProviderIdentityID: "target-role", Username: "runtime", Password: "private-stage-password", Database: "gregale", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "pool.example.test", Port: 5432}}}
	sealed, err := sink.SealCredential(context.Background(), binding, material)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed.Ciphertext) == 0 || sealed.Kid != identity.Recipient().String() || sealed.ValueHash == "" || sealed.Ref == "" || strings.Contains(string(sealed.Ciphertext), material.Password) {
		t.Fatal("stage envelope is invalid or plaintext")
	}
	envelope, err := secretbox.Open(identity, sealed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(envelope[binding.EnvironmentKey])
	if err != nil || parsed.Hostname() != "pool.example.test" {
		t.Fatal("staged envelope did not retain the pooled target endpoint")
	}
	if password, ok := parsed.User.Password(); !ok || password != material.Password {
		t.Fatal("staged envelope did not retain the target credential")
	}
	if _, err = store.GetAppSecretInScope(context.Background(), binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("sealing published a serving secret")
	}
}
