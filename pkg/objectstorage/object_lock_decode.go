package objectstorage

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func DecodeBucketObjectLockConfiguration(body []byte) (api.ObjectBucketObjectLockConfiguration, error) {
	if int64(len(body)) > api.MaxObjectLockBodyBytes {
		return api.ObjectBucketObjectLockConfiguration{}, ErrInvalid
	}
	c, err := parseBucketObjectLock(body)
	if err != nil || !c.Enabled || !c.Valid() {
		return api.ObjectBucketObjectLockConfiguration{}, ErrInvalid
	}
	return c.Clone(), nil
}

// Exact keys, duplicate rejection and null rejection precede typed decoding.
// In particular a null default or a case-folded duplicate must not become an
// accidental clear/default selection through encoding/json's permissive rules.
func DecodeObjectBucketObjectLockRequest(body []byte) (api.ObjectBucketObjectLockConfiguration, error) {
	var in api.ObjectBucketObjectLockRequest
	if int64(len(body)) > api.MaxObjectLockBodyBytes {
		return in.Configuration, ErrInvalid
	}
	schema := map[string]bool{"": true, "configuration": true, "configuration/enabled": false, "configuration/default_retention": true, "configuration/default_retention/mode": false, "configuration/default_retention/days": false, "configuration/default_retention/years": false, "configuration/default_retention/default_event_hold": true, "configuration/default_retention/default_event_hold/days": false, "configuration/default_retention/default_event_hold/years": false}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	fields := 0
	if err := objectLockJSONValue(d, schema, "", 0, &fields); err != nil {
		return in.Configuration, ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return in.Configuration, ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !in.Configuration.Enabled || !in.Configuration.Valid() {
		return api.ObjectBucketObjectLockConfiguration{}, ErrInvalid
	}
	return in.Configuration.Clone(), nil
}

func objectLockJSONValue(d *json.Decoder, schema map[string]bool, path string, depth int, fields *int) error {
	container, known := schema[path]
	if !known || depth > api.MaxObjectLockJSONDepth {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return ErrInvalid
	}
	if !container {
		if _, compound := token.(json.Delim); compound {
			return ErrInvalid
		}
		return nil
	}
	if token != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		name, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		key, ok := name.(string)
		*fields++
		if !ok || seen[key] || *fields > api.MaxObjectLockJSONFields {
			return ErrInvalid
		}
		seen[key] = true
		child := key
		if path != "" {
			child = path + "/" + key
		}
		if err = objectLockJSONValue(d, schema, child, depth+1, fields); err != nil {
			return err
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}
