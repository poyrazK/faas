package datapipeline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVerificationCancelIsOwnedRunBoundAndFenced(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	if err := service.ApproveSchema(ctx, p.AccountID, p.ID, p.Revision, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	p, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	progress := &VerificationProgress{ID: strings.Repeat("d", 64), State: "running", Phase: "scan", Rows: 8000}
	if err = store.Finish(ctx, p, Observation{State: "blocked", Reason: "schema_change_verifying", SchemaChange: &plan, VerificationProgress: progress, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	p, err = store.Get(ctx, p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		account, run string
		revision     int64
	}{{uuid.NewString(), progress.ID, p.Revision}, {p.AccountID, strings.Repeat("e", 64), p.Revision}, {p.AccountID, progress.ID, p.Revision + 1}} {
		if err = service.CancelVerification(ctx, tc.account, p.ID, tc.revision, tc.run); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign/stale cancel accepted", err)
		}
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.CancelVerification(ctx, p.AccountID, p.ID, p.Revision, progress.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, stale, Observation{State: "live", VerificationProgress: &VerificationProgress{ID: progress.ID, State: "passed"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale worker overwrote cancellation", err)
	}
	saved, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || saved.VerificationProgress.State != "cancelled" || saved.SchemaApproval != plan.ID || saved.Generation != p.Generation {
		t.Fatal("cancel changed migration permission or generation", err)
	}
}
func TestVerificationIdentityBindsDefinitionGenerationAndRetry(t *testing.T) {
	_, _, p, plan := schemaPipelineFixture(t)
	p.SchemaChange = &plan
	p.SchemaApproval = plan.ID
	id, _ := verificationID(p, plan.After)
	p.Revision++
	if next, _ := verificationID(p, plan.After); next != id {
		t.Fatal("worker revision discarded approved schema progress")
	}
	p.SchemaChange.Retry++
	if next, _ := verificationID(p, plan.After); next == id {
		t.Fatal("explicit retry reused old proof")
	}
	p.SchemaChange.Retry--
	p.Generation++
	if next, _ := verificationID(p, plan.After); next == id {
		t.Fatal("generation reused proof")
	}
	b, _ := json.Marshal(VerificationProgress{ID: id, Rows: 123, State: "running"})
	if strings.Contains(string(b), "Cursor") || strings.Contains(string(b), "Lower") || strings.Contains(string(b), "Upper") {
		t.Fatal("public progress exposed customer boundaries")
	}
}
