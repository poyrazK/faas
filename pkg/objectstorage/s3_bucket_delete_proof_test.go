package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestS3BucketDeleteRequiresTerminalProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"acknowledged", 204, "", nil},
		{"confirmed missing", 404, `<Error><Code>NoSuchBucket</Code></Error>`, nil},
		{"unknown missing", 404, "", ErrUnavailable},
		{"duplicate missing code", 404, `<Error><Code>NoSuchBucket</Code><Code>NoSuchBucket</Code></Error>`, ErrUnavailable},
		{"conflicting missing code", 404, `<Error><Code>NoSuchKey</Code><Code>NoSuchBucket</Code></Error>`, ErrUnavailable},
		{"incomplete missing document", 404, `<Error><Code>NoSuchBucket</Code></Error><Error/>`, ErrUnavailable},
		{"wrong missing resource", 404, `<Error><Code>NoSuchKey</Code></Error>`, ErrUnavailable},
		{"wrong success status", 200, `<Error><Code>InternalError</Code></Error>`, ErrUnavailable},
		{"uncertain", 503, `<Error><Code>InternalError</Code></Error>`, ErrUnavailable},
		{"nonempty", 409, `<Error><Code>BucketNotEmpty</Code></Error>`, ErrNotEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodDelete || r.URL.Path != "/bucket" {
					t.Error("wrong delete target", r.URL)
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})).(Provider)
			if err := p.DeleteBucket(t.Context(), "bucket"); !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatal("unproven deletion or hidden retry", err, calls.Load())
			}
		})
	}
}
