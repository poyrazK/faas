package datapipelines

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestPublicSetupDoesNotExposePrivateAPIPlan(t *testing.T) {
	job := datapipeline.Setup{ID: "job", AccountID: "private-account", DatabaseID: "mirror", PipelineID: "pipeline", State: "blocked", Phase: "configuring_api", Reason: "data_api_deployment_failed", Request: datapipeline.SetupRequest{Name: "orders"}, DataAPIPlan: datapipeline.DataAPIPlan{AppID: "app", Issuer: "private-issuer", JWKSURL: "private-jwks", Audience: "private-audience"}}
	raw, err := json.Marshal(publicSetup(job))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-account", "private-issuer", "private-jwks", "private-audience", "DataAPIPlan", "MirrorPlan", "LeaseToken"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private plan escaped", string(raw))
		}
	}
	if !strings.Contains(string(raw), "Review deployment logs") || !strings.Contains(string(raw), `"data_api_app_id":"app"`) {
		t.Fatal("recovery guidance or resource ID missing", string(raw))
	}
}
