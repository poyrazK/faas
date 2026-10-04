package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 582
func TestVersionProtectionNativeRejectionMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		rejected bool
	}{
		{"denied", 403, `<Error><Code>AccessDenied</Code></Error>`, true},
		{"missing", 404, `<Error><Code>NoSuchVersion</Code></Error>`, true},
		{"invalid", 400, `<Error><Code>InvalidArgument</Code></Error>`, true},
		{"lost_ack", 500, `<Error><Code>InternalError</Code></Error>`, false},
		{"unknown_denial", 403, `<Error><Code>FutureDenial</Code></Error>`, false},
		{"duplicate_code", 403, `<Error><Code>AccessDenied</Code><Code>InternalError</Code></Error>`, false},
		{"embedded_error", 200, `<Error><Code>AccessDenied</Code></Error>`, false},
		{"invalid_xml", 403, `<Error><Code>AccessDenied</Code>`, false},
	} {
		for _, kind := range []string{"retention", "legal_hold"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				var calls atomic.Int32
				p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				})).(ObjectVersionLockProvider)
				var err error
				if kind == "retention" {
					err = p.PutObjectVersionRetention(t.Context(), "bucket", "key", "version", api.ObjectVersionRetention{}, false)
				} else {
					err = p.PutObjectVersionLegalHold(t.Context(), "bucket", "key", "version", api.ObjectVersionLegalHold{Status: "ON"})
				}
				if err == nil || errors.Is(err, ErrProtectionRejected) != tc.rejected || calls.Load() != 1 {
					t.Fatal("unsafe mutation outcome", err, calls.Load())
				}
			})
		}
	}
}
