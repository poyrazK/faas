package executionproto

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestExtendedClassAndEnvironmentFailClosedOnOldProtocol(t *testing.T) {
	req := RequestFromResolvedExecution("task", api.ResolvedExecutionRequest{EnvironmentID: uuid.NewString(), Runtime: api.ExecutionRuntimeNode24, Source: "export default () => 1", Input: json.RawMessage("null"), Limits: api.ResolvedExecutionLimits{Class: api.ExecutionClassExtended, TimeoutMS: 300000, MaxOutputBytes: 1024}, Network: api.ExecutionNetworkPolicy{Mode: api.ExecutionNetworkNone}})
	if req.Version != ClassVersion || req.EnvironmentID == "" || req.Validate() != nil {
		t.Fatalf("new request %+v: %v", req, req.Validate())
	}
	req.Version = FileVersion
	if req.Validate() == nil {
		t.Fatal("old protocol accepted extended class")
	}
	req.Version = ClassVersion
	req.Class = ""
	if req.Validate() == nil {
		t.Fatal("long deadline without class")
	}
	req.Class = api.ExecutionClassExtended
	req.TimeoutMS = 300001
	if req.Validate() == nil {
		t.Fatal("unbounded deadline accepted")
	}
}
