package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's rows stay under its creator's account, so a bucket
// notification rule could route the company bucket's events to the
// creator's personal app, and kept doing so after the creator left the
// company: the rule's recipients were added after the company reach filter.
// They now meet the same reach as subscriptions, and a rule cannot target an
// app the bucket's events may not reach.
//
// adr: 20261025211616
func TestObjectNotificationsKeepCompanyBoundaryMem(t *testing.T) {
	objectNotificationsKeepCompanyBoundary(t, state.NewMemStore())
}

func TestObjectNotificationsKeepCompanyBoundaryPG(t *testing.T) {
	st, _ := pgStore(t)
	objectNotificationsKeepCompanyBoundary(t, st)
}

func objectNotificationsKeepCompanyBoundary(t *testing.T, st accountingStore) {
	ctx := t.Context()
	owner, err := st.CreateAccount(ctx, "notify-owner-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := st.CreateAccount(ctx, "notify-creator-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, state.Org{Slug: "notify-" + uuid.NewString()[:8], Name: "Notify", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = st.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, &owner.ID); err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, orgID string) state.App {
		app, err := st.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: orgID, Slug: slug + "-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	company, personal := newApp("company", org.ID), newApp("personal", "")
	notifications := st.(state.ObjectNotificationStore)
	b := readyCompanyBucket(t, st, creator.ID, company.ID)

	rule := func(id, appID string) api.ObjectNotificationRule {
		return api.ObjectNotificationRule{ID: id, Destination: "arn:gregale:lambda:" + b.Region + ":" + uuid.MustParse(creator.ID).String() + ":function:" + appID, Events: []string{"s3:ObjectCreated:Put"}}
	}
	upload := func(key string) []state.PublishedEventRecipient {
		t.Helper()
		c, err := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: creator.ID, AppID: company.ID, BucketID: b.ID, SubjectID: "creator", Key: key, Status: "pending", Bytes: 1}, accountingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if c, err = st.DispatchTrackedObjectUpload(ctx, creator.ID, b.ID, c.ID); err != nil {
			t.Fatal(err)
		}
		c.Status, c.ETag = "completed", `"done"`
		if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
			t.Fatal(err)
		}
		_, _, work := takeObjectEvent(t, st, creator.ID, "write:"+c.ID, api.ObjectEventCreated) //nolint:contextcheck // the shared helper claims with t.Context(), the same test context
		return work.RecipientSnapshot
	}
	notified := func(recipients []state.PublishedEventRecipient, appID string) bool {
		for _, r := range recipients {
			if r.ObjectNotification != nil && r.AppID == appID {
				return true
			}
		}
		return false
	}

	// While the creator may deploy to the company, its events reach their
	// personal apps, as a subscription's do.
	if _, err = notifications.SetObjectBucketNotifications(ctx, creator.ID, company.ID, b.ID, []api.ObjectNotificationRule{rule("personal", personal.ID)}); err != nil {
		t.Fatal("member's rule refused", err)
	}
	if !notified(upload("member.jpg"), personal.ID) {
		t.Fatal("a member's personal app missed the company bucket's event")
	}

	if err = st.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if notified(upload("departed.jpg"), personal.ID) {
		t.Fatal("a departed creator's personal app still received the company bucket's event")
	}
	if _, err = notifications.SetObjectBucketNotifications(ctx, creator.ID, company.ID, b.ID, []api.ObjectNotificationRule{rule("again", personal.ID)}); !errors.Is(err, state.ErrObjectNotificationInvalid) {
		t.Fatal("rule targeting an app outside the company was accepted", err)
	}
	if _, err = notifications.SetObjectBucketNotifications(ctx, creator.ID, company.ID, b.ID, []api.ObjectNotificationRule{rule("company", company.ID)}); err != nil {
		t.Fatal("rule targeting the company's own app refused", err)
	}
	if !notified(upload("company.jpg"), company.ID) {
		t.Fatal("the company's own app missed its bucket's event")
	}
}

// readyCompanyBucket is seedAccounting for a bucket on the given app.
func readyCompanyBucket(t *testing.T, st accountingStore, accountID, appID string) state.ObjectBucket {
	t.Helper()
	ctx := t.Context()
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: accountID, AppID: appID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, accountID, appID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err = st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: accountID, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, Source: "qualified-provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)}); err != nil {
		t.Fatal(err)
	}
	return b
}
