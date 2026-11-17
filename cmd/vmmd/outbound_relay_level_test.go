package main

import (
	"log/slog"
	"testing"
)

// Prod 2026-11-16: every compute vmmd start logged ERROR "outbound relay
// disabled" because the relay leaf was never issued. Missing leaf files are
// the relay being off (warn); a malformed target is still a fault (error).
func TestOutboundRelayDisabledLevel(t *testing.T) {
	dir := t.TempDir()
	missing := map[string]string{
		"FAAS_VMMD_OUTBOUND_RELAY_TARGET":         "tcp://outboundd.faas:8096",
		"FAAS_VMMD_OUTBOUND_CLIENT_TLS_CERT_PATH": dir + "/outboundd-client.crt",
		"FAAS_VMMD_OUTBOUND_CLIENT_TLS_KEY_PATH":  dir + "/outboundd-client.key",
		"FAAS_VMMD_OUTBOUND_CLIENT_TLS_CA_PATH":   dir + "/ca.crt",
	}
	badScheme := map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "http://outboundd.faas:8096"}
	tests := []struct {
		name string
		env  map[string]string
		want slog.Level
	}{
		{"leaf never issued", missing, slog.LevelWarn},
		{"malformed target", badScheme, slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := outboundRelayTarget(func(k string) string { return tt.env[k] })
			if err == nil {
				t.Fatal("outboundRelayTarget succeeded, want an error")
			}
			if got := outboundRelayDisabledLevel(err); got != tt.want {
				t.Fatalf("level = %v, want %v (err %v)", got, tt.want, err)
			}
		})
	}
}
