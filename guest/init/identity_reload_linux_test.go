//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261031193635 slice 5b: guest-init writes the branch identity, calls
// the hook with its path and digest, and succeeds only on a matching ack.
func TestRestoreIdentityReload(t *testing.T) {
	secrets := map[string]string{"DATABASE_URL": "postgres://branch"}
	env := map[string]string{"APP_ENV": "pr-1"}
	_, digest, err := api.RestoreIdentityDocument(env, secrets)
	if err != nil {
		t.Fatal(err)
	}
	served := runtimeConfigResponse{Env: env, Secrets: &secrets, Digest: digest}
	for _, tc := range []struct {
		name    string
		served  runtimeConfigResponse
		ack     string
		wantErr bool
	}{
		{"acknowledged", served, digest, false},
		{"hook ignores the identity", served, "", true},
		{"hook acknowledges another identity", served, "0" + digest[1:], true},
		{"vmmd refused", runtimeConfigResponse{Error: "identity_unavailable"}, digest, true},
		{"digest does not match the document", runtimeConfigResponse{Env: env, Secrets: &secrets, Digest: "0" + digest[1:]}, digest, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var written []byte
			var headers map[string]string
			reload := restoreIdentityReload{
				hook: api.AfterRestoreHook{Path: "/internal/after-restore", Identity: api.AfterRestoreIdentityReload}, port: 8080, uid: 1000,
				fetch: func() (runtimeConfigResponse, error) { return tc.served, nil },
				write: func(path string, uid int, body []byte) error {
					if path != api.RestoreIdentityFilePath || uid != 1000 {
						t.Fatalf("wrote %s for uid %d", path, uid)
					}
					written = body
					return nil
				},
				call: func(_ api.AfterRestoreHook, _ int, extra map[string]string, verify func(*http.Response) error) error {
					headers = extra
					answer := &http.Response{Header: http.Header{}}
					if tc.ack != "" {
						answer.Header.Set(api.AfterRestoreIdentityAckHeader, tc.ack)
					}
					return verify(answer)
				},
			}
			got, err := reload.run()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("run() = %q; want an error", got)
				}
				return
			}
			if err != nil || got != digest {
				t.Fatalf("run() = %q, %v", got, err)
			}
			var doc api.RestoreIdentity
			if err := json.Unmarshal(written, &doc); err != nil || doc.Secrets["DATABASE_URL"] != "postgres://branch" {
				t.Fatalf("identity file = %s, %v", written, err)
			}
			if headers[api.AfterRestoreIdentityFileHeader] != api.RestoreIdentityFilePath || headers[api.AfterRestoreIdentityDigestHeader] != digest {
				t.Fatalf("hook headers = %v", headers)
			}
		})
	}
	fetchFails := restoreIdentityReload{fetch: func() (runtimeConfigResponse, error) { return runtimeConfigResponse{}, errors.New("vsock") }}
	if _, err := fetchFails.run(); err == nil {
		t.Fatal("a failed fetch reloaded")
	}
}
