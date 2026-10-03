package api

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// adr: 412
func TestObjectEncryptionValidation(t *testing.T) {
	reference := "arn:gregale:kms:us-east-1:2fffa69f-b206-47db-8d82-8d8d394a630d:key/0f8a8987-12d6-45ad-a4bc-d384c10d9149"
	yes := true
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	for _, tc := range []struct {
		name  string
		in    ObjectEncryption
		valid bool
	}{
		{"omitted", ObjectEncryption{}, true},
		{"provider managed", ObjectEncryption{Algorithm: "AES256"}, true},
		{"owned KMS", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, BucketKeyEnabled: &yes, Context: encode(`{"purpose":"test","unicode":"世界"}`)}, true},
		{"dual layer", ObjectEncryption{Algorithm: "aws:kms:dsse", KeyID: reference}, true},
		{"missing algorithm", ObjectEncryption{KeyID: reference}, false},
		{"raw key", ObjectEncryption{Algorithm: "aws:kms", KeyID: "raw-key-material"}, false},
		{"native resource", ObjectEncryption{Algorithm: "aws:kms", KeyID: "arn:aws:kms:us-east-1:111122223333:key/0f8a8987-12d6-45ad-a4bc-d384c10d9149"}, false},
		{"malformed reference", ObjectEncryption{Algorithm: "aws:kms", KeyID: "arn:gregale:kms:whatever"}, false},
		{"uppercase identity", ObjectEncryption{Algorithm: "aws:kms", KeyID: strings.ToUpper(reference)}, false},
		{"nil identity", ObjectEncryption{Algorithm: "aws:kms", KeyID: strings.Replace(reference, "0f8a8987-12d6-45ad-a4bc-d384c10d9149", "00000000-0000-0000-0000-000000000000", 1)}, false},
		{"AES key", ObjectEncryption{Algorithm: "AES256", KeyID: reference}, false},
		{"AES context", ObjectEncryption{Algorithm: "AES256", Context: encode(`{}`)}, false},
		{"dual layer bucket key", ObjectEncryption{Algorithm: "aws:kms:dsse", KeyID: reference, BucketKeyEnabled: &yes}, false},
		{"empty context object", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{}`)}, true},
		{"duplicate context", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"a":"one","a":"two"}`)}, false},
		{"null value", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"a":null}`)}, false},
		{"number value", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"a":42}`)}, false},
		{"nested value", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"a":{"b":"c"}}`)}, false},
		{"array", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`[]`)}, false},
		{"empty key", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"":"x"}`)}, false},
		{"trailing document", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{} {}`)}, false},
		{"invalid UTF8", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(string([]byte{255}))}, false},
		{"bad base64", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: "invalid"}, false},
		{"line break base64", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{}`) + "\n"}, false},
		{"context overflow", ObjectEncryption{Algorithm: "aws:kms", KeyID: reference, Context: encode(`{"a":"` + strings.Repeat("x", MaxObjectEncryptionContextBytes) + `"}`)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.in.Valid() != tc.valid {
				t.Fatal("validation mismatch")
			}
		})
	}
}

func TestObjectEncryptionContextEntryLimit(t *testing.T) {
	var entries []string
	for i := 0; i < MaxObjectEncryptionContextEntries; i++ {
		entries = append(entries, `"key`+strconv.Itoa(i)+`":"x"`)
	}
	for _, extra := range []bool{false, true} {
		copyEntries := append([]string(nil), entries...)
		if extra {
			copyEntries = append(copyEntries, `"extra":"x"`)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte("{" + strings.Join(copyEntries, ",") + "}"))
		if validObjectEncryptionContext(encoded) == extra {
			t.Fatal("context entry bound", extra)
		}
	}
}
