package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// adr: 20261110210000 — slice 3j-2: imaged pulls through secretd only when
// told to, with a control-plane target and a complete mTLS configuration.
func TestRegistryProxyFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" = local (nil proxy, no error)
	}{
		{name: "unset is local"},
		{name: "local", env: map[string]string{"FAAS_IMAGED_REGISTRY_PULLS": "local"}},
		{name: "unknown", env: map[string]string{"FAAS_IMAGED_REGISTRY_PULLS": "proxy"}, want: "want local or secretd"},
		{name: "without target", env: map[string]string{"FAAS_IMAGED_REGISTRY_PULLS": "secretd"}, want: "needs FAAS_SECRETD_TARGET"},
		{name: "unix target", env: map[string]string{"FAAS_IMAGED_REGISTRY_PULLS": "secretd", "FAAS_SECRETD_TARGET": "unix:///run/faas/secretd.sock"}, want: "want a tcp://"},
		{name: "without TLS", env: map[string]string{"FAAS_IMAGED_REGISTRY_PULLS": "secretd", "FAAS_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, closeFn, err := registryProxyFromEnv(context.Background(), func(k string) string { return tc.env[k] }, log)
			if tc.want == "" {
				if err != nil || proxy != nil {
					t.Fatalf("= %v, %v; want local", proxy, err)
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
