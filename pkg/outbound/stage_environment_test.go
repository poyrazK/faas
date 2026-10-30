// adr: 1141
package outbound

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Managed integrations hold production provider keys and quotas: a stage
// instance is refused through the relay, while production and tokens that
// carry no environment keep working.
func TestRelayRefusesStageInstances(t *testing.T) {
	f := startRelayFixture(t)
	for _, tc := range []struct {
		environment string
		want        int
	}{
		{"", http.StatusOK},
		{workloadidentity.DefaultEnvironment, http.StatusOK},
		{"production", http.StatusOK},
		{"staging", http.StatusForbidden},
	} {
		token, err := f.signer.MintEnvironment(time.Now(), "account-1", "app-1", "instance-1", tc.environment, workloadidentity.OutboundRelayAudience)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := f.relayDo(t, token.AccessToken, relayPost("/i/openai/v1/chat/completions", "Authorization: Bearer placeholder\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want || tc.want == http.StatusForbidden && !strings.Contains(string(body), "outbound_stage_not_allowed") {
			t.Fatalf("environment %q: relay response = %d %s, want %d", tc.environment, resp.StatusCode, body, tc.want)
		}
	}
}

func TestWorkloadIdentityCarriesEnvironment(t *testing.T) {
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.MintEnvironment(time.Now(), "account-1", "app-1", "instance-1", "staging", identityAudiencePrefix+"integration-1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Verify(token.AccessToken, "integration-1")
	if err != nil || identity.Environment != "staging" || productionWorkloadEnvironment(identity.Environment) {
		t.Fatalf("staging identity = %+v, %v", identity, err)
	}
}
