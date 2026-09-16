// adr: 171
package sched

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func (f *fakeRouterVMM) ExecuteExecution(_ context.Context, _ string, _ executionproto.Request) (executionproto.Result, error) {
	return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`true`)}, nil
}

func TestVMMRouter_ExecuteExecutionRoutesOptionalCapability(t *testing.T) {
	r := NewVMMRouter([]ComputeNodeInfo{{ID: "node-a", TargetURL: "unix:///node-a.sock"}}, func(context.Context, string, *tls.Config) (VMM, error) {
		return &fakeRouterVMM{}, nil
	}, nil)
	result, err := r.ExecuteExecution(context.Background(), "node-a", "exec-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode22,
		Source:      "1",
		Input:       json.RawMessage(`null`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != "true" {
		t.Fatalf("result = %+v", result)
	}
}
