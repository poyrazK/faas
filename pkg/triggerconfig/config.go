// Package triggerconfig owns the credential boundary for opaque trigger
// configuration JSON. It deliberately edits raw JSON objects instead of
// decoding the whole config into versioned structs, preserving fields added by
// newer clients while protecting the credential leaves known to the platform.
package triggerconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var (
	ErrRecipientUnavailable = errors.New("triggerconfig: secret recipient unavailable")
	ErrIdentityUnavailable  = errors.New("triggerconfig: secret identity unavailable")
)

const (
	kafkaSASLPasswordNamespace = "trigger_kafka_sasl_password"
	kafkaTLSClientKeyNamespace = "trigger_kafka_tls_client_key"
	redisPasswordNamespace     = "trigger_redis_password"
	redisTLSClientKeyNamespace = "trigger_redis_tls_client_key"
	natsPasswordNamespace      = "trigger_nats_password"
	natsTokenNamespace         = "trigger_nats_token"
	natsCredentialsNamespace   = "trigger_nats_credentials"
	natsNKeyNamespace          = "trigger_nats_nkey"
	natsTLSClientKeyNamespace  = "trigger_nats_tls_client_key"
)

type rawObject map[string]json.RawMessage

type secretLeaf struct {
	block     string
	plain     string
	sealed    string
	marker    string
	namespace string
	maxBytes  int
}

var kafkaSecretLeaves = []secretLeaf{
	{
		block: "sasl", plain: "password", sealed: "password_sealed", marker: "password_set",
		namespace: kafkaSASLPasswordNamespace, maxBytes: api.KafkaSASLPasswordMaxBytes,
	},
	{
		block: "tls", plain: "client_key", sealed: "client_key_sealed", marker: "client_key_set",
		namespace: kafkaTLSClientKeyNamespace, maxBytes: api.KafkaTLSClientKeyMaxBytes,
	},
}

var redisSecretLeaves = []secretLeaf{
	{
		block: "", plain: "password", sealed: "password_sealed", marker: "password_set",
		namespace: redisPasswordNamespace, maxBytes: api.RedisPasswordMaxBytes,
	},
	{
		block: "tls", plain: "client_key", sealed: "client_key_sealed", marker: "client_key_set",
		namespace: redisTLSClientKeyNamespace, maxBytes: api.TLSClientKeyMaxBytes,
	},
}

var natsSecretLeaves = []secretLeaf{
	{
		block: "", plain: "password", sealed: "password_sealed", marker: "password_set",
		namespace: natsPasswordNamespace, maxBytes: api.NATSPasswordMaxBytes,
	},
	{
		block: "", plain: "token", sealed: "token_sealed", marker: "token_set",
		namespace: natsTokenNamespace, maxBytes: api.NATSTokenMaxBytes,
	},
	{
		block: "", plain: "credentials", sealed: "credentials_sealed", marker: "credentials_set",
		namespace: natsCredentialsNamespace, maxBytes: api.NATSCredentialsMaxBytes,
	},
	{
		block: "", plain: "nkey", sealed: "nkey_sealed", marker: "nkey_set",
		namespace: natsNKeyNamespace, maxBytes: api.NATSNKeyMaxBytes,
	},
	{
		block: "tls", plain: "client_key", sealed: "client_key_sealed", marker: "client_key_set",
		namespace: natsTLSClientKeyNamespace, maxBytes: api.TLSClientKeyMaxBytes,
	},
}

