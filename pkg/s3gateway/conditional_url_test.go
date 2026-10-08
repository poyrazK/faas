package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// adr: 732
func TestConditionalURLHeadersCannotChange(t *testing.T) {
	size := int64(3)
	for _, condition := range []objectstorage.ObjectWriteConditions{{IfMatch: `"old"`}, {IfNoneMatch: "*"}} {
		expected, err := objectstorage.PublicSignedObjectHeaders(objectstorage.SignRequest{Method: "PUT", Key: "key", SizeBytes: &size, IfMatch: condition.IfMatch, IfNoneMatch: condition.IfNoneMatch})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name string
			edit func(http.Header)
			want bool
		}{
			{"unchanged", func(http.Header) {}, true},
			{"removed", func(h http.Header) { h.Del("If-Match"); h.Del("If-None-Match") }, false},
			{"different", func(h http.Header) { h.Set("If-Match", `"changed"`) }, false},
			{"duplicate", func(h http.Header) {
				for k := range expected {
					h.Add(k, expected.Get(k))
				}
			}, false},
			{"incompatible addition", func(h http.Header) { h.Set("If-Unmodified-Since", "Thu, 01 Oct 2026 00:00:00 GMT") }, false},
		} {
			t.Run(condition.IfMatch+condition.IfNoneMatch+tc.name, func(t *testing.T) {
				r := httptest.NewRequest("PUT", "https://public.test/assets/key", nil)
				r.Header = expected.Clone()
				tc.edit(r.Header)
				if fixedURLHeaders(r, expected) != tc.want {
					t.Fatal("changed write authority accepted")
				}
			})
		}
	}
	if fixedURLHeaders(httptest.NewRequest("PUT", "https://public.test/assets/key", nil), http.Header{"If-Match": {`"old"`}}) {
		t.Fatal("missing bound header accepted")
	}
}
