package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type projectDeleteInterleave struct {
	MemStore  *state.MemStore
	competing string
	called    bool
}

func (f *projectDeleteInterleave) DeleteProjectIfNoStages(ctx context.Context, accountID, projectID string) error {
	f.called = true
	if f.competing == "stage" {
		if _, err := f.MemStore.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: accountID, ProjectID: projectID, Slug: "staging"}); err != nil {
			return err
		}
	} else {
		if _, err := f.MemStore.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
			AccountID: accountID, ProjectID: projectID, SourceEnvironment: "production", TargetEnvironment: "staging",
			IdempotencyKey: "delete-interleave", SourceRevisionHash: strings.Repeat("a", 64),
		}); err != nil {
			return err
		}
	}
	return f.MemStore.DeleteProjectIfNoStages(ctx, accountID, projectID)
}

func TestProjectDeletionReturnsConflictForRacedStageOrClone(t *testing.T) {
	for _, competing := range []string{"stage", "clone"} {
		t.Run(competing, func(t *testing.T) {
			srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
			fake := &projectDeleteInterleave{MemStore: store, competing: competing}
			*srv.Store() = statetest.NewMem(fake)
			req, rec := apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop", project.Slug, nil)
			srv.DeleteProjectForTest(rec, req, acct)
			if !fake.called || rec.Code != http.StatusConflict {
				t.Fatalf("raced delete: %d %s; checked=%v", rec.Code, rec.Body, fake.called)
			}
			if _, err := store.ProjectBySlug(t.Context(), acct.ID, project.Slug); err != nil {
				t.Fatal(err)
			}
			got, err := store.AppByID(t.Context(), app.ID)
			if err != nil || got.ProjectID != project.ID {
				t.Fatalf("raced delete detached app: %+v %v", got, err)
			}
		})
	}
}
