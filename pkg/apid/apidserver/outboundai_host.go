package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/outboundai"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// outboundAIHost adapts *server to outboundai.Host (ADR-982). It reads
// server fields at call time, so tests that swap s.store behave exactly as
// before the lift.
type outboundAIHost struct{ s *server }

var _ outboundai.Host = outboundAIHost{}

func (h outboundAIHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h outboundAIHost) Store() state.Store { return h.s.store }

func (h outboundAIHost) SealCredential(w http.ResponseWriter, authorization string) ([]byte, bool) {
	return sealOutboundCredential(w, authorization)
}

// sealOutboundCredential seals a provider credential for outboundd only.
func sealOutboundCredential(w http.ResponseWriter, authorization string) ([]byte, bool) {
	unavailable := func() ([]byte, bool) {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
		return nil, false
	}
	if outboundCredentialRecipient == nil {
		return unavailable()
	}
	recipient := outboundCredentialRecipient()
	if recipient == nil {
		return unavailable()
	}
	sealed, err := secretbox.SealBytes(recipient, outbound.ManagedAuthorizationSealNamespace, []byte(authorization), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		return unavailable()
	}
	return sealed, true
}
