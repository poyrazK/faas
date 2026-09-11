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

// Seal replaces plaintext Kafka credential leaves with age ciphertext. A
// recipient is only required when a plaintext credential is present.
func Seal(kind api.TriggerKind, raw json.RawMessage, recipient *age.X25519Recipient) (json.RawMessage, error) {
	if kind != api.TriggerKindKafka {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode kafka config: %w", err)
	}
	changed := false
	for _, leaf := range kafkaSecretLeaves {
		block, exists, err := nestedObject(top, leaf.block)
		if err != nil {
			return nil, err
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
			if err := setNestedObject(top, leaf.block, block); err != nil {
				return nil, err
			}
			continue
		}
		if recipient == nil {
			return nil, ErrRecipientUnavailable
		}
		plaintext, err := decodeString(plainRaw, leaf.block+"."+leaf.plain)
		if err != nil {
			return nil, err
		}
		ciphertext, err := secretbox.SealBytes(recipient, leaf.namespace, []byte(plaintext), leaf.maxBytes)
		if err != nil {
			return nil, fmt.Errorf("seal %s.%s: %w", leaf.block, leaf.plain, err)
		}
		sealedRaw, err := json.Marshal(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("encode %s.%s envelope: %w", leaf.block, leaf.plain, err)
		}
		delete(block, leaf.plain)
		block[leaf.sealed] = sealedRaw
		if err := setNestedObject(top, leaf.block, block); err != nil {
			return nil, err
		}
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

// Open restores sealed Kafka credentials for runtime use. Legacy plaintext
// leaves remain supported during the rollout window.
func Open(kind api.TriggerKind, raw json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	if kind != api.TriggerKindKafka {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode kafka config: %w", err)
	}
	changed := false
	for _, leaf := range kafkaSecretLeaves {
		block, exists, err := nestedObject(top, leaf.block)
		if err != nil {
			return nil, err
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
		var ciphertext []byte
		if err := json.Unmarshal(sealedRaw, &ciphertext); err != nil {
			return nil, fmt.Errorf("decode %s.%s envelope: %w", leaf.block, leaf.plain, err)
		}
		namespace, plaintext, err := secretbox.OpenBytesMulti(identities, ciphertext)
		if err != nil {
			return nil, fmt.Errorf("open %s.%s envelope: %w", leaf.block, leaf.plain, err)
		}
		if namespace != leaf.namespace {
			return nil, fmt.Errorf("open %s.%s envelope: namespace mismatch", leaf.block, leaf.plain)
		}
		plainRaw, err := json.Marshal(string(plaintext))
		if err != nil {
			return nil, fmt.Errorf("encode %s.%s: %w", leaf.block, leaf.plain, err)
		}
		delete(block, leaf.sealed)
		block[leaf.plain] = plainRaw
		if err := setNestedObject(top, leaf.block, block); err != nil {
			return nil, err
		}
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

// Redact strips plaintext and ciphertext credentials from a customer-facing
// Kafka config and reports only whether each credential is configured.
func Redact(kind api.TriggerKind, raw json.RawMessage) json.RawMessage {
	if kind != api.TriggerKindKafka {
		return cloneRaw(raw)
	}
	top, err := decodeObject(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	for _, leaf := range kafkaSecretLeaves {
		block, exists, err := nestedObject(top, leaf.block)
		if err != nil || !exists {
			if err != nil {
				return json.RawMessage("{}")
			}
			continue
		}
		_, hasPlain := block[leaf.plain]
		_, hasSealed := block[leaf.sealed]
		delete(block, leaf.plain)
		delete(block, leaf.sealed)
		delete(block, leaf.marker)
		if hasPlain || hasSealed {
			block[leaf.marker] = json.RawMessage("true")
		}
		if err := setNestedObject(top, leaf.block, block); err != nil {
			return json.RawMessage("{}")
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
	if kind != api.TriggerKindKafka {
		return cloneRaw(incoming), nil
	}
	openedStored, err := Open(kind, stored, identities)
	if err != nil {
		return nil, fmt.Errorf("open stored kafka config: %w", err)
	}
	storedTop, err := decodeObject(openedStored)
	if err != nil {
		return nil, fmt.Errorf("decode stored kafka config: %w", err)
	}
	incomingTop, err := decodeObject(incoming)
	if err != nil {
		return nil, fmt.Errorf("decode incoming kafka config: %w", err)
	}
	for _, leaf := range kafkaSecretLeaves {
		incomingBlock, supplied, err := nestedObject(incomingTop, leaf.block)
		if err != nil {
			return nil, err
		}
		if !supplied {
			continue
		}
		delete(incomingBlock, leaf.marker)
		delete(incomingBlock, leaf.sealed)
		if _, hasIncomingSecret := incomingBlock[leaf.plain]; !hasIncomingSecret {
			storedBlock, exists, err := nestedObject(storedTop, leaf.block)
			if err != nil {
				return nil, err
			}
			if exists {
				if storedSecret, ok := storedBlock[leaf.plain]; ok {
					incomingBlock[leaf.plain] = cloneRaw(storedSecret)
				}
			}
		}
		if err := setNestedObject(incomingTop, leaf.block, incomingBlock); err != nil {
			return nil, err
		}
	}
	return encodeObject(incomingTop)
}
