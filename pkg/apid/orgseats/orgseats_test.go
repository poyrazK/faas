package orgseats_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgseats"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHasSeats(t *testing.T) {
	for plan, want := range map[api.Plan]bool{
		api.PlanFree: false, api.Plan(""): false, api.Plan("legacy"): false,
		api.PlanHobby: true, api.PlanPro: true, api.PlanScale: true,
	} {
		if got := orgseats.HasSeats(state.Org{Plan: plan}); got != want {
			t.Errorf("HasSeats(%q) = %v, want %v", plan, got, want)
		}
	}
}

func TestNoSeatsProblemWording(t *testing.T) {
	free := orgseats.NoSeatsProblem(state.Org{Slug: "acme", Plan: api.PlanFree})
	if free.Code != api.CodeOrgInvitationCapExceeded || free.Fix != "gregale --org acme billing plan hobby" {
		t.Fatalf("Free problem = %+v", free)
	}
	unknown := orgseats.NoSeatsProblem(state.Org{Slug: "acme"})
	if unknown.Detail == free.Detail || unknown.Detail == "" {
		t.Fatalf("unknown-plan detail = %q, want its own wording", unknown.Detail)
	}
}
