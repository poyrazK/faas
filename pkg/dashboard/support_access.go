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
	// Open is the requests waiting for a decision, with their approval
	// form filled in from what support asked for.
	Open []SupportAccessRequestForm
	// Durations is the grant form's duration choices.
	Durations []SupportAccessDuration
}

// SupportAccessRequestForm is one open request and its approval form.
type SupportAccessRequestForm struct {
	Request api.SupportAccessRequest
	// Asked describes what support asked for, e.g. "web, api for 4 hours".
	Asked string
	// AppsValue fills the form's apps field.
	AppsValue string
	Durations []SupportAccessDuration
	// Focus marks the request a mail linked to.
	Focus bool
}

// SupportAccessDuration is one choice of the duration select.
type SupportAccessDuration struct {
	Hours    int
	Label    string
	Selected bool
}
