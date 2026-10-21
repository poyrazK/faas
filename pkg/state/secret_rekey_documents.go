package state

import (
	"bytes"
	"context"
	"encoding/json"
)

// SecretRekeyDocument is private maintenance input, never an API receipt.
// ID addresses one physical copy; logical secret version IDs remain in Payload.
type SecretRekeyDocument struct {
	Kind, ID string
	Payload  json.RawMessage
}

func (d SecretRekeyDocument) Cursor() string { return d.Kind + "|" + d.ID }

type SecretRekeyEnvelope struct {
	Key, Kid, ValueHash string
	Ciphertext          []byte
}

type SecretEncryptionKeyUsage struct {
	LiveSecrets, StagedBundles, DeploymentSnapshots, ReleaseCaptures int64
}

func (u SecretEncryptionKeyUsage) Empty() bool {
	return u == (SecretEncryptionKeyUsage{})
}

type SecretRekeyStore interface {
	ListSecretRekeyDocuments(context.Context, string, string, int) ([]SecretRekeyDocument, error)
	CompareAndSwapSecretRekeyDocument(context.Context, SecretRekeyDocument, json.RawMessage) error
	SecretEncryptionKeyUsage(context.Context, string) (SecretEncryptionKeyUsage, error)
	RetireSecretEncryptionKey(context.Context, string) error
}

// TransformSecretRekeyDocument visits only the known sealed fields. In particular,
// customer environment objects whose keys happen to be "ciphertext" are ignored.
func TransformSecretRekeyDocument(d SecretRekeyDocument, transform func(SecretRekeyEnvelope) (SecretRekeyEnvelope, error)) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(d.Payload))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, ErrConflict
	}
	visit := func(array any) error {
		if array == nil {
			return nil
		}
		rows, ok := array.([]any)
		if !ok {
			return ErrConflict
		}
		for _, entry := range rows {
			row, ok := entry.(map[string]any)
			if !ok {
				return ErrConflict
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return ErrConflict
			}
			var envelope struct {
				Key        string `json:"key"`
				Kid        string `json:"kid"`
				ValueHash  string `json:"value_hash"`
				Ciphertext []byte `json:"ciphertext"`
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope.Key == "" || len(envelope.Ciphertext) == 0 {
				return ErrConflict
			}
			next, err := transform(SecretRekeyEnvelope{envelope.Key, envelope.Kid, envelope.ValueHash, envelope.Ciphertext})
			if err != nil {
				return err
			}
			if next.Key != envelope.Key || next.ValueHash != envelope.ValueHash || next.Kid == "" || len(next.Ciphertext) == 0 {
				return ErrConflict
			}
			encoded, _ := json.Marshal(next.Ciphertext)
			var ciphertext string
			_ = json.Unmarshal(encoded, &ciphertext)
			row["kid"], row["ciphertext"] = next.Kid, ciphertext
		}
		return nil
	}
	switch d.Kind {
	case "capture", "promotion":
		capture, ok := value.(map[string]any)
		if !ok {
			return nil, ErrConflict
		}
		workloads, ok := capture["workloads"].([]any)
		if !ok {
			return nil, ErrConflict
		}
		for _, entry := range workloads {
			workload, ok := entry.(map[string]any)
			if !ok {
				return nil, ErrConflict
			}
			for _, field := range []string{"live_secrets", "secret_snapshot", "secret_target"} {
				if err := visit(workload[field]); err != nil {
					return nil, err
				}
			}
		}
	case "app", "bundle", "deployment", "project":
		if err := visit(value); err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidArgument
	}
	return json.Marshal(value)
}

func validSecretRekeyReplacement(before SecretRekeyDocument, after json.RawMessage) bool {
	strip := func(d SecretRekeyDocument) (json.RawMessage, error) {
		return TransformSecretRekeyDocument(d, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
			e.Kid, e.Ciphertext = "comparison", []byte{1}
			return e, nil
		})
	}
	a, err := strip(before)
	if err != nil {
		return false
	}
	b, err := strip(SecretRekeyDocument{before.Kind, before.ID, after})
	if err != nil {
		return false
	}
	return bytes.Equal(a, b)
}

func secretRekeyDocumentNeedsKey(d SecretRekeyDocument, kid string, other bool) (bool, error) {
	needed := false
	_, err := TransformSecretRekeyDocument(d, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
		if other && e.Kid != kid || !other && (e.Kid == kid || e.Kid == "") {
			needed = true
		}
		if e.Kid == "" {
			e.Kid = "unlabelled"
		}
		return e, nil
	})
	return needed, err
}
