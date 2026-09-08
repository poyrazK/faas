package triggerconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	return identity
}

func decodeMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode JSON: %v: %s", err, raw)
	}
	return out
}

func nestedMap(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	nested, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, object[key])
	}
	return nested
}

func TestKafkaConfigSealOpenRoundTripPreservesUnknownFields(t *testing.T) {
	identity := newIdentity(t)
	raw := json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g","future_field":{"kept":true},"sasl":{"mechanism":"PLAIN","username":"svc","password":"sasl-secret"},"tls":{"client_cert":"cert","client_key":"PRIVATE KEY"}}`)

	sealed, err := Seal(api.TriggerKindKafka, raw, identity.Recipient())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("sasl-secret")) || bytes.Contains(sealed, []byte("PRIVATE KEY")) {
		t.Fatalf("sealed config leaked plaintext: %s", sealed)
	}
	sealedObject := decodeMap(t, sealed)
	sealedSASL := nestedMap(t, sealedObject, "sasl")
	sealedTLS := nestedMap(t, sealedObject, "tls")
	if _, ok := sealedSASL["password"]; ok {
		t.Fatalf("sealed SASL retained plaintext leaf: %s", sealed)
	}
	if _, ok := sealedTLS["client_key"]; ok {
		t.Fatalf("sealed TLS retained plaintext leaf: %s", sealed)
	}
	if sealedSASL["password_sealed"] == nil || sealedTLS["client_key_sealed"] == nil {
		t.Fatalf("sealed credential leaves missing: %s", sealed)
	}

	opened, err := Open(api.TriggerKindKafka, sealed, []*age.X25519Identity{identity})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Contains(opened, []byte("sasl-secret")) || !bytes.Contains(opened, []byte("PRIVATE KEY")) ||
		!bytes.Contains(opened, []byte(`"future_field":{"kept":true}`)) {
		t.Fatalf("opened config lost secrets or unknown fields: %s", opened)
	}
	if bytes.Contains(opened, []byte("password_sealed")) || bytes.Contains(opened, []byte("client_key_sealed")) {
		t.Fatalf("opened config retained ciphertext leaves: %s", opened)
	}
}

func TestNonKafkaConfigPassesThrough(t *testing.T) {
	raw := json.RawMessage(` {"mode":"queue","future":true} `)
	sealed, err := Seal(api.TriggerKindQueue, raw, nil)
	if err != nil || !bytes.Equal(sealed, raw) {
		t.Fatalf("Seal queue = %q, %v; want byte-identical", sealed, err)
	}
	opened, err := Open(api.TriggerKindQueue, raw, nil)
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatalf("Open queue = %q, %v; want byte-identical", opened, err)
	}
	if redacted := Redact(api.TriggerKindQueue, raw); !bytes.Equal(redacted, raw) {
		t.Fatalf("Redact queue = %q, want byte-identical", redacted)
	}
}

func TestSealRequiresRecipientOnlyWhenPlaintextSecretExists(t *testing.T) {
	withoutSecret := json.RawMessage(`{"brokers":["b:9092"],"topic":"t","group":"g"}`)
	if _, err := Seal(api.TriggerKindKafka, withoutSecret, nil); err != nil {
		t.Fatalf("Seal without secret: %v", err)
	}
	withSecret := json.RawMessage(`{"sasl":{"password":"secret"}}`)
	if _, err := Seal(api.TriggerKindKafka, withSecret, nil); !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("Seal with nil recipient error = %v, want ErrRecipientUnavailable", err)
	}
}

