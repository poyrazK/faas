package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// TestRender_AppDetail_DeploymentRevisionColumn pins the ADR-198 handle on the
// dashboard deploy list.
//
// The list leads with the revision because that is the string a customer
// carries into `gregale rollback --to` or an incident channel — a uuid is
// unusable in both. A row predating the column (Revision 0) must still render
// a dash rather than a misleading `v0`, which would look like a handle that
// resolves and does not.
//
// adr: 198
func TestRender_AppDetail_DeploymentRevisionColumn(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"},
			Deployments: []dashboard.DeploymentItem{
				{ID: "8f14e45fceea467a9c8e9b0e21c6d5a1", Revision: 42, Status: "live", Kind: "image", CreatedAt: "2026-09-21T10:00:00Z"},
				{ID: "1b6453892473a467d07372d45eb05abc", Status: "superseded", Kind: "image", CreatedAt: "2026-09-20T10:00:00Z"},
			},
		},
	}
	if err := dashboard.Render(rec, log, "rev-nonce", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "<th>Revision</th>") {
		t.Errorf("deploy list has no Revision column header")
	}
	if !strings.Contains(body, "v42") {
		t.Errorf("deploy list does not render the v42 handle\n%s", body)
	}
	// The revision links to the same detail page the rest of the row does, so
	// the handle is a navigation affordance and not just decoration.
	if !strings.Contains(body, `/dashboard/apps/demo/deployments/8f14e45fceea467a9c8e9b0e21c6d5a1"><code>v42</code>`) {
		t.Errorf("v42 is not linked to its deployment detail page\n%s", body)
	}
	if strings.Contains(body, "v0") {
		t.Errorf("a revision-less deployment rendered as v0\n%s", body)
	}
}
