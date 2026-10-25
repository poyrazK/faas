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
	"net/url"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var (
	ErrRecipientUnavailable = errors.New("triggerconfig: secret recipient unavailable")
	ErrIdentityUnavailable  = errors.New("triggerconfig: secret identity unavailable")
)

const (
	kafkaSASLPasswordNamespace  = "trigger_kafka_sasl_password"
	kafkaTLSClientKeyNamespace  = "trigger_kafka_tls_client_key"
	redisPasswordNamespace      = "trigger_redis_password"
	natsURLCredentialsNamespace = "trigger_nats_url_credentials"
)

// A NATS server's credentials ride in its URL's userinfo
// (nats://user:password@host or nats://token@host). They are sealed beside the
// URL, which keeps only the host.
const (
	natsURLField             = "url"
	natsURLCredentialsSealed = "url_credentials_sealed"
	natsURLCredentialsMarker = "url_credentials_set"
)

type rawObject map[string]json.RawMessage

// secretLeaf is one credential string in a trigger config. An empty block
// names a leaf of the top-level object.
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
		plain: "password", sealed: "password_sealed", marker: "password_set",
		namespace: redisPasswordNamespace, maxBytes: api.RedisStreamsPasswordMaxBytes,
	},
}

// secretLeaves lists the credential leaves of a kind's config. NATS keeps its
// credentials in the URL and is handled apart.
func secretLeaves(kind api.TriggerKind) []secretLeaf {
	switch kind {
	case api.TriggerKindKafka:
		return kafkaSecretLeaves
	case api.TriggerKindRedisStreams:
		return redisSecretLeaves
	}
	return nil
}

// leafBlock returns the object holding leaf: top itself for a top-level leaf.
func leafBlock(top rawObject, leaf secretLeaf) (rawObject, bool, error) {
	if leaf.block == "" {
		return top, true, nil
	}
	return nestedObject(top, leaf.block)
}

// putLeafBlock stores an edited block back; a top-level leaf was edited in place.
func putLeafBlock(top rawObject, leaf secretLeaf, block rawObject) error {
	if leaf.block == "" {
		return nil
	}
	return setNestedObject(top, leaf.block, block)
}