func TestSealDropsResponseMarkersAndClientSuppliedCiphertext(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"sasl":{"username":"svc","password_set":true}}`),
		json.RawMessage(`{"tls":{"client_key_sealed":"bm90LXRydXN0ZWQ="}}`),
	} {
		sealed, err := Seal(api.TriggerKindKafka, raw, nil)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if bytes.Contains(sealed, []byte("password_set")) || bytes.Contains(sealed, []byte("client_key_sealed")) {
			t.Fatalf("Seal retained response-only or client ciphertext leaves: %s", sealed)
		}
	}
}

func TestRedactRemovesPlaintextAndCiphertextAndAddsMarkers(t *testing.T) {
	raw := json.RawMessage(`{"sasl":{"username":"svc","password":"plain","password_sealed":"YQ=="},"tls":{"client_key":"key","client_key_sealed":"Yg=="}}`)
	redacted := Redact(api.TriggerKindKafka, raw)
	if strings.Contains(string(redacted), "plain") || strings.Contains(string(redacted), `"password"`) ||
		strings.Contains(string(redacted), `"password_sealed"`) || strings.Contains(string(redacted), `"client_key"`) ||
		strings.Contains(string(redacted), `"client_key_sealed"`) {
		t.Fatalf("redacted config leaked credential material: %s", redacted)
	}
	object := decodeMap(t, redacted)
	if nestedMap(t, object, "sasl")["password_set"] != true || nestedMap(t, object, "tls")["client_key_set"] != true {
		t.Fatalf("redacted config omitted markers: %s", redacted)
	}
	if got := Redact(api.TriggerKindKafka, json.RawMessage(`{`)); string(got) != "{}" {
		t.Fatalf("malformed redaction = %s, want {}", got)
	}
}

func TestOpenRejectsWrongNamespaceCorruptCiphertextAndMissingIdentity(t *testing.T) {
	identity := newIdentity(t)
	wrongNamespace, err := secretbox.SealBytes(identity.Recipient(), "wrong_namespace", []byte("secret"), api.KafkaSASLPasswordMaxBytes)
	if err != nil {
		t.Fatalf("SealBytes: %v", err)
	}
	encodedWrong, _ := json.Marshal(wrongNamespace)
	wrongRaw := json.RawMessage(`{"sasl":{"password_sealed":` + string(encodedWrong) + `}}`)
	if _, err := Open(api.TriggerKindKafka, wrongRaw, []*age.X25519Identity{identity}); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("wrong namespace error = %v", err)
	}
	if _, err := Open(api.TriggerKindKafka, json.RawMessage(`{"sasl":{"password_sealed":"bm90LWFnZQ=="}}`), []*age.X25519Identity{identity}); err == nil {
		t.Fatal("corrupt ciphertext error = nil")
	}
	if _, err := Open(api.TriggerKindKafka, wrongRaw, nil); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("missing identity error = %v, want ErrIdentityUnavailable", err)
	}
}

func TestOpenAcceptsLegacyPlaintext(t *testing.T) {
	raw := json.RawMessage(`{"sasl":{"password":"legacy"},"tls":{"client_key":"legacy-key"}}`)
	opened, err := Open(api.TriggerKindKafka, raw, nil)
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatalf("Open legacy = %s, %v; want byte-identical", opened, err)
	}
}

func TestMergeForUpdatePreservesRotatesAndDeletesSecretBlocks(t *testing.T) {
	oldIdentity := newIdentity(t)
	newIdentity := newIdentity(t)
	stored, err := Seal(api.TriggerKindKafka,
		json.RawMessage(`{"brokers":["old:9092"],"sasl":{"username":"old","password":"old-secret"},"tls":{"client_key":"old-key"}}`),
		oldIdentity.Recipient())
	if err != nil {
		t.Fatalf("Seal stored: %v", err)
	}

	preserved, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"brokers":["new:9092"],"sasl":{"username":"new","password_set":true},"tls":{"client_cert":"cert"},"future":1}`),
		[]*age.X25519Identity{newIdentity, oldIdentity})
	if err != nil {
		t.Fatalf("MergeForUpdate preserve: %v", err)
	}
	if !bytes.Contains(preserved, []byte("old-secret")) || !bytes.Contains(preserved, []byte("old-key")) ||
		bytes.Contains(preserved, []byte("password_set")) || !bytes.Contains(preserved, []byte(`"future":1`)) {
		t.Fatalf("preserve merge = %s", preserved)
	}

	rotated, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"sasl":{"password":"new-secret"},"tls":{"client_key":"new-key"}}`),
		[]*age.X25519Identity{oldIdentity})
	if err != nil {
		t.Fatalf("MergeForUpdate rotate: %v", err)
	}
	if bytes.Contains(rotated, []byte("old-secret")) || !bytes.Contains(rotated, []byte("new-secret")) || !bytes.Contains(rotated, []byte("new-key")) {
		t.Fatalf("rotate merge = %s", rotated)
	}

	removed, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"brokers":["new:9092"],"topic":"t","group":"g"}`),
		[]*age.X25519Identity{oldIdentity})
	if err != nil {
		t.Fatalf("MergeForUpdate remove: %v", err)
	}
	if bytes.Contains(removed, []byte("sasl")) || bytes.Contains(removed, []byte("tls")) || bytes.Contains(removed, []byte("old-secret")) {
		t.Fatalf("whole block removal retained secrets: %s", removed)
	}
}
