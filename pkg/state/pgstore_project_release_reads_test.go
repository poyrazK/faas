//go:build !no_pg

package state_test

import (
	"sort"
	"testing"
	"time"
)

func TestPgProjectReleaseReadContract(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, project, releases := testProjectReleaseReadContract(t, store)
	// Force timestamp ties and expired history in the same isolated test schema.
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET created_at = $1 WHERE project_id = $2`, at, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at = $1 WHERE project_id = $2 AND NOT active`, at, project.ID); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, release := range releases {
		ids = append(ids, release.ID)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	page, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", time.Time{}, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != ids[0] || page[1].ID != ids[1] {
		t.Fatalf("tied page = %+v, %v", page, err)
	}
	next, err := store.ListProjectReleaseSetsBefore(ctx, account.ID, project.ID, "production", page[1].CreatedAt, page[1].ID, 2)
	if err != nil || len(next) != 1 || next[0].ID != ids[2] {
		t.Fatalf("tied next page = %+v, %v", next, err)
	}
	expired, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", releases[0].ID)
	if err != nil || expired.Active || expired.ExpiresAt == nil || !expired.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expired inventory = %+v, %v", expired, err)
	}
}
