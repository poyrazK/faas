// adr: 20261104173000
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestServiceAbortNamedIdentity(t *testing.T) {
	project, environment := uuid.NewString(), uuid.NewString()
	for _, c := range []struct {
		scope, project, environment string
		valid                       bool
	}{
		{"default", "", "", true}, {"", "", "", true}, {"staging", project, environment, true},
		{"default", project, environment, false}, {"staging", "", environment, false}, {"staging", project, "", false},
		{"staging", "garbage", environment, false}, {"../staging", project, environment, false},
	} {
		if ValidServiceAbortScope(c.scope, c.project, c.environment) != c.valid {
			t.Fatalf("scope shape: %+v", c)
		}
	}
	review := AppReleaseReview{AppID: uuid.NewString(), ProjectID: project, EnvironmentID: environment, Scope: "staging", ConfigurationHash: "review"}
	req := RecoverRolloutRequest{Action: "abort", DeploymentID: uuid.NewString(), ExpectedPredecessorDeploymentID: uuid.NewString(), ExpectedReview: &review}
	hash := ServiceAbortRequestHash(review.AppID, req)
	alias := review
	alias.ProjectID, alias.EnvironmentID = strings.ReplaceAll(project, "-", ""), strings.ReplaceAll(environment, "-", "")
	req.ExpectedReview = &alias
	if ServiceAbortRequestHash(review.AppID, req) != hash {
		t.Fatal("UUID spelling changed named identity")
	}
	alias.EnvironmentID = uuid.NewString()
	if ServiceAbortRequestHash(review.AppID, req) == hash {
		t.Fatal("environment replacement not bound")
	}
	alias = review
	alias.ProjectID = uuid.NewString()
	if ServiceAbortRequestHash(review.AppID, req) == hash {
		t.Fatal("project replacement not bound")
	}
	wire, err := json.Marshal(ServiceAbortReceipt{Scope: "default"})
	if err != nil || strings.Contains(string(wire), "project_id") || strings.Contains(string(wire), "environment_id") {
		t.Fatalf("default receipt changed optional identity shape: %s %v", wire, err)
	}
}
