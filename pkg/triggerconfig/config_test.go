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

	sealed, err := Seal(api.TriggerKindKafka, raw, []*age.X25519Recipient{identity.Recipient()})
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
		[]*age.X25519Recipient{oldIdentity.Recipient()})
	if err != nil {
		t.Fatalf("Seal stored: %v", err)
	}
	storedObject := decodeMap(t, stored)

	// The merge needs no identity: an omitted credential keeps its stored
	// ciphertext, byte for byte (ADR 20261022111812).
	preserved, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"brokers":["old:9092"],"sasl":{"username":"new","password_set":true},"tls":{"client_cert":"cert"},"future":1}`))
	if err != nil {
		t.Fatalf("MergeForUpdate preserve: %v", err)
	}
	// A stored credential never follows the trigger to other brokers: anyone
	// who could edit it could otherwise read the password off their own
	// broker (bug hunt 2026-10-28 H107).
	if _, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"brokers":["attacker:9092"],"sasl":{"username":"old","password_set":true}}`)); !errors.Is(err, ErrCredentialMove) {
		t.Fatalf("MergeForUpdate to other brokers = %v, want ErrCredentialMove", err)
	}
	preservedObject := decodeMap(t, preserved)
	if preservedObject["sasl"].(map[string]any)["password_sealed"] != storedObject["sasl"].(map[string]any)["password_sealed"] ||
		preservedObject["tls"].(map[string]any)["client_key_sealed"] != storedObject["tls"].(map[string]any)["client_key_sealed"] ||
		bytes.Contains(preserved, []byte("password_set")) || !bytes.Contains(preserved, []byte(`"future":1`)) {
		t.Fatalf("preserve merge = %s", preserved)
	}
	resealed, err := SealMerged(api.TriggerKindKafka, preserved, []*age.X25519Recipient{newIdentity.Recipient()})
	if err != nil {
		t.Fatalf("SealMerged preserve: %v", err)
	}
	opened, err := Open(api.TriggerKindKafka, resealed, []*age.X25519Identity{oldIdentity})
	if err != nil || !bytes.Contains(opened, []byte("old-secret")) || !bytes.Contains(opened, []byte("old-key")) {
		t.Fatalf("kept credentials open = %s, %v; want them under the key they were sealed to", opened, err)
	}

	rotated, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"sasl":{"password":"new-secret"},"tls":{"client_key":"new-key"}}`))
	if err != nil {
		t.Fatalf("MergeForUpdate rotate: %v", err)
	}
	if bytes.Contains(rotated, []byte("_sealed")) || !bytes.Contains(rotated, []byte("new-secret")) || !bytes.Contains(rotated, []byte("new-key")) {
		t.Fatalf("rotate merge = %s", rotated)
	}

	removed, err := MergeForUpdate(api.TriggerKindKafka, stored,
		json.RawMessage(`{"brokers":["new:9092"],"topic":"t","group":"g"}`))
	if err != nil {
		t.Fatalf("MergeForUpdate remove: %v", err)
	}
	if bytes.Contains(removed, []byte("sasl")) || bytes.Contains(removed, []byte("tls")) || bytes.Contains(removed, []byte("old-secret")) {
		t.Fatalf("whole block removal retained secrets: %s", removed)
	}
}

// TestUpdateKeepsCredentialsWithoutOpening pins the update path apid runs
// (ADR 20261022111812): merge, validate the view, SealMerged. The client
// cannot smuggle ciphertext in, a credential stored in plaintext before
// sealing is sealed on the way out, and a kept credential reads as set to
// the validator.
func TestUpdateKeepsCredentialsWithoutOpening(t *testing.T) {
	domain, fleet := newIdentity(t), newIdentity(t)
	both := []*age.X25519Recipient{domain.Recipient(), fleet.Recipient()}
	stored, err := Seal(api.TriggerKindKafka,
		json.RawMessage(`{"brokers":["b:9092"],"sasl":{"mechanism":"PLAIN","username":"svc","password":"stored-secret"}}`), both)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []*age.X25519Identity{domain, fleet} {
		if opened, err := Open(api.TriggerKindKafka, stored, []*age.X25519Identity{id}); err != nil || !bytes.Contains(opened, []byte("stored-secret")) {
			t.Fatalf("a credential sealed to both recipients does not open with each: %s, %v", opened, err)
		}
	}

	smuggled := json.RawMessage(`{"brokers":["b:9092"],"sasl":{"mechanism":"PLAIN","username":"svc","password_sealed":"QUFBQQ=="}}`)
	merged, err := MergeForUpdate(api.TriggerKindKafka, stored, smuggled)
	if err != nil {
		t.Fatal(err)
	}
	if decodeMap(t, merged)["sasl"].(map[string]any)["password_sealed"] != decodeMap(t, stored)["sasl"].(map[string]any)["password_sealed"] {
		t.Fatalf("client ciphertext replaced the stored one: %s", merged)
	}
	view := decodeMap(t, ValidationView(api.TriggerKindKafka, merged))["sasl"].(map[string]any)
	if view["password"] != validationPlaceholder || view["password_sealed"] != nil {
		t.Fatalf("validation view = %v; want the kept password set", view)
	}
	resealed, err := SealMerged(api.TriggerKindKafka, merged, both)
	if err != nil || !bytes.Equal(resealed, merged) {
		t.Fatalf("SealMerged of a kept credential = %s, %v; want it unchanged", resealed, err)
	}
	if dropped, err := Seal(api.TriggerKindKafka, merged, both); err != nil || bytes.Contains(dropped, []byte("password_sealed")) {
		t.Fatalf("Seal kept supplied ciphertext: %s, %v", dropped, err)
	}

	legacy := json.RawMessage(`{"brokers":["b:9092"],"sasl":{"mechanism":"PLAIN","username":"svc","password":"legacy-secret"}}`)
	merged, err = MergeForUpdate(api.TriggerKindKafka, legacy, json.RawMessage(`{"brokers":["b:9092"],"sasl":{"mechanism":"PLAIN","username":"svc"}}`))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealMerged(api.TriggerKindKafka, merged, both)
	if err != nil || bytes.Contains(sealed, []byte("legacy-secret")) {
		t.Fatalf("a legacy plaintext password left the update unsealed: %s, %v", sealed, err)
	}
	if opened, err := Open(api.TriggerKindKafka, sealed, []*age.X25519Identity{domain}); err != nil || !bytes.Contains(opened, []byte("legacy-secret")) {
		t.Fatalf("legacy password after the update = %s, %v", opened, err)
	}
}
