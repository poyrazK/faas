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
