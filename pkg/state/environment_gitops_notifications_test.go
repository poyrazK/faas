// adr: 1155 — candidate assessment and customer status feedback contracts.
package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitOpsNotificationBoundsAndRedactsDetailsWithoutChangingReport(t *testing.T) {
	plan := api.EnvironmentGitOpsPlan{}
	for i := 0; i <= api.EnvironmentGitOpsNotificationDetailsMax; i++ {
		plan.Changes = append(plan.Changes, api.EnvironmentGitOpsChange{Resource: "workload/api", Path: fmt.Sprintf("variables/V%d", i), Action: "write",
			Before: json.RawMessage(`"PRIVATE_CURRENT_VALUE"`), After: json.RawMessage(`"PRIVATE_DESIRED_VALUE"`)})
		plan.BlockingReasons = append(plan.BlockingReasons, fmt.Sprintf("field-%d: ownership conflict", i))
	}
	source := EnvironmentGitSource{ID: "source", EnvironmentSlug: "staging", Generation: 1}
	next, body, id, err := prepareEnvironmentGitNotification(nil, source, "shop", "drift", "blocked", "run", "", plan, time.Now())
	var payload api.EnvironmentGitOpsChangedWebhookPayload
	if err != nil || id == "" || json.Unmarshal(body, &payload) != nil || !payload.DetailsTruncated ||
		len(payload.Changes) != api.EnvironmentGitOpsNotificationDetailsMax || len(payload.BlockingReasons) != api.EnvironmentGitOpsNotificationDetailsMax || strings.Contains(string(body), "PRIVATE_") {
		t.Fatalf("unsafe or unbounded alert: %s %v", body, err)
	}
	if len(plan.Changes[0].Before) == 0 || len(plan.Changes[0].After) == 0 {
		t.Fatal("alert redaction changed the stored report")
	}
	if _, _, repeat, err := prepareEnvironmentGitNotification(next, source, "shop", "drift", "blocked", "run", "", plan, time.Now()); err != nil || repeat != "" {
		t.Fatalf("unchanged status repeated: %s %v", repeat, err)
	}
	source.Generation++
	if _, _, renewed, err := prepareEnvironmentGitNotification(next, source, "shop", "drift", "blocked", "run", "", plan, time.Now()); err != nil || renewed == "" {
		t.Fatalf("new generation's unresolved blockers were silent: %s %v", renewed, err)
	}
}
