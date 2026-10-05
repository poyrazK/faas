package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplicationStandardWorkerRepairsOnboarding(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "worker-standards@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "worker-company", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApproveApplicationStandardReview(ctx, plan.OrgID, owner.Account.ID, plan.ID, plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	s := &server{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// The first pass completes genuine no-target admission work. New creation
	// then leaves a durable pending enrollment for a restarted apid to discover.
	s.runApplicationStandardPass(ctx, store, "boot-worker")
	app, err := store.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "worker-new-service", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	restarted := &server{store: store, log: s.log}
	restarted.runApplicationStandardPass(ctx, store, "restarted-worker")
	actual, err := store.AppByID(ctx, app.ID)
	if err != nil || actual.SecurityPolicy != api.AppSecurityPolicyWarn {
		t.Fatalf("background pass missed durable enrollment: %+v %v", actual, err)
	}
	e, err := store.GetApplicationStandardEnrollment(ctx, plan.OrgID, app.ID)
	if err != nil || e.State != "persisted" || e.PersistedRevision != 1 || e.ObservedRevision != 0 {
		t.Fatalf("background pass fabricated observation: %+v %v", e, err)
	}
	if e.ErrorCode != "logging_consumer_unavailable" {
		t.Fatal("restarted worker did not reconcile automatic observation", e.ErrorCode)
	}
}
