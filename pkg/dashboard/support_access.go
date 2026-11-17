package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// SupportAccessData is the Support access page (ADR-20261116163508): for
// each organization the signed-in person owns or administers, the grants
// Gregale support holds and the requests waiting for a decision.
type SupportAccessData struct {
	Orgs      []SupportAccessOrg
	CSRFToken string
	Flash     string
	Error     string
}

// SupportAccessOrg is one organization on the page.
type SupportAccessOrg struct {
	Slug     string
	Name     string
	Personal bool
	Enabled  bool
	Apps     []string
	Access   api.SupportAccess
}
