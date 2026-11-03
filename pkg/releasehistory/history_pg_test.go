//go:build !no_pg

package releasehistory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/releasehistory"
)

func openStore(t *testing.T) *releasehistory.Store {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return releasehistory.NewStore(pool)
}

func activation(host, kind, release, previous string, started time.Time) releasehistory.Activation {
	return releasehistory.Activation{
		Host: host, Kind: kind, ReleaseID: release, CommitSHA: strings.Repeat("a", 40),
		PreviousReleaseID: previous, Actor: "cd-controlplane run 1", StartedAt: started,
	}
}

func TestStoreRecordsActivations(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	t0 := time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC)

	deploy := activation("cp-1", releasehistory.KindDeploy, "rel-b", "rel-a", t0)
	deploy.Services = []string{"apid", "schedd"}
	id, err := store.Start(ctx, deploy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, id, deploy, releasehistory.StatusRolledBack, "restart apid: exit 1", t0.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Finishing twice is refused: the row is no longer in progress.
	if err := store.Finish(ctx, id, deploy, releasehistory.StatusSucceeded, "", t0.Add(time.Minute)); !errors.Is(err, releasehistory.ErrNotInProgress) {
		t.Fatalf("second Finish = %v, want ErrNotInProgress", err)
	}

	// A start that could not be recorded is inserted whole at the end.
	rollback := activation("cp-1", releasehistory.KindRollback, "rel-a", "rel-b", t0.Add(2*time.Minute))
	rollback.CommitSHA = ""
	if err := store.Finish(ctx, "", rollback, releasehistory.StatusSucceeded, "", t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Another host's activation, then one whose deployctl died.
	other := activation("cp-2", releasehistory.KindDeploy, "rel-b", "", t0.Add(4*time.Minute))
	if _, err := store.Start(ctx, other); err != nil {
		t.Fatal(err)
	}
	// The next activation on cp-2 marks the abandoned one interrupted.
	next := activation("cp-2", releasehistory.KindDeploy, "rel-c", "rel-b", t0.Add(5*time.Minute))
	nextID, err := store.Start(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, nextID, next, releasehistory.StatusFailed, strings.Repeat("é", 1500), t0.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}

	all, err := store.List(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ host, kind, release, previous, status string }
	got := make([]row, 0, len(all))
	for _, r := range all {
		got = append(got, row{r.Host, r.Kind, r.ReleaseID, r.PreviousReleaseID, r.Status})
	}
	want := []row{
		{"cp-2", "deploy", "rel-c", "rel-b", "failed"},
		{"cp-2", "deploy", "rel-b", "", "interrupted"},
		{"cp-1", "rollback", "rel-a", "rel-b", "succeeded"},
		{"cp-1", "deploy", "rel-b", "rel-a", "rolled_back"},
	}
	if len(got) != len(want) {
		t.Fatalf("List = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if d := all[0].Detail; len(d) > releasehistory.MaxDetail || !strings.HasPrefix(d, "é") || strings.ToValidUTF8(d, "") != d {
		t.Fatalf("detail kept %d bytes (valid UTF-8 %t), want at most %d", len(d), strings.ToValidUTF8(d, "") == d, releasehistory.MaxDetail)
	}
	if all[1].FinishedAt == nil || !all[1].FinishedAt.Equal(next.StartedAt) {
		t.Fatalf("interrupted row finished at %v, want the next start %v", all[1].FinishedAt, next.StartedAt)
	}
	if all[2].CommitSHA != "" {
		t.Fatalf("rollback commit = %q, want none recorded", all[2].CommitSHA)
	}
	if s := all[3].Services; len(s) != 2 || s[0] != "apid" || s[1] != "schedd" {
		t.Fatalf("services = %v, want [apid schedd]", s)
	}

	cp1, err := store.List(ctx, "cp-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp1) != 1 || cp1[0].Kind != releasehistory.KindRollback {
		t.Fatalf("List(cp-1, 1) = %+v, want the rollback", cp1)
	}
}

func TestStoreRefusesNonFinalStatus(t *testing.T) {
	store := openStore(t)
	a := activation("cp-1", releasehistory.KindDeploy, "rel-a", "", time.Now().UTC())
	if err := store.Finish(context.Background(), "", a, releasehistory.StatusInProgress, "", time.Now().UTC()); err == nil {
		t.Fatal("Finish accepted in_progress as an outcome")
	}
}
