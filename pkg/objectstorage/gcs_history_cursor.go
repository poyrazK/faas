package objectstorage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

type gcsHistoryCursor struct {
	Binding string `json:"b"`
	Token   string `json:"t"`
}

func decodeGCSHistoryCursor(bucket string, r ObjectHistoryProofRequest) (string, error) {
	if r.Cursor == "" {
		return "", nil
	}
	if len(r.Cursor) > api.ObjectUploadHistoryCursorMaxBytes {
		return "", ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(r.Cursor)
	if err != nil {
		return "", ErrInvalid
	}
	var c gcsHistoryCursor
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Binding != historyBinding(bucket, r) || c.Token == "" || len(c.Token) > api.ObjectVersionInventoryCursorMaxBytes {
		return "", ErrInvalid
	}
	return c.Token, nil
}

func encodeGCSHistoryCursor(bucket string, r ObjectHistoryProofRequest, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	raw, err := json.Marshal(gcsHistoryCursor{Binding: historyBinding(bucket, r), Token: token})
	if err != nil {
		return "", ErrUnavailable
	}
	out := base64.RawURLEncoding.EncodeToString(raw)
	if len(out) > api.ObjectUploadHistoryCursorMaxBytes {
		return "", ErrUnavailable
	}
	return out, nil
}
