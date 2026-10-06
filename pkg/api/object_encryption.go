package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ObjectEncryption selects an owned managed key. KeyID is a Gregale key
// reference; raw key material and provider resource names are not accepted.
type ObjectEncryption struct {
	Algorithm        string `json:"algorithm,omitempty"`
	KeyID            string `json:"key_id,omitempty"`
	BucketKeyEnabled *bool  `json:"bucket_key_enabled,omitempty"`
	Context          string `json:"context,omitempty"`
}

func (e ObjectEncryption) Empty() bool {
	return e.Algorithm == "" && e.KeyID == "" && e.BucketKeyEnabled == nil && e.Context == ""
}

func (e ObjectEncryption) Valid() bool {
	if e.Empty() {
		return true
	}
	if e.Algorithm == "AES256" {
		return e.KeyID == "" && e.BucketKeyEnabled == nil && e.Context == ""
	}
	if e.Algorithm != "aws:kms" && e.Algorithm != "aws:kms:dsse" || !validObjectEncryptionKeyReference(e.KeyID) {
		return false
	}
	if e.Algorithm == "aws:kms:dsse" && e.BucketKeyEnabled != nil {
		return false
	}
	return validObjectEncryptionContext(e.Context)
}

func validObjectEncryptionKeyReference(value string) bool {
	if len(value) > MaxObjectEncryptionKeyRefBytes {
		return false
	}
	parts := strings.Split(value, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "gregale" || parts[2] != "kms" || !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(parts[3]) || !strings.HasPrefix(parts[5], "key/") {
		return false
	}
	valid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	account, key := parts[4], strings.TrimPrefix(parts[5], "key/")
	return valid.MatchString(account) && valid.MatchString(key) && account != "00000000-0000-0000-0000-000000000000" && key != "00000000-0000-0000-0000-000000000000"
}

// The context is non-secret AAD. Reject ambiguous JSON rather than letting
// different parsers interpret duplicate entries or non-string values.
func validObjectEncryptionContext(encoded string) bool {
	if encoded == "" {
		return true
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxObjectEncryptionContextBytes) {
		return false
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > MaxObjectEncryptionContextBytes || !utf8.Valid(data) || base64.StdEncoding.EncodeToString(data) != encoded {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || key == "" || seen[key] || len(seen) >= MaxObjectEncryptionContextEntries {
			return false
		}
		seen[key] = true
		value, valueErr := d.Token()
		if _, ok := value.(string); valueErr != nil || !ok {
			return false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}
