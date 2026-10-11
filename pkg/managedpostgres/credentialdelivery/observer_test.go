package credentialdelivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type observerSecret struct {
	row *state.AppSecret
	err error
}

func (s observerSecret) GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error) {
	return s.row, s.err
}

func TestObserverRejectsCredentialSubstitution(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	binding := managedpostgres.Binding{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1}
	hmac := []byte(strings.Repeat("h", 32))
	m := managedpostgres.CredentialMaterial{ProviderIdentityID: "identity", Username: "runtime", Password: "secret", Database: "app", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "example.test", Port: 5432}}}
	sealed, err := Seal(binding, m, identity.Recipient(), hmac)
	if err != nil {
		t.Fatal(err)
	}
	row := state.AppSecret{AccountID: binding.AccountID, AppID: binding.AppID, Scope: binding.Scope, Key: binding.EnvironmentKey, ManagedPostgresBindingID: binding.ID, ManagedCredentialRef: sealed.Ref, ManagedCredentialGeneration: 1, Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash}
	observer := Observer{Store: observerSecret{row: &row}, Identity: identity, HMACKey: hmac}
	if _, err := observer.URI(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*state.AppSecret){
		"foreign account": func(r *state.AppSecret) { r.AccountID = uuid.NewString() },
		"foreign app":     func(r *state.AppSecret) { r.AppID = uuid.NewString() },
		"scope":           func(r *state.AppSecret) { r.Scope = "preview" },
		"key":             func(r *state.AppSecret) { r.Key = "OTHER_URL" },
		"foreign binding": func(r *state.AppSecret) { r.ManagedPostgresBindingID = uuid.NewString() },
		"old generation":  func(r *state.AppSecret) { r.ManagedCredentialGeneration = 0 },
		"hash":            func(r *state.AppSecret) { r.ValueHash = "wrong" },
		"ciphertext":      func(r *state.AppSecret) { r.Ciphertext = []byte("wrong") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := row
			mutate(&changed)
			o := observer
			o.Store = observerSecret{row: &changed}
			if _, err := o.URI(context.Background(), binding); err == nil {
				t.Fatal("accepted substituted credential")
			}
		})
	}
	observer.Store = observerSecret{err: state.ErrNotFound}
	if err := observer.Deleted(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	observer.Store = observerSecret{err: errors.New("store unavailable")}
	if err := observer.Deleted(context.Background(), binding); err == nil {
		t.Fatal("store failure proved secret deletion")
	}
}
