package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemReleaseInventoryTiedCursorIncludesExpiredGraphs(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "release-history@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "release-history"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Hour)
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		store.projectReleaseSets[id] = ProjectReleaseSet{ID: id, AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", CreatedAt: at, ExpiresAt: &at}
	}
	page, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", time.Time{}, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != "00000000-0000-0000-0000-000000000003" || page[1].ID != "00000000-0000-0000-0000-000000000002" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	next, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", page[1].CreatedAt, page[1].ID, 2)
	if err != nil || len(next) != 1 || next[0].ID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("next page = %+v, %v", next, err)
	}
	expired, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", page[0].ID)
	if err != nil || expired.ExpiresAt == nil || !expired.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expired read = %+v, %v", expired, err)
	}
}