func leafName(leaf secretLeaf) string {
	if leaf.block == "" {
		return leaf.plain
	}
	return leaf.block + "." + leaf.plain
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

// Seal replaces plaintext credentials (Kafka SASL password and TLS client key,
// Redis password, NATS URL userinfo) with age ciphertext. A recipient is only
// required when a plaintext credential is present.
func Seal(kind api.TriggerKind, raw json.RawMessage, recipient *age.X25519Recipient) (json.RawMessage, error) {
	if kind == api.TriggerKindNATS {
		return sealNATSURL(raw, recipient)
	}
	leaves := secretLeaves(kind)
	if leaves == nil {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s config: %w", kind, err)
	}
	changed := false
	for _, leaf := range leaves {
		block, exists, err := leafBlock(top, leaf)
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
			if err := putLeafBlock(top, leaf, block); err != nil {
				return nil, err
			}
			continue
		}
		if recipient == nil {
			return nil, ErrRecipientUnavailable
		}
		plaintext, err := decodeString(plainRaw, leafName(leaf))
		if err != nil {
			return nil, err
		}
		sealedRaw, err := sealString(recipient, leaf.namespace, plaintext, leaf.maxBytes, leafName(leaf))
		if err != nil {
			return nil, err
		}
		delete(block, leaf.plain)
		block[leaf.sealed] = sealedRaw
		if err := putLeafBlock(top, leaf, block); err != nil {
			return nil, err
		}
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

// Open restores sealed credentials for runtime use. Legacy plaintext leaves
// remain supported during the rollout window.
func Open(kind api.TriggerKind, raw json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	if kind == api.TriggerKindNATS {
		return openNATSURL(raw, identities)
	}
	leaves := secretLeaves(kind)
	if leaves == nil {
		return cloneRaw(raw), nil
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s config: %w", kind, err)
	}
	changed := false
	for _, leaf := range leaves {
		block, exists, err := leafBlock(top, leaf)
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
			if err := putLeafBlock(top, leaf, block); err != nil {
				return nil, err
			}
			continue
		}
		plaintext, err := openString(identities, leaf.namespace, sealedRaw, leafName(leaf))
		if err != nil {
			return nil, err
		}
		plainRaw, err := json.Marshal(plaintext)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", leafName(leaf), err)
		}
		delete(block, leaf.sealed)
		block[leaf.plain] = plainRaw
		if err := putLeafBlock(top, leaf, block); err != nil {
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
// config and reports only whether each credential is configured.
func Redact(kind api.TriggerKind, raw json.RawMessage) json.RawMessage {
	if kind == api.TriggerKindNATS {
		return redactNATSURL(raw)
	}
	leaves := secretLeaves(kind)
	if leaves == nil {
		return cloneRaw(raw)
	}
	top, err := decodeObject(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	for _, leaf := range leaves {
		block, exists, err := leafBlock(top, leaf)
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
		if err := putLeafBlock(top, leaf, block); err != nil {
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
// its plaintext leaf is omitted. Omitting the whole block removes it; a
// top-level leaf (the Redis password) is kept whenever it is omitted.
func MergeForUpdate(kind api.TriggerKind, stored, incoming json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	if kind == api.TriggerKindNATS {
		return mergeNATSURL(stored, incoming, identities)
	}
	leaves := secretLeaves(kind)
	if leaves == nil {
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
		incomingBlock, supplied, err := leafBlock(incomingTop, leaf)
		if err != nil {
			return nil, err
		}
		if !supplied {
			continue
		}
		delete(incomingBlock, leaf.marker)
		delete(incomingBlock, leaf.sealed)
		if _, hasIncomingSecret := incomingBlock[leaf.plain]; !hasIncomingSecret {
			storedBlock, exists, err := leafBlock(storedTop, leaf)
			if err != nil {
				return nil, err
			}
			if exists {
				if storedSecret, ok := storedBlock[leaf.plain]; ok {
					incomingBlock[leaf.plain] = cloneRaw(storedSecret)
				}
			}
		}
		if err := putLeafBlock(incomingTop, leaf, incomingBlock); err != nil {
			return nil, err
		}
	}
	return encodeObject(incomingTop)
}

func sealString(recipient *age.X25519Recipient, namespace, plaintext string, maxBytes int, name string) (json.RawMessage, error) {
	ciphertext, err := secretbox.SealBytes(recipient, namespace, []byte(plaintext), maxBytes)
	if err != nil {
		return nil, fmt.Errorf("seal %s: %w", name, err)
	}
	sealedRaw, err := json.Marshal(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("encode %s envelope: %w", name, err)
	}
	return sealedRaw, nil
}

func openString(identities []*age.X25519Identity, namespace string, sealedRaw json.RawMessage, name string) (string, error) {
	if len(identities) == 0 {
		return "", ErrIdentityUnavailable
	}
	var ciphertext []byte
	if err := json.Unmarshal(sealedRaw, &ciphertext); err != nil {
		return "", fmt.Errorf("decode %s envelope: %w", name, err)
	}
	opened, plaintext, err := secretbox.OpenBytesMulti(identities, ciphertext)
	if err != nil {
		return "", fmt.Errorf("open %s envelope: %w", name, err)
	}
	if opened != namespace {
		return "", fmt.Errorf("open %s envelope: namespace mismatch", name)
	}
	return string(plaintext), nil
}

// natsURL reads the config's URL. A missing or unparsable URL is left to
// trigger validation, so it reports ok=false rather than an error.
func natsURL(top rawObject) (*url.URL, bool, error) {
	raw, exists := top[natsURLField]
	if !exists {
		return nil, false, nil
	}
	value, err := decodeString(raw, natsURLField)
	if err != nil {
		return nil, false, err
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, false, nil
	}
	return parsed, true, nil
}

func setNATSURL(top rawObject, u *url.URL) error {
	raw, err := json.Marshal(u.String())
	if err != nil {
		return err
	}
	top[natsURLField] = raw
	return nil
}

// withUserinfo returns u carrying userinfo in its escaped form
// ("user:password" or "token").
func withUserinfo(u *url.URL, userinfo string) (*url.URL, error) {
	parsed, err := url.Parse(u.Scheme + "://" + userinfo + "@" + u.Host)
	if err != nil || parsed.User == nil {
		return nil, errors.New("nats url credentials are malformed")
	}
	out := *u
	out.User = parsed.User
	return &out, nil
}

func sealNATSURL(raw json.RawMessage, recipient *age.X25519Recipient) (json.RawMessage, error) {
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode nats config: %w", err)
	}
	_, hasMarker := top[natsURLCredentialsMarker]
	_, suppliedCiphertext := top[natsURLCredentialsSealed]
	delete(top, natsURLCredentialsMarker)
	delete(top, natsURLCredentialsSealed)
	changed := hasMarker || suppliedCiphertext
	u, ok, err := natsURL(top)
	if err != nil {
		return nil, err
	}
	if ok && u.User != nil {
		if recipient == nil {
			return nil, ErrRecipientUnavailable
		}
		sealedRaw, err := sealString(recipient, natsURLCredentialsNamespace, u.User.String(), api.NATSURLCredentialsMaxBytes, "url credentials")
		if err != nil {
			return nil, err
		}
		u.User = nil
		if err := setNATSURL(top, u); err != nil {
			return nil, err
		}
		top[natsURLCredentialsSealed] = sealedRaw
		changed = true
	}
	if !changed {
		return cloneRaw(raw), nil
	}
	return encodeObject(top)
}

func openNATSURL(raw json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	top, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("decode nats config: %w", err)
	}
	sealedRaw, hasSealed := top[natsURLCredentialsSealed]
	_, hasMarker := top[natsURLCredentialsMarker]
	if !hasSealed && !hasMarker {
		return cloneRaw(raw), nil
	}
	delete(top, natsURLCredentialsMarker)
	delete(top, natsURLCredentialsSealed)
	if hasSealed {
		userinfo, err := openString(identities, natsURLCredentialsNamespace, sealedRaw, "url credentials")
		if err != nil {
			return nil, err
		}
		u, ok, err := natsURL(top)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("nats url credentials are sealed without a url")
		}
		if u, err = withUserinfo(u, userinfo); err != nil {
			return nil, err
		}
		if err := setNATSURL(top, u); err != nil {
			return nil, err
		}
	}
	return encodeObject(top)
}

func redactNATSURL(raw json.RawMessage) json.RawMessage {
	top, err := decodeObject(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	_, hasSealed := top[natsURLCredentialsSealed]
	delete(top, natsURLCredentialsSealed)
	delete(top, natsURLCredentialsMarker)
	u, ok, err := natsURL(top)
	if err != nil {
		return json.RawMessage("{}")
	}
	if ok && u.User != nil {
		// A config stored before sealing keeps its userinfo in the URL.
		hasSealed = true
		u.User = nil
		if setNATSURL(top, u) != nil {
			return json.RawMessage("{}")
		}
	} else if _, exists := top[natsURLField]; exists && !ok {
		// An unparsable URL could still hold a credential.
		top[natsURLField] = json.RawMessage(`""`)
	}
	if hasSealed {
		top[natsURLCredentialsMarker] = json.RawMessage("true")
	}
	redacted, err := encodeObject(top)
	if err != nil {
		return json.RawMessage("{}")
	}
	return redacted
}

// mergeNATSURL keeps the stored URL credentials when the incoming URL omits
// them and still names the same server, so a client can round-trip the
// redacted config. Credentials never move to a different server.
func mergeNATSURL(stored, incoming json.RawMessage, identities []*age.X25519Identity) (json.RawMessage, error) {
	openedStored, err := openNATSURL(stored, identities)
	if err != nil {
		return nil, fmt.Errorf("open stored nats config: %w", err)
	}
	storedTop, err := decodeObject(openedStored)
	if err != nil {
		return nil, fmt.Errorf("decode stored nats config: %w", err)
	}
	incomingTop, err := decodeObject(incoming)
	if err != nil {
		return nil, fmt.Errorf("decode incoming nats config: %w", err)
	}
	delete(incomingTop, natsURLCredentialsMarker)
	delete(incomingTop, natsURLCredentialsSealed)
	in, inOK, err := natsURL(incomingTop)
	if err != nil {
		return nil, err
	}
	was, wasOK, err := natsURL(storedTop)
	if err != nil {
		return nil, err
	}
	if inOK && wasOK && in.User == nil && was.User != nil && in.Scheme == was.Scheme && in.Host == was.Host {
		in.User = was.User
		if err := setNATSURL(incomingTop, in); err != nil {
			return nil, err
		}
	}
	return encodeObject(incomingTop)
}
