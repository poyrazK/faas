// adr: 20261102123523 — the catalog records a database's previews setting and
// a preview environment's database by its owner, reserves one per owner
// outside the plan's database limit, and counts and lists them.
package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStorePreviewDatabases(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "preview-source", at)
	environmentID := uuid.NewString()
	previewOf := func(prNumber int) Database {
		owner := PreviewOwner{SourceDatabaseID: source.ID, EnvironmentID: environmentID, PRNumber: prNumber}
		input := postgresTestDatabase(accountID, PreviewDatabaseName(source.Name, owner), at.Add(time.Minute))
		input.Spec.RestoreWindowSeconds = 0
		input.Preview = owner
		return input
	}

	if _, _, err := store.ReservePreview(ctx, previewOf(7), 10); !errors.Is(err, ErrPreviewsOff) {
		t.Fatalf("ReservePreview with previews off = %v, want ErrPreviewsOff", err)
	}
	updated, err := store.SetPreviews(ctx, accountID, source.ID, PreviewsEmpty, at)
	if err != nil || updated.Previews != PreviewsEmpty {
		t.Fatalf("SetPreviews = %+v, %v", updated, err)
	}

	first, created, err := store.ReservePreview(ctx, previewOf(7), 2)
	if err != nil || !created || first.Preview != previewOf(7).Preview || first.Previews != "" || first.State != StateProvisioning {
		t.Fatalf("ReservePreview = %+v, created %v, %v", first, created, err)
	}
	again, created, err := store.ReservePreview(ctx, previewOf(7), 2)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second ReservePreview = %+v, created %v, %v; want the first", again, created, err)
	}
	if found, err := store.FindPreviewDatabase(ctx, first.Preview); err != nil || found.ID != first.ID {
		t.Fatalf("FindPreviewDatabase = %+v, %v", found, err)
	}
	if _, _, err := store.ReservePreview(ctx, previewOf(8), 2); err != nil {
		t.Fatalf("second pull request = %v", err)
	}
	if _, _, err := store.ReservePreview(ctx, previewOf(9), 2); !errors.Is(err, ErrPreviewDatabaseLimit) {
		t.Fatalf("past the preview limit = %v, want ErrPreviewDatabaseLimit", err)
	}

	// Two preview databases beside the source: the plan's limit counts the
	// source alone.
	if count, err := store.CountSubjectDatabases(ctx, accountID); err != nil || count != 1 {
		t.Fatalf("CountSubjectDatabases = %d, %v, want 1", count, err)
	}
	if _, created, err := store.Reserve(ctx, postgresTestDatabase(accountID, "preview-second", at), 2); err != nil || !created {
		t.Fatalf("Reserve inside the limit beside preview databases = %v", err)
	}
	if _, _, err := store.Reserve(ctx, postgresTestDatabase(accountID, first.Name, at), 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("Reserve adopting a preview database's name = %v, want ErrConflict", err)
	}
	taken := previewOf(10)
	taken.Name = "preview-second"
	if _, _, err := store.ReservePreview(ctx, taken, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReservePreview with a customer database's name = %v, want ErrConflict", err)
	}

	if _, err := store.SetPreviews(ctx, accountID, first.ID, PreviewsEmpty, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetPreviews on a preview database = %v, want ErrConflict", err)
	}
	listed, err := store.ListPreviewDatabases(ctx, accountID, environmentID, 7)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("ListPreviewDatabases = %+v, %v", listed, err)
	}
	off, err := store.SetPreviews(ctx, accountID, source.ID, PreviewsOff, at)
	if err != nil || off.Previews != "" {
		t.Fatalf("SetPreviews(off) = %+v, %v", off, err)
	}
	if _, _, err := store.ReservePreview(ctx, previewOf(11), 10); !errors.Is(err, ErrPreviewsOff) {
		t.Fatalf("ReservePreview after previews off = %v, want ErrPreviewsOff", err)
	}
	if again, created, err := store.ReservePreview(ctx, previewOf(7), 10); err != nil || created || again.ID != first.ID {
		t.Fatalf("an existing preview database outlives previews off: %+v, created %v, %v", again, created, err)
	}
}
