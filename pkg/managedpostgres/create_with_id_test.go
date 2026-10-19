package managedpostgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCreateWithIDRecoversSameMirrorAndRejectsNameAdoption(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	request := CreateRequest{AccountID: "account-a", Name: "saved-setup-mirror", Spec: testSpec()}
	id := uuid.NewString()
	first, err := service.CreateWithID(t.Context(), request, id)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.CreateWithID(t.Context(), request, id)
	if err != nil || first.ID != id || retry.ID != id || provider.provisionCalls != 1 {
		t.Fatalf("duplicate resource: %+v %+v %v", first, retry, err)
	}
	if _, err := service.CreateWithID(t.Context(), request, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatalf("adopted another setup's mirror: %v", err)
	}
	if provider.provisionCalls != 1 {
		t.Fatal("name conflict reached the provider")
	}
	if _, err := service.CreateWithID(t.Context(), request, "bad-id"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad identity accepted: %v", err)
	}
}
