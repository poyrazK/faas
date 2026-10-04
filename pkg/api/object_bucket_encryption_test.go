package api

import (
	"encoding/json"
	"testing"
)

// adr: 419
func TestObjectBucketEncryptionRequest(t *testing.T) {
	key := "arn:gregale:kms:aws:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"
	for _, body := range []string{
		`{"encryption":{"algorithm":"AES256"}}`,
		`{"encryption":{"algorithm":"aws:kms","key_id":"` + key + `","bucket_key_enabled":true}}`,
		`{"encryption":{"algorithm":"aws:kms:dsse","key_id":"` + key + `"}}`,
	} {
		var in ObjectBucketEncryptionRequest
		if err := json.Unmarshal([]byte(body), &in); err != nil || !in.Encryption.Valid() {
			t.Fatal(body, in, err)
		}
	}
	for _, body := range []string{
		`null`, `{}`, `{"encryption":null}`, `{"encryption":{}}`,
		`{"encryption":{"algorithm":"AES256"},"extra":true}`,
		`{"encryption":{"algorithm":"AES256"},"encryption":{"algorithm":"AES256"}}`,
		`{"encryption":{"algorithm":"AES256","algorithm":"aws:kms"}}`,
		`{"encryption":{"algorithm":"AES256","Algorithm":"AES256"}}`,
		`{"encryption":{"algorithm":"AES256","unknown":true}}`,
		`{"encryption":{"algorithm":"AES256","bucket_key_enabled":false}}`,
		`{"encryption":{"algorithm":"aws:kms","key_id":"native-key"}}`,
		`{"encryption":{"algorithm":"aws:kms","key_id":"` + key + `","context":"e30="}}`,
		`{"encryption":{"algorithm":"aws:kms:dsse","key_id":"` + key + `","bucket_key_enabled":false}}`,
		`{"encryption":{"algorithm":"AES256"}} {}`,
	} {
		var in ObjectBucketEncryptionRequest
		if err := json.Unmarshal([]byte(body), &in); err == nil {
			t.Fatal("accepted ambiguous or unsupported policy", body)
		}
	}
}
