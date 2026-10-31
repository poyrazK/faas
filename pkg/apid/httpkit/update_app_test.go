package httpkit

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeUpdateAppRequest(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
		check      func(t *testing.T, requireAuthn *bool)
	}{
		// CLIs built before #4488 send this key as null on every app update.
		{name: "legacy null StartCommand from a pre-#4488 CLI", body: `{"require_authn":false,"StartCommand":null}`,
			check: func(t *testing.T, requireAuthn *bool) {
				if requireAuthn == nil || *requireAuthn {
					t.Fatalf("require_authn = %v, want false", requireAuthn)
				}
			}},
		{name: "current CLI omits the key", body: `{"require_authn":true}`,
			check: func(t *testing.T, requireAuthn *bool) {
				if requireAuthn == nil || !*requireAuthn {
					t.Fatalf("require_authn = %v, want true", requireAuthn)
				}
			}},
		{name: "a StartCommand value is still rejected", body: `{"StartCommand":"npm start"}`, wantErr: true},
		{name: "other unknown fields are still rejected", body: `{"require_authn":false,"bogus":1}`, wantErr: true},
		{name: "trailing values are still rejected", body: `{"StartCommand":null}{}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeUpdateAppRequest(httptest.NewRequest("PATCH", "/v1/apps/demo", strings.NewReader(tc.body)))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("decode %s succeeded, want an error", tc.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			if got.StartCommand != nil {
				t.Fatalf("StartCommand = %q, want it never set from the wire", *got.StartCommand)
			}
			tc.check(t, got.RequireAuthn)
		})
	}
}
