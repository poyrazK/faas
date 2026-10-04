package api

import (
	"encoding/json"
	"testing"
)

// Durable receipts hash the encoded normalized request. Existing file-based
// requests must keep their exact encoding after adding optional saved fields.
func TestRoutePolicyLegacyRequestEncoding(t *testing.T) {
	request := RoutePolicyApplyRequest{
		RoutePolicyPlanRequest: RoutePolicyPlanRequest{ConsolidateBudgets: true, DeploymentID: "deployment", Requirements: RouteRequirementsConfig{Version: 2}, ThrottleBurst: 20},
		ExpectedPlanSHA256:     "reviewed", Confirm: true,
	}
	body, err := json.Marshal(request)
	const previous = `{"consolidate_budgets":true,"deployment_id":"deployment","requirements":{"version":2},"throttle_burst":20,"expected_plan_sha256":"reviewed","confirm":true}`
	if err != nil || string(body) != previous {
		t.Fatalf("legacy receipt retry encoding changed: %s %v", body, err)
	}
}
