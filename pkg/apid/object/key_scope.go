package object

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// CapabilityHost is the optional Host capability that decides a product
// capability for an account's app. A host without it never issues key-scoped
// bindings (ADR-20261106114500).
type CapabilityHost interface {
	CapabilityEnabledFor(capability string, acct state.Account, app state.App) bool
}

// validObjectKeyScope admits a literal key prefix: a valid object key of at
// most MaxObjectKeyScopeBytes, with no pattern characters, so "segments/*"
// is refused instead of matching keys that start with "*".
func validObjectKeyScope(prefix string) bool {
	return len(prefix) <= api.MaxObjectKeyScopeBytes && objectstorage.ValidRequestKey(prefix) &&
		!strings.ContainsAny(prefix, "*?[")
}

// keyScopeProblem refuses an invalid key prefix, and any key prefix before
// the capability is released to the account: a gateway that does not enforce
// it would grant the whole bucket.
func (s *Service) keyScopeProblem(acct state.Account, app state.App, prefix string) *api.Problem {
	if !validObjectKeyScope(prefix) {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid key prefix",
			"key_prefix must be a literal object key prefix such as segments/L0/, up to 512 bytes; patterns such as * are not supported")
	}
	host, ok := s.Host.(CapabilityHost)
	if !ok || !host.CapabilityEnabledFor(api.ObjectStorageKeyScopesCapability, acct, app) {
		return api.NewProblem(http.StatusForbidden, "object_storage_key_scopes_unavailable", "Key-scoped bindings are not available",
			"key_prefix is not available to this account yet; bind the whole bucket, or ask support to enable key-scoped bindings")
	}
	return nil
}
