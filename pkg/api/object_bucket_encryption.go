package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type ObjectBucketEncryptionRequest struct {
	Encryption ObjectEncryption `json:"encryption"`
}

// Bucket defaults carry a single non-empty encryption selection. Duplicate
// fields and per-object encryption contexts cannot define a bucket policy.
func (r *ObjectBucketEncryptionRequest) UnmarshalJSON(body []byte) error {
	var fields map[string]json.RawMessage
	if err := decodeUniqueEncryptionFields(body, &fields); err != nil {
		return err
	}
	raw, ok := fields["encryption"]
	if !ok || len(fields) != 1 {
		return fmt.Errorf("invalid bucket encryption request")
	}
	var selection map[string]json.RawMessage
	if err := decodeUniqueEncryptionFields(raw, &selection); err != nil {
		return err
	}
	for name := range selection {
		switch name {
		case "algorithm", "key_id", "bucket_key_enabled", "context":
		default:
			return fmt.Errorf("invalid bucket encryption field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r.Encryption); err != nil {
		return err
	}
	if r.Encryption.Empty() || !r.Encryption.Valid() || r.Encryption.Context != "" {
		return fmt.Errorf("invalid bucket encryption selection")
	}
	return nil
}

func decodeUniqueEncryptionFields(body []byte, out *map[string]json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("invalid encryption object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return fmt.Errorf("invalid encryption field")
		}
		if _, exists := fields[key]; exists {
			return fmt.Errorf("duplicate encryption field")
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return err
		}
		fields[key] = raw
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return fmt.Errorf("invalid encryption object")
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("invalid trailing encryption data")
	}
	*out = fields
	return nil
}

type ObjectBucketEncryption struct {
	BucketID          string            `json:"bucket_id"`
	State             string            `json:"state"`
	Revision          int64             `json:"revision"`
	Encryption        *ObjectEncryption `json:"encryption,omitempty"`
	DesiredEncryption *ObjectEncryption `json:"desired_encryption,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}
