package e2etest

import "testing"

func TestGatewayPublicEnvPrivateSchedulerTargets(t *testing.T) {
	const socket = "/tmp/faas-private-harness/schedd.sock"
	env := gatewaydPublicEnv("postgres:///test", "127.0.0.1:8080", "127.0.0.1:8081", "/tmp/internal.sock", socket, []string{"FAAS_UDPD_ENABLED=1", "FAAS_UDPD_ALLOWED_SOURCE_CIDRS=127.0.0.0/8"})
	for _, name := range []string{"FAAS_TCPD_SCHEDD_TARGET", "FAAS_UDPD_SCHEDD_TARGET"} {
		if got, ok := envValue(t, env, name); !ok || got != "unix://"+socket {
			t.Fatalf("%s=%q present=%v", name, got, ok)
		}
	}
	for name, want := range map[string]string{"FAAS_PUBLIC_LISTEN_ADDR": "127.0.0.1:8080", "FAAS_PUBLIC_CONTROL_ADDR": "127.0.0.1:8081", "FAAS_INTERNAL_SOCKET": "/tmp/internal.sock", "FAAS_UDPD_ENABLED": "1", "FAAS_UDPD_ALLOWED_SOURCE_CIDRS": "127.0.0.0/8"} {
		if got, ok := envValue(t, env, name); !ok || got != want {
			t.Fatalf("%s=%q present=%v want=%q", name, got, ok, want)
		}
	}
}
