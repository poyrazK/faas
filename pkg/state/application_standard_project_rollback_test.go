package state

import (
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStandardProjectRollbackRestoresServiceAddresses(t *testing.T) {
	m := NewMemStore()
	owner, err := m.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	indices, cursors := maps.Clone(m.serviceAddressIndex), maps.Clone(m.serviceAddressCursors)
	id := uuid.NewString()
	apps := []App{{ID: id, Slug: "first", WorkloadName: "first"}, {ID: id, Slug: "second", WorkloadName: "second"}}
	_, _, _, err = m.ApplyProjectPlan(t.Context(), Project{AccountID: owner.Account.ID, Slug: "rollback"}, apps, nil, api.MustLimitsFor(api.PlanPro))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate app identity did not roll back: %v", err)
	}
	if !maps.Equal(indices, m.serviceAddressIndex) || !maps.Equal(cursors, m.serviceAddressCursors) || len(m.applicationStandardEnrollments) != 0 {
		t.Fatal("project rollback retained service address or enrollment state")
	}
}
