// Organization-aware whoami. Issue #190 / IAM-6 / ADR-061, PR 4
// + PR 5.
//
// GET /v1/orgs/me returns the caller's currently-active org plus
// the membership role. With no X-Active-Org / ?org= hint it returns the
// caller's personal organization. The endpoint exercises pkg/authz.LoadOrg
// end-to-end and is the load-bearing seam for every org-scoped
// handler that follows.
//
// PR 5 rewrote whoamiActiveOrg to render the canonical
// api.OrgMeResponse (formerly the local orgMeResponse struct),
// placing the wire shape next to the rest of the /v1/orgs/{slug}
// surface in pkg/api/orgs.go.
//
// Wire shape:
//
//	{
//	  "org": {
//	    "id": "<uuid>",
//	    "slug": "u-<12hex>",
//	    "name": "Personal",
//	    "personal": true,
//	    "plan": "free",
//	    "status": "active",
//	    "created_at": "...",
//	    "updated_at": "...",
//	    "role": "owner"
//	  }
//	}
//
// A pre-migration account without a personal organization receives
// {"org": null} for rolling-upgrade compatibility.
package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// whoamiActiveOrg is the handler mounted at GET /v1/orgs/me. It
// reads the membership stamped by s.loadOrg (issue #190 / IAM-6 /
// ADR-061, PR 4) and renders the response.
//
// Behaviour:
//   - no membership on r → resolve the caller's personal organization.
//   - membership present → fetch the org by id (the membership's
//     OrgID) so the response carries the slug + name + personal
//     flag. The role field carries the caller's role on the org.
//
// Errors:
//   - 500 CodeCapacity if OrgByID fails (a stale membership row —
//     surfaces in audit).
func (s *server) whoamiActiveOrg(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil {
		// With no explicit active-org hint, resolve the caller's personal
		// organization. Every modern account owns one, so returning null here
		// contradicted GET /v1/orgs and made org-bound key commands unusable
		// unless clients knew to synthesize an extra header.
		personal, err := s.store.OrgByPersonalAccount(r.Context(), acct.ID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				// Compatibility for pre-personal-org fixtures during rolling
				// migration. New accounts never take this branch.
				writeJSON(w, http.StatusOK, api.OrgMeResponse{Org: nil})
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not resolve personal organization"))
			return
		}
		membership, err := s.store.OrgMemberByAccount(r.Context(), personal.ID, acct.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not resolve personal organization membership"))
			return
		}
		writeJSON(w, http.StatusOK, api.OrgMeResponse{Org: &api.OrgWithRole{
			OrgResponse: api.OrgResponseFromRow(orgToRow(personal)),
			Role:        string(membership.Role),
		}})
		return
	}
	org, err := s.store.OrgByID(r.Context(), mem.OrgID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Active org unavailable",
			"the active org's row could not be loaded; refresh and try again"))
		return
	}
	resp := api.OrgMeResponse{
		Org: &api.OrgWithRole{
			OrgResponse: api.OrgResponseFromRow(orgToRow(org)),
			Role:        string(mem.Role),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}
