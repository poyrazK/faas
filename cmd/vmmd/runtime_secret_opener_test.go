package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// adr: 20261110210000 — vmmd asks secretd only when told to, and only with
// binding required and a complete mTLS configuration.
func TestRuntimeSecretOpenerFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name    string
		env     map[string]string
		binding bool
		want    string // "" = local (nil opener, no error)
	}{
		{name: "unset is local", binding: false},
		{name: "local", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "local"}},
		{name: "unknown", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "remote"}, want: "want local or secretd"},
		{name: "secretd without binding", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "secretd", "FAAS_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, want: "binding required"},
		{name: "secretd without target", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "secretd"}, binding: true, want: "needs FAAS_SECRETD_TARGET"},
		{name: "secretd on a unix socket", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "secretd", "FAAS_SECRETD_TARGET": "unix:///run/faas/secretd.sock"}, binding: true, want: "want a tcp://"},
		{name: "secretd without TLS", env: map[string]string{"FAAS_RUNTIME_SECRET_OPENER": "secretd", "FAAS_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, binding: true, want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opener, closeFn, err := runtimeSecretOpenerFromEnv(context.Background(), func(k string) string { return tc.env[k] }, tc.binding, log)
			if tc.want == "" {
				if err != nil || opener != nil {
					t.Fatalf("= %v, %v; want local", opener, err)
				}
				closeFn()
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
