package apidserver_test

// adr: 1141 — the outbound relay needs cluster workload identity.
// adr: 20261110120000 — pull leases also take API keys, so they do not.

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Production advertised outbound-integrations while vmmd had no
// workload-identity key (2026-11-09), so the first relay call failed; it is
// available only with the cluster verifier configured. Pull leases accept API
// keys with queues:consume too, so they stay available without it.
func TestCapabilitiesRequireWorkloadIdentityForOutboundOnly(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "capabilities", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		verifier *workloadidentity.Verifier
		want     bool
	}{
		{name: "identity configured", verifier: verifier, want: true},
		{name: "identity missing", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := apidserver.CapabilitiesWithWorkloadVerifier(tc.verifier)
			var response api.CapabilitiesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("status %d: %v", recorder.Code, err)
			}
			for key, want := range map[string]bool{"queue-pull-leases": true, "outbound-integrations": tc.want} {
				found := false
				for _, capability := range response.Capabilities {
					if capability.Key != key {
						continue
					}
					found = true
					if capability.Enabled != want {
						t.Fatalf("%s enabled = %t, want %t (%+v)", key, capability.Enabled, want, capability)
					}
					if !want && capability.UnavailableReason != api.CapabilityUnavailableRuntime {
						t.Fatalf("%s reason = %q, want runtime", key, capability.UnavailableReason)
					}
				}
				if !found {
					t.Fatalf("capability %q missing", key)
				}
			}
		})
	}
}
