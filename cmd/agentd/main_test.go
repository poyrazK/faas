// adr: 20261027090010, 20261027090014
package main

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	cfg, target, tlsCfg, err := configFromEnv(env(nil))
	if err != nil || target != "unix:///run/faas/schedd.sock" || tlsCfg != nil || cfg.Owner == "" {
		t.Fatalf("defaults: %+v %q %v %v", cfg, target, tlsCfg, err)
	}
	cfg, _, _, err = configFromEnv(env(map[string]string{"FAAS_AGENTD_CONCURRENCY": "16", "FAAS_AGENTD_LEASE_TTL": "45s"}))
	if err != nil || cfg.Concurrency != 16 || cfg.LeaseTTL != 45*time.Second {
		t.Fatalf("set: %+v %v", cfg, err)
	}
	for name, values := range map[string]map[string]string{
		"bad concurrency": {"FAAS_AGENTD_CONCURRENCY": "0"},
		"short lease":     {"FAAS_AGENTD_LEASE_TTL": "1s"},
		"missing key":     {"FAAS_AGENTD_SCHEDD_TLS_CERT_PATH": "/nope/cert.pem"},
	} {
		if _, _, _, err := configFromEnv(env(values)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
