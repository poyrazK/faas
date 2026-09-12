package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/executionpayload"
)

func TestNewAgeExecutionPayloadDecoderUsesAuthenticatedEnvelope(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := executionpayload.Seal(identity.Recipient(), "return input", json.RawMessage(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	decode := NewAgeExecutionPayloadDecoder([]*age.X25519Identity{identity})
	if decode == nil {
		t.Fatal("decoder is nil")
	}
	source, input, err := decode(context.Background(), sealed, identity.Recipient().String())
	if err != nil || source != "return input" || string(input) != `{"ok":true}` {
		t.Fatalf("decoded payload = %q/%s, err=%v", source, input, err)
	}
	if _, _, err := decode(context.Background(), sealed, "wrong-kid"); !errors.Is(err, executionpayload.ErrKIDMismatch) {
		t.Fatalf("wrong kid error = %v", err)
	}
}

func TestNewAgeExecutionPayloadDecoderRequiresIdentities(t *testing.T) {
	if got := NewAgeExecutionPayloadDecoder(nil); got != nil {
		t.Fatal("nil identities produced an active decoder")
	}
}
