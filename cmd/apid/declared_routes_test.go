package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidateUpdateApp_DeclaredRoutes(t *testing.T) {
	acct := state.Account{Plan: api.PlanPro}
	app := state.App{MaxConcurrency: 10}
	limits := api.MustLimitsFor(acct.Plan)

	valid := []api.DeclaredRoute{{Path: "/products", Methods: []string{"GET"}}, {Path: "/orders/{id}", Methods: []string{"GET", "POST"}}}
	req := &api.UpdateAppRequest{DeclaredRoutes: &valid}
	if prob := validateUpdateApp(req, acct, limits, app); prob != nil {
		t.Fatalf("valid declared routes rejected: %+v", prob)
	}

	bad := []api.DeclaredRoute{{Path: "wp-login.php", Methods: []string{"GET"}}}
	req = &api.UpdateAppRequest{DeclaredRoutes: &bad}
	if prob := validateUpdateApp(req, acct, limits, app); prob == nil {
		t.Fatal("invalid declared route accepted")
	}
}

func TestHasDeclaredRouteSourceUsesPostPatchList(t *testing.T) {
	existing := state.App{DeclaredRoutes: []state.DeclaredRoute{{Path: "/old", Methods: []string{"GET"}}}}

	if !hasDeclaredRouteSource(&api.UpdateAppRequest{}, existing) {
		t.Fatal("existing explicit routes should satisfy the contract")
	}
	empty := []api.DeclaredRoute{}
	if hasDeclaredRouteSource(&api.UpdateAppRequest{DeclaredRoutes: &empty}, existing) {
		t.Fatal("an explicit empty PATCH must clear the old list and require OpenAPI")
	}
	newRoutes := []api.DeclaredRoute{{Path: "/new", Methods: []string{"GET"}}}
	if !hasDeclaredRouteSource(&api.UpdateAppRequest{DeclaredRoutes: &newRoutes}, state.App{}) {
		t.Fatal("a non-empty PATCH list should satisfy the contract")
	}
}
