package state_test

import (
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStandardProjectRollbackRestoresServiceAddresses(t *testing.T) {
	m := state.NewMemStore()
	owner, err := m.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	indices, cursors := maps.Clone((*m.ServiceAddressIndex())), maps.Clone((*m.ServiceAddressCursors()))
	id := uuid.NewString()
	apps := []state.App{{ID: id, Slug: "first", WorkloadName: "first"}, {ID: id, Slug: "second", WorkloadName: "second"}}
	_, _, _, err = m.ApplyProjectPlan(t.Context(), state.Project{AccountID: owner.Account.ID, Slug: "rollback"}, apps, nil, api.MustLimitsFor(api.PlanPro))
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate app identity did not roll back: %v", err)
	}
	if !maps.Equal(indices, (*m.ServiceAddressIndex())) || !maps.Equal(cursors, (*m.ServiceAddressCursors())) || len((*m.ApplicationStandardEnrollments())) != 0 {
		t.Fatal("project rollback retained service address or enrollment state")
	}
}
