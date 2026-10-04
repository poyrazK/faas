package globaldirectory

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRegionalOrganizationBindingSealedIdentity(t *testing.T) {
	k := delegationKeys(t)
	a := App{ID: uuid.NewString(), OwnerID: uuid.NewString(), Slug: "sealed-global"}
	b := Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "sealed-regional"}
	target := Target{Binding: b, GlobalAppID: a.ID, APIBase: "https://fsn.example.com", CellEnabled: true, RegistryGeneration: 1}
	personal, err := k.Mint(a, target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload(personal, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "RegionalOrgID") || !strings.Contains(string(data), "AccountID") {
		t.Fatal("legacy personal ciphertext shape changed")
	}
	target.RegionalOrgID = uuid.NewString()
	g, err := k.Mint(a, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = k.Open(g); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"", uuid.NewString()} {
		changed := g
		changed.Binding.RegionalOrgID = org
		if _, err = k.Open(changed); !errors.Is(err, ErrDelegationSealed) {
			t.Fatalf("organization anchor tampering accepted: %v", err)
		}
	}
}
