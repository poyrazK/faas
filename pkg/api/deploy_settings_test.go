package api

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestDeploySettingsConditionValidation(t *testing.T) {
	for _, mode := range []string{"valid", "app", "deployment", "empty", "unknown", "type", "uncovered", "unrelated", "no-patch"} {
		t.Run(mode, func(t *testing.T) {
			value := 30
			condition := &AppDeploySettingsCondition{ExpectedAppID: uuid.NewString(), ExpectedDeploymentID: uuid.NewString(),
				ExpectedSettings: map[string]json.RawMessage{"request_timeout_s": json.RawMessage(`0`)}}
			req := UpdateAppRequest{RequestTimeoutS: &value, DeploySettingsCondition: condition}
			switch mode {
			case "app":
				condition.ExpectedAppID = "invalid"
			case "deployment":
				condition.ExpectedDeploymentID = ""
			case "empty":
				condition.ExpectedSettings = nil
			case "unknown":
				condition.ExpectedSettings["env"] = json.RawMessage(`{"TOKEN":"secret"}`)
			case "type":
				condition.ExpectedSettings["request_timeout_s"] = json.RawMessage(`"bad"`)
			case "uncovered":
				condition.ExpectedSettings = map[string]json.RawMessage{"restart_policy": json.RawMessage(`"always"`)}
			case "unrelated":
				req.RAMMB = &value
			case "no-patch":
				req.RequestTimeoutS = nil
			}
			if err := condition.Validate(req); (err == nil) != (mode == "valid") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestDeploySettingsSnapshotExcludesEnvironment(t *testing.T) {
	values := DeploySettingsSnapshot(AppResponse{Manifest: AppManifest{Env: map[string]string{"TOKEN": "fixture-secret"}}})
	if _, exists := values["env"]; exists {
		t.Fatal("snapshot contains environment")
	}
	if !DeploySettingEqual("retry_policy", json.RawMessage(`null`), json.RawMessage(`{}`)) {
		t.Fatal("cleared retry policy should equal missing policy")
	}
	if DeploySettingEqual("scaling_policy", json.RawMessage(`{"target":{"name":"a"}}`), json.RawMessage(`{"target":{"name":"b"}}`)) {
		t.Fatal("different scaling metrics compare equal")
	}
}
