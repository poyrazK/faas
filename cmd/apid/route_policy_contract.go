package main

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func routePolicyInventory(contract *state.RoutePolicyContract, plan api.Plan) routerequirements.CoverageInventory {
	inventory := routerequirements.CoverageInventory{Status: "unavailable", Code: "candidate_inventory_unavailable", Source: "captured_candidate_contract"}
	if plan.OpenAPIDocsPerDeployment() <= 0 {
		inventory.Code = "captured_contract_plan_unavailable"
		return inventory
	}
	if contract == nil {
		return inventory
	}
	inventory.Deployment, inventory.SHA256 = contract.DeploymentID, contract.SHA256
	if contract.Truncated {
		inventory.Code = "candidate_contract_truncated"
		return inventory
	}
	if len(contract.Doc) == 0 || len(contract.Doc) > state.OpenAPIDocMaxBytes || len(contract.Doc) > plan.OpenAPIDocMaxBytes() || !json.Valid(contract.Doc) || len(contract.SHA256) != 64 {
		return inventory
	}
	spec, err := openapidiff.LoadBytes(contract.Doc)
	if err != nil {
		inventory.Code = "candidate_contract_invalid"
		return inventory
	}
	return routerequirements.CandidateInventory(spec, contract.DeploymentID, contract.SHA256)
}
