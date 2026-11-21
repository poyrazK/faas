package executionpayload

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/runfile"
)

func TestStoredFileKeysAndResultSchemaSurviveSealingButNeverGuestJSON(t *testing.T) {
	key, _ := age.GenerateX25519Identity()
	fileKey, _ := age.GenerateX25519Identity()
	account, id := uuid.NewString(), uuid.NewString()
	ref := runfile.Ref{Descriptor: runfile.Descriptor{ID: id, Path: "data.bin", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}, StorageKey: runfile.Key(account, id, uuid.NewString()), PrivateKey: fileKey.String()}
	request := api.ResolvedExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default () => 42", Input: json.RawMessage("null"), ResultSchema: json.RawMessage(`{"type":"integer"}`), StoredInputs: []runfile.Ref{ref}}
	sealed, err := SealRequest(key.Recipient(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(context.Background(), []*age.X25519Identity{key}, sealed, key.Recipient().String()); err == nil {
		t.Fatal("legacy decoder silently discarded required files or contract")
	}
	decoded, err := DecodeRequest(context.Background(), []*age.X25519Identity{key}, sealed, key.Recipient().String())
	if err != nil || len(decoded.StoredInputs) != 1 || decoded.StoredInputs[0].PrivateKey != ref.PrivateKey || string(decoded.ResultSchema) != string(request.ResultSchema) {
		t.Fatalf("sealed metadata lost: %v", err)
	}
	wire := executionproto.RequestFromResolvedExecution("run", api.ResolvedExecutionRequest{StoredInputs: decoded.StoredInputs, Runtime: request.Runtime, Source: decoded.Source, Input: decoded.Input})
	body, _ := json.Marshal(wire)
	if wire.Version != executionproto.FileVersion || strings.Contains(string(body), ref.PrivateKey) || strings.Contains(string(body), ref.StorageKey) || strings.Contains(string(body), "result_schema") {
		t.Fatal("private metadata reached guest JSON")
	}
	if err := (Envelope{Version: CurrentVersion, Source: request.Source, Input: request.Input, StoredInputs: []runfile.Ref{ref}}).Validate(); err == nil {
		t.Fatal("old payload version accepted stored keys")
	}
}
