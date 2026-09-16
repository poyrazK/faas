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
