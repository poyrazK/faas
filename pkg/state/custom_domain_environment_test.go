package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomDomainEnvironmentBindingOwnershipAndDeletion(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "domain-environment@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "domain-environment-project"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "domain-environment-other"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "domain-environment-app"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: otherProject.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, "wrong.example.test", app.ID, "token", 10, 10, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project binding error = %v", err)
	}
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, "*.example.test", app.ID, "token", 10, 10, environment.ID); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("wildcard binding error = %v", err)
	}
	domain, err := store.CreateCustomDomainIfUnderQuota(ctx, "staging.example.test", app.ID, "token", 10, 10, environment.ID)
	if err != nil || domain.EnvironmentID != environment.ID {
		t.Fatalf("bound domain = %+v, err=%v", domain, err)
	}
	if err := store.MarkDomainVerified(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefaultCustomDomain(ctx, app.ID, domain.Domain); !errors.Is(err, ErrNotFound) {
		t.Fatalf("environment domain became app default: %v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, environment.Slug); !errors.Is(err, ErrConflict) {
		t.Fatalf("bound environment deletion error = %v", err)
	}
	if err := store.DeleteCustomDomain(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, environment.Slug); err != nil {
		t.Fatalf("unbound environment deletion: %v", err)
	}
}
