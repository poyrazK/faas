// adr: 171
package sched_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
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

const schedulerTestIntegrationID = "11111111-1111-4111-8111-111111111111"

type brokerExecutionVMM struct {
	*fakeVMM
	response executionproto.OutboundResponse
}

func (f *brokerExecutionVMM) ExecutionOutboundIdentity(instance, integrationID string) (string, string, string, error) {
	if instance != "exec-vm-1" || integrationID != schedulerTestIntegrationID {
		return "", "", "", fmt.Errorf("integration was not granted")
	}
	return "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444", nil
}

func (f *brokerExecutionVMM) ExecuteExecutionWithBroker(ctx context.Context, _ string, request executionproto.Request, receive executionproto.OutputReceiver, broker executionproto.OutboundCallFunc) (executionproto.Result, error) {
	if !request.OutboundEnabled {
		return executionproto.Result{}, fmt.Errorf("outbound helper enable flag was lost in transport")
	}
	if err := receive(ctx, "stdout", []byte("before call\n")); err != nil {
		return executionproto.Result{}, err
	}
	response, err := broker(ctx, executionproto.OutboundRequest{ID: 1, IntegrationID: schedulerTestIntegrationID, Method: "GET", Path: "/v1/issues"})
	if err != nil {
		return executionproto.Result{}, err
	}
	f.response = response
	return executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Stdout: []byte("before call\n")}, nil
}

type recordingExecutionOutboundRelay struct {
	request   executionproto.OutboundRequest
	assertion string
}

func (r *recordingExecutionOutboundRelay) Call(_ context.Context, request executionproto.OutboundRequest, assertion string) (executionproto.OutboundResponse, error) {
	r.request = request
	r.assertion = assertion
	return executionproto.OutboundResponse{ID: request.ID, Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"issues":[]}`)}, nil
}

func TestVMMClientExecutionBrokerRelaysOnlyHostAssertionAndSafeResponse(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(privateKey, "https://vmmd.example.test", "vmmd-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fake := &brokerExecutionVMM{fakeVMM: &fakeVMM{}}
	client := newClientWithSigner(t, fake, signer)
	relay := &recordingExecutionOutboundRelay{}
	request := executionproto.Request{
		Version: executionproto.Version, ExecutionID: "exec-1", Runtime: api.ExecutionRuntimeNode22,
		Source: "1 + 1", Input: json.RawMessage("null"), TimeoutMS: 1000,
		MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone, OutboundEnabled: true,
	}
	var output string
	result, err := client.ExecuteExecutionWithBroker(context.Background(), "exec-vm-1", request, func(_ context.Context, stream string, chunk []byte) error {
		if stream != "stdout" {
			t.Fatalf("output stream = %q", stream)
		}
		output += string(chunk)
		return nil
	}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if relay.assertion == "" || relay.request.IntegrationID != schedulerTestIntegrationID || relay.request.Path != "/v1/issues" {
		t.Fatalf("relayed call = %+v, assertion present=%t", relay.request, relay.assertion != "")
	}
	if output != "before call\n" || string(result.Result) != `{"ok":true}` || string(fake.response.Body) != `{"issues":[]}` {
		t.Fatalf("broker execution output/result/response = %q / %s / %s", output, result.Result, fake.response.Body)
	}
	if strings.Contains(string(result.Result), relay.assertion) || strings.Contains(string(result.Stdout), relay.assertion) {
		t.Fatal("host execution assertion leaked into the guest result")
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
