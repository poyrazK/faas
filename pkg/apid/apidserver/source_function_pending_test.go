package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A push's deploy activity lists the settings it left to `gregale deploy`
// and keeps the activity's existing fields (ADR-20261108160000 §5).
func TestWithPendingFunctionSteps(t *testing.T) {
	activity := &state.OrgActivity{Data: []byte(`{"source":"github","phase":"requested"}`)}
	steps := gregalemanifest.FunctionSpec{
		Webhooks:       []gregalemanifest.FunctionWebhook{{Name: "stripe"}},
		SecretsFromEnv: []string{"API_KEY"},
	}.RepositoryDeferredSteps()
	apidserver.WithPendingFunctionSteps(activity, steps)
	var data struct {
		Source  string `json:"source"`
		Pending []struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"pending_steps"`
		Command string `json:"pending_steps_command"`
	}
	if err := json.Unmarshal(activity.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Source != "github" || len(data.Pending) != 2 || data.Pending[0].Kind != "webhook" || data.Pending[1].Detail != "API_KEY" || data.Command != "gregale deploy" {
		t.Fatalf("activity data = %s", activity.Data)
	}
	apidserver.WithPendingFunctionSteps(nil, steps) // a push without an activity is a no-op
}
