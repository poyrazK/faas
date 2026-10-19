package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectAppProjectID = "3f2b8c1e-6a4d-4e5f-9b7a-1c2d3e4f5a6b"

// projectIDCases pins the read-only project_id projection: present for an
// app that belongs to a project (ADR-050), omitted from the wire for a
// standalone app so existing consumers see no new key.
var projectIDCases = []struct {
	name      string
	slug      string
	projectID string
}{
	{name: "project app", slug: "project-member-app", projectID: projectAppProjectID},
	{name: "standalone app", slug: "standalone-app", projectID: ""},
}

// wireProjectID reports the raw project_id key from a JSON object so the
// test distinguishes "omitted" from "present but empty".
func wireProjectID(t *testing.T, raw []byte) (string, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	value, ok := fields["project_id"]
	if !ok {
		return "", false
	}
	var projectID string
	if err := json.Unmarshal(value, &projectID); err != nil {
		t.Fatalf("decode project_id: %v", err)
	}
	return projectID, true
}

func assertWireProjectID(t *testing.T, raw []byte, want string) {
	t.Helper()
	got, present := wireProjectID(t, raw)
	if want == "" {
		if present {
			t.Fatalf("project_id = %q, want key omitted for a standalone app: %s", got, raw)
		}
		return
	}
	if !present || got != want {
		t.Fatalf("project_id = %q (present=%v), want %q: %s", got, present, want, raw)
	}
}

func TestAppResponseProjectID(t *testing.T) {
	for _, tc := range projectIDCases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&apidserver.Server{}).AppResponse(state.App{Slug: tc.slug, ProjectID: tc.projectID}, api.PlanHobby)
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal app response: %v", err)
			}
			assertWireProjectID(t, raw, tc.projectID)
		})
	}
}

func TestListOrgAppsReportsProjectID(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "project-id-org", "Project ID Org", api.PlanPro)
	// CreateApp rejects a project_id that is not a project owned by the
	// app's account and org, so the project must exist first.
	project := state.Project{ID: projectAppProjectID, AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: "project-id-test"}
	if _, err := (*e.Store()).CreateProject(ctx, project); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, tc := range projectIDCases {
		app := state.App{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: tc.slug, Type: state.AppTypeApp, ProjectID: tc.projectID}
		if _, err := (*e.Store()).CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", tc.slug, err)
		}
	}

	rec := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/apps", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list org apps: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Apps []json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode app inventory: %v", err)
	}
	bySlug := make(map[string]json.RawMessage, len(response.Apps))
	for _, raw := range response.Apps {
		var summary api.OrgAppSummary
		if err := json.Unmarshal(raw, &summary); err != nil {
			t.Fatalf("decode app summary: %v", err)
		}
		bySlug[summary.Slug] = raw
	}
	for _, tc := range projectIDCases {
		t.Run(tc.name, func(t *testing.T) {
			raw, ok := bySlug[tc.slug]
			if !ok {
				t.Fatalf("app %q missing from inventory: %s", tc.slug, rec.Body.String())
			}
			assertWireProjectID(t, raw, tc.projectID)
		})
	}
}
