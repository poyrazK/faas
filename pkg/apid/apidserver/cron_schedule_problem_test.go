package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// An invalid cron schedule names the parser's reason; it used to say
// "expected 5-field cron expression" even for five-field input (H10-68).
func TestCreateCronInvalidScheduleSaysWhy(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "cron-why")
	for schedule, want := range map[string]string{
		"61 * * * *":  "above maximum (59)",
		"0 0 30 2 *":  "never fires",
		"*/0 * * * *": "positive number",
		"* * * *":     "found 4",
	} {
		rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: schedule}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, want 400; body=%s", schedule, rec.Code, rec.Body.String())
		}
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatalf("%q: decode: %v", schedule, err)
		}
		if problem.Code != api.CodeCronInvalid || !strings.Contains(problem.Detail, want) || strings.Contains(problem.Detail, "sched:") {
			t.Fatalf("%q: problem = %s %q, want cron_invalid naming %q", schedule, problem.Code, problem.Detail, want)
		}
	}
}
