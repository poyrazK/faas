package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgObjectUploadRouteAllowsUnrestrictedContentTypes(t *testing.T) {
	st, ctx := pgStore(t)
	acct, err := st.CreateAccount(ctx, "upload-routes-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "upload-route-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	bucketID := uuid.NewString()
	bucket, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default",
		Region: "eu-central-1", BackendID: "gcs", BackendFingerprint: strings.Repeat("a", 64),
		PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", ""),
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "ready", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, bucket.ID, "ready", "ready"); err != nil {
		t.Fatal(err)
	}

	route := state.ObjectUploadRoute{
		ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, BucketID: bucket.ID,
		Name: "uploads", MaxBytes: 1024, Enabled: true,
	}
	created, err := st.UpsertObjectUploadRoute(ctx, route)
	if err != nil || len(created.AllowedContentTypes) != 0 {
		t.Fatalf("create unrestricted route: %+v, %v", created, err)
	}
	route.AllowedContentTypes = []string{"text/plain"}
	if _, err = st.UpsertObjectUploadRoute(ctx, route); err != nil {
		t.Fatalf("restrict route: %v", err)
	}
	route.AllowedContentTypes = nil
	updated, err := st.UpsertObjectUploadRoute(ctx, route)
	if err != nil || len(updated.AllowedContentTypes) != 0 {
		t.Fatalf("clear content type restriction: %+v, %v", updated, err)
	}
}
