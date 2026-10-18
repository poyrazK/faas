// Package objectinventory defines the private owned-node inventory protocol.
// These observations describe capacity; they are not cumulative billing meters.
package objectinventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	Version       = 1
	Query         = "gregale-inventory"
	Buckets       = "buckets"
	Objects       = "objects"
	Uploads       = "uploads"
	UsageKind     = "usage"
	ScrubKind     = "scrub"
	ScrubFindings = "scrub-findings"
)

type Bucket struct {
	Name       string    `json:"name"`
	Generation string    `json:"generation"`
	CreatedAt  time.Time `json:"created_at"`
}

type Object struct {
	Key      string    `json:"key"`
	ID       string    `json:"id"`
	Size     int64     `json:"size"`
	ETag     string    `json:"etag"`
	SHA256   string    `json:"sha256"`
	Modified time.Time `json:"modified"`
}

type Upload struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Status    string    `json:"status"`
	Size      int64     `json:"size"`
	Parts     int64     `json:"parts"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Continuations expire after mutation or restart. Callers must discard all
// partial totals before retrying a scan against the new catalog revision.
type Page struct {
	Version    int       `json:"version"`
	Kind       string    `json:"kind"`
	Bucket     string    `json:"bucket"`
	Generation string    `json:"generation"`
	Snapshot   string    `json:"snapshot"`
	ObservedAt time.Time `json:"observed_at"`
	Buckets    []Bucket  `json:"buckets"`
	Objects    []Object  `json:"objects"`
	Uploads    []Upload  `json:"uploads"`
	NextCursor string    `json:"next_cursor"`
}

// Distinguish measured zeroes and complete empty pages from missing evidence.
// Duplicate fields are rejected at every proof-bearing level.
func decode(data []byte, target any, required ...string) error {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid inventory object")
	}
	fields := make(map[string]json.RawMessage, len(required))
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("invalid inventory field")
		}
		if _, duplicate := fields[key]; duplicate {
			return errors.New("duplicate inventory field")
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return err
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("trailing inventory data")
	}
	if len(fields) != len(required) {
		return errors.New("incomplete inventory object")
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("missing inventory field")
		}
	}
	return json.Unmarshal(data, target)
}

func (p *Page) UnmarshalJSON(data []byte) error {
	type plain Page
	return decode(data, (*plain)(p), "version", "kind", "bucket", "generation", "snapshot", "observed_at", "buckets", "objects", "uploads", "next_cursor")
}
func (o *Object) UnmarshalJSON(data []byte) error {
	type plain Object
	return decode(data, (*plain)(o), "key", "id", "size", "etag", "sha256", "modified")
}
func (u *Upload) UnmarshalJSON(data []byte) error {
	type plain Upload
	return decode(data, (*plain)(u), "id", "key", "status", "size", "parts", "created_at", "expires_at")
}
func (b *Bucket) UnmarshalJSON(data []byte) error {
	type plain Bucket
	return decode(data, (*plain)(b), "name", "generation", "created_at")
}
