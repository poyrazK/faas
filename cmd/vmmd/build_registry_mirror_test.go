package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// adr: 20261110210000 — slice 3j-3: vmmd serves builders a registry mirror
// only when told to, through secretd with a complete mTLS configuration.
func TestBuildRegistryMirrorFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" = off (nil mirror, no error)
	}{
		{name: "unset is off"},
		{name: "off", env: map[string]string{"FAAS_VMMD_BUILD_REGISTRY_MIRROR": "off"}},
		{name: "unknown", env: map[string]string{"FAAS_VMMD_BUILD_REGISTRY_MIRROR": "local"}, want: "want off or secretd"},
		{name: "secretd without target", env: map[string]string{"FAAS_VMMD_BUILD_REGISTRY_MIRROR": "secretd"}, want: "FAAS_VMMD_BUILD_REGISTRY_MIRROR=secretd needs FAAS_SECRETD_TARGET"},
		{name: "secretd without TLS", env: map[string]string{"FAAS_VMMD_BUILD_REGISTRY_MIRROR": "secretd", "FAAS_SECRETD_TARGET": "tcp://10.0.0.1:8098"}, want: "TLS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mirror, closeFn, err := buildRegistryMirrorFromEnv(context.Background(), func(k string) string { return tc.env[k] }, log)
			if tc.want == "" {
				if err != nil || mirror != nil {
					t.Fatalf("= %v, %v; want off", mirror != nil, err)
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
