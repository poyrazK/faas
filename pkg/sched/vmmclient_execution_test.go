// adr: 171
package sched_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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

type artifactExecutionVMM struct{ *fakeVMM }

type profileExecutionVMM struct{ *fakeVMM }

func (f *profileExecutionVMM) ExecuteExecution(_ context.Context, _ string, req executionproto.Request) (executionproto.Result, error) {
	if req.Profile != api.ExecutionProfilePythonDataV1 || req.Runtime != api.ExecutionRuntimePython313 || req.Version != executionproto.ProfileVersion {
		return executionproto.Result{}, fmt.Errorf("profile lost in transport: %+v", req)
	}
	return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null")}, nil
}

func (f *profileExecutionVMM) ExecuteExecutionWithOutput(ctx context.Context, instance string, req executionproto.Request, _ executionproto.OutputReceiver) (executionproto.Result, error) {
	return f.ExecuteExecution(ctx, instance, req)
}

func TestVMMClientExecutionProfileSurvivesUnaryAndStreamingTransport(t *testing.T) {
	c := newClient(t, &profileExecutionVMM{fakeVMM: &fakeVMM{}})
	req := executionproto.Request{Version: executionproto.ProfileVersion, Profile: api.ExecutionProfilePythonDataV1, ExecutionID: "data", Runtime: api.ExecutionRuntimePython313, Source: "def main(input, context): return input", Input: json.RawMessage("null"), TimeoutMS: 1000, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone}
	if _, err := c.ExecuteExecution(context.Background(), "exec-vm-1", req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExecuteExecutionWithOutput(context.Background(), "exec-vm-1", req, nil); err != nil {
		t.Fatal(err)
	}
}

func (f *artifactExecutionVMM) ExecuteExecution(_ context.Context, _ string, req executionproto.Request) (executionproto.Result, error) {
	if req.Version != executionproto.ArtifactVersion || req.Entrypoint != "main.mjs" || len(req.Files) != 1 || string(req.Files[0].Content) != "export default () => null" || len(req.OutputFiles) != 1 || req.OutputFiles[0] != "report.bin" {
		return executionproto.Result{}, fmt.Errorf("bundle/output selection lost: %+v", req)
	}
	content := []byte(strings.Repeat("x", 5*1024*1024))
	digest := sha256.Sum256(content)
	return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null"), Artifacts: []api.ExecutionArtifact{{Name: "report.bin", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:])}}}, nil
}

func (f *artifactExecutionVMM) ExecuteExecutionWithOutput(ctx context.Context, instance string, req executionproto.Request, _ executionproto.OutputReceiver) (executionproto.Result, error) {
	return f.ExecuteExecution(ctx, instance, req)
}

func TestVMMClientExecutionArtifactsSurviveUnaryAndStreamingTransport(t *testing.T) {
	// adr:382 — selection, bundle and bounded outputs cross both real gRPC paths.
	c := newClient(t, &artifactExecutionVMM{fakeVMM: &fakeVMM{}})
	req := executionproto.Request{Version: executionproto.ArtifactVersion, ExecutionID: "exports", Runtime: api.ExecutionRuntimeNode24, Entrypoint: "main.mjs", Files: []api.ExecutionFile{{Path: "main.mjs", Content: []byte("export default () => null")}}, OutputFiles: []string{"report.bin"}, Input: json.RawMessage("null"), TimeoutMS: 1000, MaxOutput: api.ExecutionOutputHardMaxBytes, NetworkMode: api.ExecutionNetworkNone}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var result executionproto.Result
			var err error
			if stream {
				result, err = c.ExecuteExecutionWithOutput(context.Background(), "exec-vm-1", req, nil)
			} else {
				result, err = c.ExecuteExecution(context.Background(), "exec-vm-1", req)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !api.ExecutionArtifactsMatch(req.OutputFiles, result.Artifacts) || len(result.Artifacts[0].Content) != 5*1024*1024 {
				t.Fatalf("artifacts dropped: %+v", result)
			}
			if err := result.Validate(req.MaxOutput); err != nil {
				t.Fatal(err)
			}
		})
	}
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
