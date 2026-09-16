// adr: 157 — policy-controlled object upload routes and durable completions.
package state

import (
	"context"
	"errors"
	"testing"
)

func TestMemObjectUploadRoutesLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	route := ObjectUploadRoute{
		ID:                  "route-1",
		AccountID:           "account-1",
		AppID:               "app-1",
		Name:                "avatar",
		BucketID:            "bucket-1",
		KeyPrefix:           "uploads/",
		MaxBytes:            1024,
		AllowedContentTypes: []string{"image/png", "image/jpeg"},
		Enabled:             true,
	}

	created, err := store.UpsertObjectUploadRoute(ctx, route)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("route timestamps = created %v updated %v, want both set", created.CreatedAt, created.UpdatedAt)
	}
	created.AllowedContentTypes[0] = "text/plain"
	fetched, err := store.GetObjectUploadRoute(ctx, route.AccountID, route.AppID, route.Name)
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	if fetched.AllowedContentTypes[0] != "image/png" {
		t.Fatalf("route content types were mutated through returned slice: %v", fetched.AllowedContentTypes)
	}

	routes, err := store.ListObjectUploadRoutes(ctx, route.AccountID, route.AppID)
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != route.Name {
		t.Fatalf("listed routes = %+v, want one %q route", routes, route.Name)
	}

	duplicate := route
	duplicate.ID = "route-2"
	if _, err := store.UpsertObjectUploadRoute(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate route error = %v, want ErrConflict", err)
	}

	updated := route
	updated.MaxBytes = 4096
	updated.AllowedContentTypes = []string{"application/octet-stream"}
	updated, err = store.UpsertObjectUploadRoute(ctx, updated)
	if err != nil {
		t.Fatalf("update route: %v", err)
	}
	if updated.CreatedAt.IsZero() || updated.MaxBytes != 4096 || updated.AllowedContentTypes[0] != "application/octet-stream" {
		t.Fatalf("updated route = %+v", updated)
	}

	if _, err := store.GetObjectUploadRoute(ctx, "other-account", route.AppID, route.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account get error = %v, want ErrNotFound", err)
	}
	if err := store.DeleteObjectUploadRoute(ctx, "other-account", route.AppID, route.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account delete error = %v, want ErrNotFound", err)
	}

	if _, err := store.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid completion error = %v, want ErrConflict", err)
	}
	completion, err := store.RecordObjectUploadCompletion(ctx, ObjectUploadCompletion{
		ID: "completion-1", RouteID: route.ID, AccountID: route.AccountID, AppID: route.AppID,
		BucketID: route.BucketID, Key: "uploads/avatar.png", Bytes: 0, Status: "committed",
	})
	if err != nil {
		t.Fatalf("record completion: %v", err)
	}
	if completion.CreatedAt.IsZero() || completion.Bytes != 0 {
		t.Fatalf("completion = %+v, want timestamped zero-byte receipt", completion)
	}
	intent, err := store.CreateObjectUploadIntent(ctx, ObjectUploadCompletion{
		ID: "intent-1", RouteID: route.ID, AccountID: route.AccountID, AppID: route.AppID,
		BucketID: route.BucketID, SubjectID: "key-1", Key: "uploads/idempotent", Bytes: 3,
		ContentType: "image/png", Status: "pending", IdempotencyKey: "request-1", RequestFingerprint: "fingerprint-1",
	})
	if err != nil || intent.Status != "pending" {
		t.Fatalf("create upload intent = %+v, err=%v", intent, err)
	}
	if _, err := store.CreateObjectUploadIntent(ctx, ObjectUploadCompletion{
		ID: "intent-2", RouteID: route.ID, AccountID: route.AccountID, AppID: route.AppID,
		BucketID: route.BucketID, SubjectID: "key-1", Key: "uploads/other", Bytes: 3,
		Status: "pending", IdempotencyKey: "request-1", RequestFingerprint: "fingerprint-1",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate upload intent error = %v, want ErrConflict", err)
	}
	fetchedIntent, err := store.GetObjectUploadIntent(ctx, route.ID, "key-1", "request-1")
	if err != nil || fetchedIntent.ID != intent.ID {
		t.Fatalf("get upload intent = %+v, err=%v", fetchedIntent, err)
	}
	fetchedIntent.Status, fetchedIntent.ETag = "completed", "etag-1"
	updatedIntent, err := store.UpdateObjectUploadCompletion(ctx, fetchedIntent)
	if err != nil || updatedIntent.Status != "completed" || updatedIntent.ETag != "etag-1" {
		t.Fatalf("update upload completion = %+v, err=%v", updatedIntent, err)
	}

	if err := store.DeleteObjectUploadRoute(ctx, route.AccountID, route.AppID, route.Name); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if _, err := store.GetObjectUploadRoute(ctx, route.AccountID, route.AppID, route.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted route error = %v, want ErrNotFound", err)
	}
	routes, err = store.ListObjectUploadRoutes(ctx, route.AccountID, route.AppID)
	if err != nil {
		t.Fatalf("list deleted routes: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes after delete = %+v, want empty", routes)
	}
}
