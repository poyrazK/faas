// adr: 171
package sched_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func (f *fakeVMM) ExecuteExecution(_ context.Context, _ string, _ executionproto.Request) (executionproto.Result, error) {
	exitCode := 0
	return executionproto.Result{
		Status:          api.ExecutionStatusSucceeded,
		Result:          json.RawMessage(`{"ok":true}`),
		OutputTruncated: true,
		ExitCode:        &exitCode,
		Stdout:          []byte("out"),
		Stderr:          []byte("err"),
		Usage:           api.ExecutionUsage{WallTimeMS: 9, CPUTimeMS: 4, PeakMemoryMB: 32},
	}, nil
}

func TestVMMClient_ExecuteExecution(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	result, err := c.ExecuteExecution(context.Background(), "exec-vm-1", executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: "exec-1",
		Runtime:     api.ExecutionRuntimeNode24,
		Source:      "2 + 2",
		Input:       json.RawMessage(`{"value":2}`),
		TimeoutMS:   1000,
		MaxOutput:   1024,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("ExecuteExecution: %v", err)
	}
	if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"ok":true}` {
		t.Fatalf("result status/value = %q/%s", result.Status, result.Result)
	}
	if !result.OutputTruncated || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("result terminal fields = %+v", result)
	}
	if result.Usage.WallTimeMS != 9 || result.Usage.CPUTimeMS != 4 || result.Usage.PeakMemoryMB != 32 {
		t.Fatalf("result usage = %+v", result.Usage)
	}
}
