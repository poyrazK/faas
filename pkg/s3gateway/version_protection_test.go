package s3gateway

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// adr: 572
func TestVersionProtectionRetryIDMustBeSigned(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		name, signed string
		values       []string
		valid        bool
	}{
		{"absent", "host", nil, true},
		{"signed", "host;x-gregale-protection-id", []string{id}, true},
		{"unsigned", "host", []string{id}, false},
		{"duplicate", "host;x-gregale-protection-id", []string{id, id}, false},
		{"empty", "host;x-gregale-protection-id", []string{""}, false},
		{"noncanonical", "host;x-gregale-protection-id", []string{"{" + id + "}"}, false},
		{"wrong_uuid_variant", "host;x-gregale-protection-id", []string{"00000000-0000-4000-0000-000000000001"}, false},
		{"wrong_uuid_version", "host;x-gregale-protection-id", []string{uuid.NewSHA1(uuid.Nil, []byte("retry")).String()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "https://s3.test/b/key?legal-hold&versionId=null", nil)
			r.Header["X-Gregale-Protection-Id"] = tc.values
			req := requestContext{}
			req.signature.SignedHeader = tc.signed
			got, err := protectionRequestID(r, req)
			if tc.valid {
				u, parseErr := uuid.Parse(got)
				if err != nil || parseErr != nil || u.Version() != 4 || len(tc.values) > 0 && got != id {
					t.Fatal(got, err, parseErr)
				}
			} else if !errors.Is(err, objectstorage.ErrInvalid) {
				t.Fatal("untrusted retry identity accepted", got, err)
			}
		})
	}
}
