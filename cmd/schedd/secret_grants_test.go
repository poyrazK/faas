// adr: 20261110210000
package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// A compute schedd grants through secretd only when told to, only as a node,
// and only with a complete mTLS configuration.
func TestSecretGrantRPCFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name  string
		env   map[string]string
		owner string
		want  string // "" = off (nil, no error)
	}{
		{name: "unset is off", owner: "n-1"},
		{name: "central schedd", env: map[string]string{"FAAS_SCHEDD_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, want: "has no node"},
		{name: "unix target", env: map[string]string{"FAAS_SCHEDD_SECRETD_TARGET": "unix:///run/faas/secretd.sock"}, owner: "n-1", want: "want a tcp://"},
		{name: "no TLS", env: map[string]string{"FAAS_SCHEDD_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, owner: "n-1", want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc, closeFn, err := secretGrantRPCFromEnv(context.Background(), func(k string) string { return tc.env[k] }, tc.owner, log)
			if tc.want == "" {
				if err != nil || rpc != nil {
					t.Fatalf("= %v, %v; want off", rpc, err)
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
