package objectstorage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func DecodeObjectVersionRetention(body []byte) (api.ObjectVersionRetention, error) {
	if int64(len(body)) > api.MaxObjectLockBodyBytes {
		return api.ObjectVersionRetention{}, ErrInvalid
	}
	r, err := parseObjectRetentionPolicy(body, false)
	if err != nil || !r.ValidForWrite() {
		return api.ObjectVersionRetention{}, ErrInvalid
	}
	return r.ForWrite(), nil
}
func DecodeObjectVersionLegalHold(body []byte) (api.ObjectVersionLegalHold, error) {
	if int64(len(body)) > api.MaxObjectLockBodyBytes {
		return api.ObjectVersionLegalHold{}, ErrInvalid
	}
	h, err := parseObjectLegalHold(body)
	if err != nil || !h.Valid() {
		return api.ObjectVersionLegalHold{}, ErrInvalid
	}
	return h, nil
}
func DecodeObjectVersionProtectionRequest(body []byte, kind string) (api.ObjectVersionRetentionRequest, api.ObjectVersionLegalHoldRequest, error) {
	var r api.ObjectVersionRetentionRequest
	var h api.ObjectVersionLegalHoldRequest
	schema := map[string]bool{"": true, "id": false}
	var out any
	switch kind {
	case "retention":
		out = &r
		for path, container := range map[string]bool{"retention": true, "retention/mode": false, "retention/retain_until_date": false, "retention/event_hold": false, "retention/event_hold_duration": true, "retention/event_hold_duration/days": false, "retention/event_hold_duration/years": false} {
			schema[path] = container
		}
	case "legal_hold":
		out = &h
		schema["legal_hold"] = true
		schema["legal_hold/status"] = false
	default:
		return r, h, ErrInvalid
	}
	if int64(len(body)) > api.MaxObjectLockBodyBytes {
		return r, h, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	fields := 0
	if err := objectLockJSONValue(d, schema, "", 0, &fields); err != nil {
		return r, h, ErrInvalid
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return r, h, ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return r, h, ErrInvalid
	}
	// Required containers must not disappear into a zero-valued clear request.
	var members map[string]json.RawMessage
	if json.Unmarshal(body, &members) != nil {
		return r, h, ErrInvalid
	}
	if _, ok := members[kind]; !ok {
		return r, h, ErrInvalid
	}
	return r, h, nil
}
