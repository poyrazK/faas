package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
)

// adr: 20261016151947 — with the fleet PKI's live-query leaves, realtimed
// verifies every hub as livequeryd.faas, whatever host live_hub_url names,
// and presents its live client leaf. A server name without the CA that
// verifies it is refused.
func TestLiveTLSConfig(t *testing.T) {
	root := t.TempDir()
	if _, _, err := pki.EnsureCA(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(root, root, pki.FeatureLiveQueries, "compute-only"); err != nil {
		t.Fatal(err)
	}
	caFile, _ := pki.CARoot(root)
	certFile, keyFile := pki.LeafPaths(root, pki.FeatureRoles(pki.FeatureLiveQueries, "compute-only")[0])
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	conf, err := liveTLSConfig(env(map[string]string{
		"FAAS_REALTIME_LIVE_TLS_CA_FILE": caFile, "FAAS_REALTIME_LIVE_TLS_CERT_FILE": certFile,
		"FAAS_REALTIME_LIVE_TLS_KEY_FILE": keyFile, "FAAS_REALTIME_LIVE_TLS_SERVER_NAME": pki.LivequerydServerName,
	}))
	if err != nil || conf == nil || conf.ServerName != pki.LivequerydServerName || conf.RootCAs == nil || len(conf.Certificates) != 1 {
		t.Fatalf("fleet live TLS = %+v, %v", conf, err)
	}
	if conf, err := liveTLSConfig(env(nil)); conf != nil || err != nil {
		t.Fatalf("no live TLS settings = %+v, %v; want the defaults", conf, err)
	}
	for name, vars := range map[string]map[string]string{
		"server name without a CA": {"FAAS_REALTIME_LIVE_TLS_SERVER_NAME": pki.LivequerydServerName},
		"server name with only a client certificate": {
			"FAAS_REALTIME_LIVE_TLS_CERT_FILE": certFile, "FAAS_REALTIME_LIVE_TLS_KEY_FILE": keyFile,
			"FAAS_REALTIME_LIVE_TLS_SERVER_NAME": pki.LivequerydServerName,
		},
		"certificate without its key": {"FAAS_REALTIME_LIVE_TLS_CA_FILE": caFile, "FAAS_REALTIME_LIVE_TLS_CERT_FILE": certFile},
	} {
		if _, err := liveTLSConfig(env(vars)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
