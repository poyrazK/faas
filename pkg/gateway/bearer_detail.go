package gateway

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// missingBearerDetail explains a bearer the platform gates cannot use. An API
// consumer key (ck_…) authenticates an app's own customers (ADR-120) and
// never satisfies require_authn or public_auth=bearer, which take the owning
// account's API key; saying only "present it as Bearer <token>" left callers
// who did exactly that with no way forward (prod CLI e2e 2026-11-14).
func missingBearerDetail(tok string) string {
	if strings.HasPrefix(tok, api.ConsumerKeyPrefix) {
		return "this app's platform auth takes the owning account's API key (" + api.APIKeyPrefix + "…); API consumer keys (" +
			api.ConsumerKeyPrefix + "…) do not satisfy it. To authenticate your API's callers with consumer keys, open the app's " +
			"platform auth (gregale app <slug> --public-auth open --no-require-authn) and set --consumer-auth-mode required"
	}
	return "this app requires an API key; present it as `Authorization: Bearer <token>`"
}
