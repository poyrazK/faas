// adr: 590
package state_test

import (
	"context"
	"errors"
	"testing"

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

	stray, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "stray-" + app.ID[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifications.SetObjectBucketNotifications(ctx, account.ID, app.ID, source.ID,
		[]api.ObjectNotificationRule{notificationRuleForTest(source, stray, "function")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneObjectNotificationDestinationUnavailable) {
		t.Fatalf("captured a rule whose destination the stage will not hold: %v", err)
	}

	rule := notificationRuleForTest(source, app, "function")
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