func secretLeavesForKind(kind api.TriggerKind) []secretLeaf {
	switch kind {
	case api.TriggerKindKafka:
		return kafkaSecretLeaves
	case api.TriggerKindRedisStreams:
		return redisSecretLeaves
	case api.TriggerKindNATS:
		return natsSecretLeaves
	default:
		return nil
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func decodeObject(raw json.RawMessage) (rawObject, error) {
	var object rawObject
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty JSON")
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected JSON object")
	}
	return object, nil
}

func encodeObject(object rawObject) (json.RawMessage, error) {
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func nestedObject(parent rawObject, key string) (rawObject, bool, error) {
	raw, ok := parent[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	object, err := decodeObject(raw)
	if err != nil {
		return nil, true, fmt.Errorf("%s must be an object: %w", key, err)
	}
	return object, true, nil
}

func setNestedObject(parent rawObject, key string, object rawObject) error {
	raw, err := encodeObject(object)
	if err != nil {
		return err
	}
	parent[key] = raw
	return nil
}

func decodeString(raw json.RawMessage, field string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string: %w", field, err)
	}
	return value, nil
}

// Seal replaces plaintext credential leaves with age ciphertext. A
// recipient is only required when a plaintext credential is present.
func Seal(kind api.TriggerKind, raw json.RawMessage, recipient *age.X25519Recipient) (json.RawMessage, error) {
	leaves := secretLeavesForKind(kind)
	if len(leaves) == 0 {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s config: %w", kind, err)
	}
	changed := false
	for _, leaf := range leaves {
		var block rawObject
		var exists bool
		if leaf.block == "" {
			block = top
			exists = true
		} else {
			block, exists, err = nestedObject(top, leaf.block)
			if err != nil {
				return nil, err
			}
		}
		if !exists {
			continue
		}
		if _, hasMarker := block[leaf.marker]; hasMarker {
			delete(block, leaf.marker)
			changed = true
		}
		plainRaw, hasPlain := block[leaf.plain]
		if !hasPlain {
			if _, suppliedCiphertext := block[leaf.sealed]; suppliedCiphertext {
				delete(block, leaf.sealed)
				changed = true
			}
			if leaf.block != "" {
				if err := setNestedObject(top, leaf.block, block); err != nil {
					return nil, err
				}
			}
			continue
		}
		if recipient == nil {
			return nil, ErrRecipientUnavailable
		}
		fieldPath := leaf.plain
		if leaf.block != "" {
			fieldPath = leaf.block + "." + leaf.plain
		}
		plaintext, err := decodeString(plainRaw, fieldPath)
		if err != nil {
			return nil, err
		}
		ciphertext, err := secretbox.SealBytes(recipient, leaf.namespace, []byte(plaintext), leaf.maxBytes)
		if err != nil {
			return nil, fmt.Errorf("seal %s: %w", fieldPath, err)
		}
		sealedRaw, err := json.Marshal(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("encode %s envelope: %w", fieldPath, err)
		}
		delete(block, leaf.plain)
		block[leaf.sealed] = sealedRaw
		if leaf.block != "" {
			if err := setNestedObject(top, leaf.block, block); err != nil {
				return nil, err
			}
		}
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

// Open restores sealed credentials for runtime use. Legacy plaintext
// leaves remain supported during the rollout window.
func Open(kind api.TriggerKind, raw json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	leaves := secretLeavesForKind(kind)
	if len(leaves) == 0 {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s config: %w", kind, err)
	}
	changed := false
	for _, leaf := range leaves {
		var block rawObject
		var exists bool
		if leaf.block == "" {
			block = top
			exists = true
		} else {
			block, exists, err = nestedObject(top, leaf.block)
			if err != nil {
				return nil, err
			}
		}
		if !exists {
			continue
		}
		if _, hasMarker := block[leaf.marker]; hasMarker {
			delete(block, leaf.marker)
			changed = true
		}
		sealedRaw, hasSealed := block[leaf.sealed]
		if !hasSealed {
			continue
		}
		if len(identities) == 0 {
			return nil, ErrIdentityUnavailable
		}
		fieldPath := leaf.plain
		if leaf.block != "" {
			fieldPath = leaf.block + "." + leaf.plain
		}
		var ciphertext []byte
		if err := json.Unmarshal(sealedRaw, &ciphertext); err != nil {
			return nil, fmt.Errorf("decode %s envelope: %w", fieldPath, err)
		}
		namespace, plaintext, err := secretbox.OpenBytesMulti(identities, ciphertext)
		if err != nil {
			return nil, fmt.Errorf("open %s envelope: %w", fieldPath, err)
		}
		if namespace != leaf.namespace {
			return nil, fmt.Errorf("open %s envelope: namespace mismatch", fieldPath)
		}
		plainRaw, err := json.Marshal(string(plaintext))
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", fieldPath, err)
		}
		delete(block, leaf.sealed)
		block[leaf.plain] = plainRaw
		if leaf.block != "" {
			if err := setNestedObject(top, leaf.block, block); err != nil {
				return nil, err
			}
		}
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

// Redact strips plaintext and ciphertext credentials from a customer-facing
// trigger config and reports only whether each credential is configured.
func Redact(kind api.TriggerKind, raw json.RawMessage) json.RawMessage {
	leaves := secretLeavesForKind(kind)
	if len(leaves) == 0 {
		return cloneRaw(raw)
	}
	top, err := decodeObject(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	for _, leaf := range leaves {
		var block rawObject
		var exists bool
		if leaf.block == "" {
			block = top
			exists = true
		} else {
			block, exists, err = nestedObject(top, leaf.block)
			if err != nil || !exists {
				if err != nil {
					return json.RawMessage("{}")
				}
				continue
			}
		}
		_, hasPlain := block[leaf.plain]
		_, hasSealed := block[leaf.sealed]
		delete(block, leaf.plain)
		delete(block, leaf.sealed)
		delete(block, leaf.marker)
		if hasPlain || hasSealed {
			block[leaf.marker] = json.RawMessage("true")
		}
		if leaf.block != "" {
			if err := setNestedObject(top, leaf.block, block); err != nil {
				return json.RawMessage("{}")
			}
		}
	}
	redacted, err := encodeObject(top)
	if err != nil {
		return json.RawMessage("{}")
	}
	return redacted
}

// MergeForUpdate treats incoming as a complete config replacement, while
// preserving a stored credential when its containing block is supplied but
// its plaintext leaf is omitted. Omitting the whole block removes it.
func MergeForUpdate(kind api.TriggerKind, stored, incoming json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	leaves := secretLeavesForKind(kind)
	if len(leaves) == 0 {
		return cloneRaw(incoming), nil
	}
	openedStored, err := Open(kind, stored, identities)
	if err != nil {
		return nil, fmt.Errorf("open stored %s config: %w", kind, err)
	}
	storedTop, err := decodeObject(openedStored)
	if err != nil {
		return nil, fmt.Errorf("decode stored %s config: %w", kind, err)
	}
	incomingTop, err := decodeObject(incoming)
	if err != nil {
		return nil, fmt.Errorf("decode incoming %s config: %w", kind, err)
	}
	for _, leaf := range leaves {
		var incomingBlock rawObject
		var supplied bool
		if leaf.block == "" {
			incomingBlock = incomingTop
			supplied = true
		} else {
			incomingBlock, supplied, err = nestedObject(incomingTop, leaf.block)
			if err != nil {
				return nil, err
			}
			if !supplied {
				continue
			}
		}
		delete(incomingBlock, leaf.marker)
		delete(incomingBlock, leaf.sealed)
		if _, hasIncomingSecret := incomingBlock[leaf.plain]; !hasIncomingSecret {
			var storedBlock rawObject
			var exists bool
			if leaf.block == "" {
				storedBlock = storedTop
				exists = true
			} else {
				storedBlock, exists, err = nestedObject(storedTop, leaf.block)
				if err != nil {
					return nil, err
				}
			}
			if exists {
				if storedSecret, ok := storedBlock[leaf.plain]; ok {
					incomingBlock[leaf.plain] = cloneRaw(storedSecret)
				}
			}
		}
		if leaf.block != "" {
			if err := setNestedObject(incomingTop, leaf.block, incomingBlock); err != nil {
				return nil, err
			}
		}
	}
	return encodeObject(incomingTop)
}
