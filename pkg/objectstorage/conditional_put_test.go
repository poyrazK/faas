package objectstorage

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// adr: 818
func TestS3ConditionalPutSavedAuthority(t *testing.T) {
	p, err := NewS3(testBackend(), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(3)
	for _, condition := range []ObjectWriteConditions{{IfMatch: `"old"`}, {IfNoneMatch: "*"}} {
		r := SignRequest{Method: "PUT", Key: "key", SizeBytes: &size, IfMatch: condition.IfMatch, IfNoneMatch: condition.IfNoneMatch}
		out, err := p.Presign(t.Context(), "physical", r)
		if err != nil {
			t.Fatal(err)
		}
		name, value := "If-Match", condition.IfMatch
		if condition.IfNoneMatch != "" {
			name, value = "If-None-Match", condition.IfNoneMatch
		}
		u, _ := url.Parse(out.URL)
		if http.Header(mapHeaders(out.Headers)).Get(name) != value || !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), strings.ToLower(name)) {
			t.Fatal("saved write condition not signed")
		}
		if _, err := p.(ConditionalObjectPresigner).PresignConditionalPut(t.Context(), "physical", r, ObjectWriteConditions{IfMatch: `"changed"`}); !errors.Is(err, ErrInvalid) {
			t.Fatal("saved authority was weakened", err)
		}
	}
}

// adr: 818
func TestConditionalPutAdmissionCapabilities(t *testing.T) {
	p := &GCS{}
	for _, c := range []ObjectWriteConditions{{IfNoneMatch: "*"}, {IfMatch: `"old"`}, {IfMatch: "*"}} {
		if err := ValidateConditionalPut(p, c); err != nil {
			t.Fatal("valid native conditional capability rejected", err)
		}
		if err := ValidateConditionalPut(struct{ Provider }{p}, c); !errors.Is(err, ErrUnsupported) {
			t.Fatal("provider capability silently bypassed", err)
		}
	}
	for _, c := range []ObjectWriteConditions{{IfMatch: `W/"old"`}, {IfMatch: `"one", "two"`}, {IfMatch: "unquoted"}} {
		if err := ValidateConditionalPut(p, c); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unrepresentable GCS ETag admitted", err)
		}
	}
}
