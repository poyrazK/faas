// adr: 590
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A full clone refuses a source whose bucket rule names a destination the
// stage will not hold, and captures each bucket's notification rules
// (ADR-20261104120000 amendment 2026-11-16).
func TestMemCloneCapturesBucketNotifications(t *testing.T) {
	cloneBucketNotificationCapture(t, state.NewMemStore())
}

func cloneBucketNotificationCapture(t *testing.T, s cloneBucketReservationTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, app, op := cloneBindingFixture(t, s)
	source := cloneBindingBucket(t, s, account, app, "assets", "production", true)
	notifications := s.(state.ObjectNotificationStore)

	// An app of another project is not a captured workload: the stage will
	// not hold it.
	other, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	stray, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: other.ID, WorkloadName: "stray", Slug: "stray-" + app.ID[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifications.SetObjectBucketNotifications(ctx, account.ID, app.ID, source.ID,
		[]api.ObjectNotificationRule{cloneNotificationFunctionRule(source, stray)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable) {
		t.Fatalf("captured a rule whose destination the stage will not hold: %v", err)
	}

	rule := cloneNotificationFunctionRule(source, app)
	if _, err := notifications.SetObjectBucketNotifications(ctx, account.ID, app.ID, source.ID, []api.ObjectNotificationRule{rule}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	views, err := s.ProjectEnvironmentCloneBindings(ctx, account.ID, project.ID, op.ID)
	if err != nil || len(views) != 1 || len(views[0].Buckets) != 1 {
		t.Fatalf("bindings = %+v, %v", views, err)
	}
	captured := views[0].Buckets[0].Notifications
	if captured == nil || captured.Revision != 2 || len(captured.Rules) != 1 || captured.Rules[0].Destination != rule.Destination || len(captured.ManagedRuleIDs) != 0 {
		t.Fatalf("captured notifications = %+v", captured)
	}
}

// cloneNotificationFunctionRule names the app by its canonical ID: the memory
// store's IDs are compact, and a destination ARN carries a UUID.
func cloneNotificationFunctionRule(bucket state.ObjectBucket, app state.App) api.ObjectNotificationRule {
	app.ID = uuid.MustParse(app.ID).String()
	return notificationRuleForTest(bucket, app, "function")
}
