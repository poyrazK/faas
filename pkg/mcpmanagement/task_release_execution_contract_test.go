package mcpmanagement

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTaskApplyReceiptIdentityContract(t *testing.T) {
	record := taskIntentReceipt(t)
	record.Status = "approved"
	record.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("d", 64), BindingsSHA256: strings.Repeat("e", 64), AuthorizerKeyID: uuid.NewString(), State: "authorized", AuthorizedAt: record.CreatedAt.Add(time.Second)}
	body, _ := json.Marshal(record)
	args := applyTaskExecutionArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, AllowToolExecution: true}
	bound, err := bindToolPlanID("gregale_apply_task_tool_deploy", args, body)
	if err != nil || string(bound) != string(body) || strings.Contains(string(bound), `"plan_id"`) {
		t.Fatal("Task receipt was rewritten as a generic deployment plan", err)
	}

	m := &management{}
	verified, err := m.addToolVerification("gregale_apply_task_tool_deploy", args, bound)
	if err != nil || string(verified) != string(body) || strings.Contains(string(verified), `"verification"`) {
		t.Fatal("Task receipt acquired unrelated generic verification", err)
	}
	generic, err := m.addToolVerification("gregale_apply_operation", applyArgs{PlanID: "reviewed"}, json.RawMessage(`{"plan_id":"reviewed"}`))
	if err != nil || !strings.Contains(string(generic), `"verification"`) {
		t.Fatal("generic apply lost verification", err)
	}
	for _, field := range []string{"intent", "operation", "digest"} {
		t.Run(field, func(t *testing.T) {
			request := args
			response := record
			switch field {
			case "intent":
				request.IntentID = strings.Repeat("1", 64)
			case "operation":
				request.OperationID = strings.Repeat("1", 64)
			case "digest":
				response.IntentSHA256 = strings.Repeat("1", 64)
			}
			data, _ := json.Marshal(response)
			if _, err := bindToolPlanID("gregale_apply_task_tool_deploy", request, data); err == nil {
				t.Fatal("foreign Task identity accepted")
			}
		})
	}
}
