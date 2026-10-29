package triggerconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRedisPasswordSealOpenRedactMerge(t *testing.T) {
	identity := newIdentity(t)
	raw := json.RawMessage(`{"addr":"redis.example:6379","stream":"orders","group":"g","username":"svc","password":"redis-secret","tls":true}`)

	if _, err := Seal(api.TriggerKindRedisStreams, raw, nil); !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("Seal without recipient = %v, want ErrRecipientUnavailable", err)
	}
	sealed, err := Seal(api.TriggerKindRedisStreams, raw, identity.Recipient())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("redis-secret")) {
		t.Fatalf("sealed config leaked the password: %s", sealed)
	}
	sealedObject := decodeMap(t, sealed)
	if _, ok := sealedObject["password"]; ok || sealedObject["password_sealed"] == nil || sealedObject["username"] != "svc" {
		t.Fatalf("sealed config = %s", sealed)
	}

	for _, stored := range []json.RawMessage{sealed, raw} {
		redacted := decodeMap(t, Redact(api.TriggerKindRedisStreams, stored))
		if _, ok := redacted["password"]; ok || redacted["password_sealed"] != nil || redacted["password_set"] != true || redacted["addr"] != "redis.example:6379" {
			t.Fatalf("Redact(%s) = %v", stored, redacted)
		}
	}

	opened, err := Open(api.TriggerKindRedisStreams, sealed, []*age.X25519Identity{identity})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if object := decodeMap(t, opened); object["password"] != "redis-secret" || object["password_sealed"] != nil {
		t.Fatalf("Open = %s", opened)
	}

	// A client round-trips the redacted config and keeps the password; a
	// new password replaces it.
	kept, err := MergeForUpdate(api.TriggerKindRedisStreams, sealed, Redact(api.TriggerKindRedisStreams, sealed), []*age.X25519Identity{identity})
	if err != nil {
		t.Fatalf("MergeForUpdate keep: %v", err)
	}
	if object := decodeMap(t, kept); object["password"] != "redis-secret" || object["password_set"] != nil {
		t.Fatalf("merged keep = %s", kept)
	}
	rotated, err := MergeForUpdate(api.TriggerKindRedisStreams, sealed, json.RawMessage(`{"addr":"redis.example:6379","stream":"orders","group":"g","password":"rotated"}`), []*age.X25519Identity{identity})
	if err != nil {
		t.Fatalf("MergeForUpdate rotate: %v", err)
	}
	if object := decodeMap(t, rotated); object["password"] != "rotated" {
		t.Fatalf("merged rotate = %s", rotated)
	}
}

func TestNATSURLCredentialsSealOpenRedactMerge(t *testing.T) {
	identity := newIdentity(t)
	identities := []*age.X25519Identity{identity}
	for _, original := range []string{"nats://svc:p%40ss@nats.example:4222", "tls://s3cr3t-token@nats.example:4222"} {
		raw, _ := json.Marshal(map[string]any{"url": original, "stream": "ORDERS", "subject": "orders.*", "durable": "d", "future": true})

		if _, err := Seal(api.TriggerKindNATS, raw, nil); !errors.Is(err, ErrRecipientUnavailable) {
			t.Fatalf("Seal without recipient = %v, want ErrRecipientUnavailable", err)
		}
		sealed, err := Seal(api.TriggerKindNATS, raw, identity.Recipient())
		if err != nil {
			t.Fatalf("Seal %s: %v", original, err)
		}
		if bytes.Contains(sealed, []byte("p%40ss")) || bytes.Contains(sealed, []byte("s3cr3t")) || bytes.Contains(sealed, []byte("svc")) {
			t.Fatalf("sealed config leaked the credentials: %s", sealed)
		}
		object := decodeMap(t, sealed)
		host := "nats://nats.example:4222"
		if original[:3] == "tls" {
			host = "tls://nats.example:4222"
		}
		if object["url"] != host || object["url_credentials_sealed"] == nil || object["future"] != true {
			t.Fatalf("sealed config = %s", sealed)
		}

		for _, stored := range []json.RawMessage{sealed, raw} {
			redacted := decodeMap(t, Redact(api.TriggerKindNATS, stored))
			if redacted["url"] != host || redacted["url_credentials_sealed"] != nil || redacted["url_credentials_set"] != true {
				t.Fatalf("Redact(%s) = %v", stored, redacted)
			}
		}

		opened, err := Open(api.TriggerKindNATS, sealed, identities)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if object := decodeMap(t, opened); object["url"] != original || object["url_credentials_sealed"] != nil {
			t.Fatalf("Open = %s, want url %s", opened, original)
		}

		// Round-tripping the redacted config keeps the credentials for the
		// same server only.
		kept, err := MergeForUpdate(api.TriggerKindNATS, sealed, Redact(api.TriggerKindNATS, sealed), identities)
		if err != nil {
			t.Fatalf("MergeForUpdate keep: %v", err)
		}
		if object := decodeMap(t, kept); object["url"] != original || object["url_credentials_set"] != nil {
			t.Fatalf("merged keep = %s", kept)
		}
		moved, err := MergeForUpdate(api.TriggerKindNATS, sealed, json.RawMessage(`{"url":"nats://elsewhere.example:4222","stream":"ORDERS","subject":"orders.*","durable":"d"}`), identities)
		if err != nil {
			t.Fatalf("MergeForUpdate move: %v", err)
		}
		if object := decodeMap(t, moved); object["url"] != "nats://elsewhere.example:4222" {
			t.Fatalf("credentials followed the server move: %s", moved)
		}
	}

	plain := json.RawMessage(`{"url":"nats://nats.example:4222","stream":"S","subject":"s","durable":"d"}`)
	if sealed, err := Seal(api.TriggerKindNATS, plain, nil); err != nil || !bytes.Equal(sealed, plain) {
		t.Fatalf("Seal without credentials = %s, %v; want byte-identical", sealed, err)
	}
	if opened, err := Open(api.TriggerKindNATS, plain, nil); err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("Open without credentials = %s, %v; want byte-identical", opened, err)
	}
}
