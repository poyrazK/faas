package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// mcpContractHost is what pkg/apid/mcpcontract needs (ADR-20261127191645):
// the consumer sign-up host plus who made the request.
type mcpContractHost struct{ consumerSignupHost }

func (h mcpContractHost) Actor(r *http.Request, acct state.Account) string {
	return configActor(r, acct)
}
