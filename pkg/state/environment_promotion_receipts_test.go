// adr: 20261105153007
package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestEnvironmentPromotionReceiptMem(t *testing.T) {
	environmentPromotionReceiptSuite(t, state.NewMemStore())
}
func TestEnvironmentPromotionReceiptPG(t *testing.T) {
	s, _ := pgStore(t)
	environmentPromotionReceiptSuite(t, s)
}
func environmentPromotionReceiptSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, project, from, target, legacy := environmentReceiptFixture(t, s)
	receipts := s.(state.EnvironmentPromotionReceiptStore)
	p := legacy
	p.ID = ""
	p.IdempotencyKey = "modern-original"
	p.Status = "running"
	attempt := strings.Repeat("a", 32)
	hash := api.EnvironmentPromotionRequestHash(project.ID, from.ID, target.ID, p.PromotionHash)
	original := state.ContextWithEnvironmentPromotionAttempt(ctx, attempt, hash)
	if err := receipts.CheckEnvironmentPromotionAttempt(original, legacy); !errors.Is(err, state.ErrEnvironmentPromotionAttemptConflict) {
		t.Fatal("adopted legacy", err)
	}
	if _, _, err := s.CreateProjectEnvironmentPromotion(state.ContextWithEnvironmentPromotionAttempt(ctx, attempt, strings.Repeat("f", 64)), p, nil); !errors.Is(err, state.ErrEnvironmentPromotionAttemptConflict) {
		t.Fatal("stale scope", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, _, err := s.CreateProjectEnvironmentPromotion(original, p, nil); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrEnvironmentPromotionAttemptAccepted) {
			t.Error(err)
		}
	}
	if winners != 1 {
		t.Fatal("winners", winners)
	}
	receipt, err := receipts.EnvironmentPromotionReceipt(ctx, acct.ID, project.Slug, target.Slug, attempt)
	if err != nil || receipt.ProjectID != project.ID || receipt.FromEnvironmentID != from.ID || receipt.EnvironmentID != target.ID || receipt.RequestHash != hash || receipt.PromotionID == "" || receipt.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if _, err = s.UpdateProjectEnvironmentPromotion(ctx, acct.ID, receipt.PromotionID, "failed", "later failure", nil); err != nil {
		t.Fatal(err)
	}
	p.IdempotencyKey = "new-http-key"
	if _, _, err = s.CreateProjectEnvironmentPromotion(original, p, nil); !errors.Is(err, state.ErrEnvironmentPromotionAttemptAccepted) {
		t.Fatal("terminal replay", err)
	}
	p.IdempotencyKey = "modern-original"
	if _, _, err = s.CreateProjectEnvironmentPromotion(state.ContextWithEnvironmentPromotionAttempt(ctx, strings.Repeat("b", 32), hash), p, nil); !errors.Is(err, state.ErrEnvironmentPromotionAttemptConflict) {
		t.Fatal("replacement attempt", err)
	}
	later, err := receipts.EnvironmentPromotionReceipt(ctx, acct.ID, project.Slug, target.Slug, attempt)
	if err != nil || !reflect.DeepEqual(receipt, later) {
		t.Fatal("receipt mutated", err)
	}
	if _, err = receipts.EnvironmentPromotionReceipt(ctx, uuid.NewString(), project.Slug, target.Slug, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign receipt", err)
	}
	if err = s.DeleteProjectEnvironment(ctx, acct.ID, project.ID, from.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: from.Slug}); err != nil {
		t.Fatal(err)
	}
	if _, err = receipts.EnvironmentPromotionReceipt(ctx, acct.ID, project.Slug, target.Slug, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("replacement inherited receipt", err)
	}
	if _, _, err = s.CreateProjectEnvironmentPromotion(original, p, nil); !errors.Is(err, state.ErrEnvironmentPromotionAttemptAccepted) {
		t.Fatal("replacement revived original attempt", err)
	}
}
func TestEnvironmentPromotionReceiptPGAtomicFailure(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, project, from, target, p := environmentReceiptFixture(t, s)
	p.ID = ""
	p.IdempotencyKey = "receipt-fault"
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_environment_promotion_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt storage fault'; END $$; CREATE TRIGGER reject_environment_promotion_receipt BEFORE INSERT ON project_environment_promotion_receipts FOR EACH ROW EXECUTE FUNCTION reject_environment_promotion_receipt();`); err != nil {
		t.Fatal(err)
	}
	hash := api.EnvironmentPromotionRequestHash(project.ID, from.ID, target.ID, p.PromotionHash)
	if _, _, err := s.CreateProjectEnvironmentPromotion(state.ContextWithEnvironmentPromotionAttempt(ctx, strings.Repeat("c", 32), hash), p, nil); err == nil {
		t.Fatal("receipt fault admitted promotion")
	}
	if _, _, err := s.ProjectEnvironmentPromotionByIdempotencyKey(ctx, acct.ID, project.Slug, p.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("admission leaked", err)
	}
}
