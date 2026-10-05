package objectstorage

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/api"
)

// Bound control responses before the SDK allocates them. Duplicate identity
// fields cannot silently select the last key metadata supplied by a parser.
type encryptionKeyReadClient struct{ base aws.HTTPClient }

func (c encryptionKeyReadClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.base.Do(r)
	if err != nil || response == nil {
		return response, err
	}
	if response.Body == nil {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectEncryptionProviderResponseBytes+1))
	_ = response.Body.Close()
	if err != nil || len(body) > api.MaxObjectEncryptionProviderResponseBytes || response.StatusCode >= 200 && response.StatusCode < 300 && !validEncryptionKeyJSON(body) {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func validEncryptionKeyJSON(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') || !validEncryptionJSONContainer(d, json.Delim('{'), 1) {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}

func validEncryptionJSONContainer(d *json.Decoder, kind json.Delim, depth int) bool {
	if depth > api.MaxObjectEncryptionJSONDepth {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		if kind == '{' {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
		}
		value, err := d.Token()
		if err != nil {
			return false
		}
		if nested, ok := value.(json.Delim); ok && (!validEncryptionJSONContainer(d, nested, depth+1) || nested != '{' && nested != '[') {
			return false
		}
	}
	closing, err := d.Token()
	return err == nil && (kind == '{' && closing == json.Delim('}') || kind == '[' && closing == json.Delim(']'))
}
