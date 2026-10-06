package executionpayload

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestSealDecodeRoundTripAndKIDBinding(t *testing.T) {
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(current.Recipient(), "export default async function main(input) { return input; }", json.RawMessage(`{"ok":true}`))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	source, input, err := Decode(context.Background(), []*age.X25519Identity{current, previous}, sealed, current.Recipient().String())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !strings.HasPrefix(source, "export default") || string(input) != `{"ok":true}` {
		t.Fatalf("decoded payload = %q/%s", source, input)
	}

	if _, _, err := Decode(context.Background(), []*age.X25519Identity{current, previous}, sealed, previous.Recipient().String()); !errors.Is(err, ErrKIDMismatch) {
		t.Fatalf("wrong kid error = %v, want ErrKIDMismatch", err)
	}
	if _, _, err := Decode(context.Background(), []*age.X25519Identity{previous, current}, sealed, current.Recipient().String()); err != nil {
		t.Fatalf("rotation-order decode: %v", err)
	}
}

func TestWorkflowPlanSealingKeepsNamespaceAndKIDBinding(t *testing.T) {
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	plan := []byte(`{"workflow_id":"agent-check","version":"v1","steps":[]}`)
	sealed, err := SealWorkflowPlan(current.Recipient(), plan)
	if err != nil {
		t.Fatalf("SealWorkflowPlan: %v", err)
	}
	decoded, err := DecodeWorkflowPlan(context.Background(), []*age.X25519Identity{current}, sealed, current.Recipient().String())
	if err != nil {
		t.Fatalf("DecodeWorkflowPlan: %v", err)
	}
	if string(decoded) != string(plan) {
		t.Fatalf("decoded plan = %s, want %s", decoded, plan)
	}
	clear(decoded)
	if _, err := DecodeWorkflowPlan(context.Background(), []*age.X25519Identity{current}, sealed, "wrong-key-id"); !errors.Is(err, ErrKIDMismatch) {
		t.Fatalf("wrong key id error = %v, want ErrKIDMismatch", err)
	}
	if _, err := SealWorkflowPlan(current.Recipient(), []byte(`{"broken"`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid plan error = %v, want ErrInvalid", err)
	}
}

func TestDecodeRejectsTamperNamespaceAndMalformedEnvelope(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	kid := identity.Recipient().String()
	sealed, err := Seal(identity.Recipient(), "return true", json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, _, err := Decode(context.Background(), []*age.X25519Identity{identity}, sealed, kid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tamper error = %v, want ErrInvalid", err)
	}

	wrongNamespace, err := sealRaw(identity, "other", Envelope{Version: CurrentVersion, Source: "return true", Input: json.RawMessage("null")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(context.Background(), []*age.X25519Identity{identity}, wrongNamespace, kid); !errors.Is(err, ErrWrongNamespace) {
		t.Fatalf("namespace error = %v, want ErrWrongNamespace", err)
	}

	bad, err := secretbox.SealBytes(identity.Recipient(), Namespace, []byte(`{"version":1,"source":"return true","input":{"bad"}}`), api.ExecutionSealedPayloadMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(context.Background(), []*age.X25519Identity{identity}, bad, kid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed input error = %v, want ErrInvalid", err)
	}
}

func TestEnvelopeAndDecodeBounds(t *testing.T) {
	if err := (Envelope{Version: CurrentVersion, Source: " ", Input: json.RawMessage("null")}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty source error = %v", err)
	}
	if err := (Envelope{Version: CurrentVersion, Source: "return true", Input: nil}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing input error = %v", err)
	}
	if err := (Envelope{Version: CurrentVersion + 1, Source: "return true", Input: json.RawMessage("null")}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("version error = %v", err)
	}

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(identity.Recipient(), strings.Repeat("x", api.ExecutionPlaintextFieldMaxBytes+1), json.RawMessage("null"))
	if err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlong source error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Decode(ctx, []*age.X25519Identity{identity}, sealed, identity.Recipient().String()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decode error = %v", err)
	}
}

func TestSealDecodeBundleRoundTrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	req := api.ResolvedExecutionRequest{
		Entrypoint:  "src/main.mjs",
		OutputFiles: []string{"report.txt", "data/result.bin"},
		Files: []api.ExecutionFile{
			{Path: "src/main.mjs", Content: []byte("export default () => 42")},
			{Path: "src/lib.mjs", Content: []byte("export const value = 41")},
		},
		Input: json.RawMessage(`{"ok":true}`),
	}
	sealed, err := SealRequest(identity.Recipient(), req)
	if err != nil {
		t.Fatalf("SealRequest: %v", err)
	}
	decoded, err := DecodeRequest(context.Background(), []*age.X25519Identity{identity}, sealed, identity.Recipient().String())
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if decoded.Source != "" || decoded.Entrypoint != req.Entrypoint || string(decoded.Input) != string(req.Input) || len(decoded.Files) != 2 {
		t.Fatalf("decoded = %#v", decoded)
	}
	if strings.Join(decoded.OutputFiles, ",") != strings.Join(req.OutputFiles, ",") {
		t.Fatalf("output selection lost: %v", decoded.OutputFiles)
	}
	req.OutputFiles[0] = "../invalid"
	if _, err := SealRequest(identity.Recipient(), req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid output selection = %v", err)
	}
	req.Files[0].Content[0] = 'X'
	if decoded.Files[0].Content[0] == 'X' {
		t.Fatal("decoded bundle aliases request")
	}
}

func TestSealDecodeProfileBinding(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []api.ExecutionProfile{"", api.ExecutionProfileStandard, api.ExecutionProfilePythonDataV1} {
		sealed, err := SealRequest(identity.Recipient(), api.ResolvedExecutionRequest{Profile: profile, Source: "def main(input, context): return input"})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeRequest(context.Background(), []*age.X25519Identity{identity}, sealed, identity.Recipient().String())
		if err != nil || decoded.Profile != profile.Normalized() {
			t.Fatalf("profile=%q decoded=%q, %v", profile, decoded.Profile, err)
		}
	}
	if _, err := SealRequest(identity.Recipient(), api.ResolvedExecutionRequest{Profile: "pip-anything", Source: "def main(input, context): return input"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown profile accepted: %v", err)
	}
}

func sealRaw(identity *age.X25519Identity, namespace string, envelope Envelope) ([]byte, error) {
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	// SealBytes is intentionally used here rather than constructing age
	// frames in the test; it exercises the same authenticated namespace wire
	// shape as production sealing.
	return secretbox.SealBytes(identity.Recipient(), namespace, plaintext, api.ExecutionSealedPayloadMaxBytes)
}
