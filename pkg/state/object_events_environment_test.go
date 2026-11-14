package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stage-owned bucket's storage events carry the stage, so only the stage's
// subscriptions receive them; every other bucket's events stay production.
//
// adr: 20261104120000
func TestObjectEventsCarryBucketEnvironmentMem(t *testing.T) {
	objectEventsCarryBucketEnvironment(t, state.NewMemStore())
}

func TestObjectEventsCarryBucketEnvironmentPG(t *testing.T) {
	st, _ := pgStore(t)
	objectEventsCarryBucketEnvironment(t, st)
}

func objectEventsCarryBucketEnvironment(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	full := st.(state.Store)
	upload := func(key string) string {
		t.Helper()
		writes := st.(state.ObjectTrackedGatewayCopyStore)
		c := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID,
			SubjectID: "subject", RequestID: "request", Key: key, Bytes: 1, Status: "pending"}
		c, err := writes.BeginTrackedGatewayUpload(ctx, c, accountingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if c, err = writes.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID); err != nil {
			t.Fatal(err)
		}
		c.Status, c.ETag = "completed", `"done"`
		if _, err = writes.FinishTrackedObjectUpload(ctx, c); err != nil {
			t.Fatal(err)
		}
		envelope, _, _ := takeObjectEvent(t, st, b.AccountID, "write:"+c.ID, api.ObjectEventCreated)
		return envelope.EnvironmentID
	}
	if got := upload("before.txt"); got != "" {
		t.Fatalf("a production bucket's event carries environment %q", got)
	}

	project, err := full.CreateProject(ctx, state.Project{AccountID: b.AccountID, Slug: "objenv-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := full.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: b.AccountID, ProjectID: project.ID, Slug: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.(state.ProjectEnvironmentBranchBucketStore).RecordProjectEnvironmentBranchBucket(ctx, state.ProjectEnvironmentBranchBucket{AccountID: b.AccountID,
		ProjectID: project.ID, EnvironmentID: branch.ID, EnvironmentSlug: branch.Slug, AppID: b.AppID, SourceBucketID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.(state.ProjectEnvironmentBranchBucketStore).SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := upload("after.txt"); got != branch.ID {
		t.Fatalf("a branch copy bucket's event carries environment %q, want %s", got, branch.ID)
	}
}
