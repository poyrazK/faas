package executionproto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestArtifactProtocolRoundTripAndBudget(t *testing.T) {
	data := []byte("patch")
	hash := sha256.Sum256(data)
	artifact := api.ExecutionArtifact{Name: "patch.diff", Content: data, SizeBytes: len(data), SHA256: "sha256:" + hex.EncodeToString(hash[:])}
	result := Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null"), Artifacts: []api.ExecutionArtifact{artifact}}
	bytes := len(result.Result) + api.ExecutionArtifactsOutputBytes(result.Artifacts)
	if err := result.Validate(bytes); err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(bytes - 1); !errors.Is(err, ErrOutputLimitExceeded) {
		t.Fatalf("limit = %v", err)
	}
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	req := Request{Version: ArtifactVersion, ExecutionID: "artifact", Runtime: api.ExecutionRuntimeNode22, Source: "export default () => null", Input: json.RawMessage("null"), TimeoutMS: 1000, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone, OutputFiles: []string{"patch.diff"}}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), guest, func(_ context.Context, received Request, _, _ *OutputWriter) (Result, error) {
			if received.Version != ArtifactVersion || len(received.OutputFiles) != 1 {
				t.Errorf("selection dropped: %+v", received)
			}
			return result, nil
		})
	}()
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Execute(context.Background(), req)
	if err != nil || string(got.Artifacts[0].Content) != "patch" {
		t.Fatalf("Execute = %+v, %v", got, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	req.Version = Version
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("artifact request used legacy protocol")
	}
}

func TestArtifactProtocolRejectsDroppedSelection(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	req := Request{Version: ArtifactVersion, ExecutionID: "missing-export", Runtime: api.ExecutionRuntimeNode22, Source: "export default () => null", Input: json.RawMessage("null"), TimeoutMS: 1000, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone, OutputFiles: []string{"patch.diff"}}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), guest, func(context.Context, Request, *OutputWriter, *OutputWriter) (Result, error) {
			return Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null")}, nil
		})
	}()
	client, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(context.Background(), req); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("dropped export acknowledged: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
