// adr: 20261105102920
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func environmentReceiptFixture(t *testing.T, s state.Store) (state.Account, state.Project, state.ProjectEnvironment, state.ProjectEnvironment, state.ProjectEnvironmentPromotion) {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@environment-rollback.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "receipt-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	from, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	promotion, _, err := s.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: target.Slug, PromotionHash: strings.Repeat("a", 64), IdempotencyKey: uuid.NewString(), Status: "succeeded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return acct, project, from, target, promotion
}
func TestEnvironmentRollbackReceiptMem(t *testing.T) { environmentReceiptSuite(t, state.NewMemStore()) }
func TestEnvironmentRollbackReceiptPG(t *testing.T) {
	s, _ := pgStore(t)
	environmentReceiptSuite(t, s)
}
func environmentReceiptSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, project, from, target, promotion := environmentReceiptFixture(t, s)
	receipts := s.(state.EnvironmentRollbackReceiptStore)
	hash := api.EnvironmentRollbackRequestHash(project.ID, from.ID, target.ID, promotion.ID, promotion.PromotionHash)
	attempt := strings.Repeat("a", 32)
	original := state.ContextWithEnvironmentRollbackAttempt(ctx, attempt, hash)
	if _, err := s.StartProjectEnvironmentPromotionRollback(state.ContextWithEnvironmentRollbackAttempt(ctx, attempt, strings.Repeat("f", 64)), acct.ID, promotion.ID, "original"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptConflict) {
		t.Fatalf("stale scope admitted: %v", err)
	}
	if _, err := receipts.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, promotion.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("rejected attempt left receipt", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := s.StartProjectEnvironmentPromotionRollback(original, acct.ID, promotion.ID, "original")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrEnvironmentRollbackAttemptAccepted) {
			t.Error(err)
		}
	}
	if winners != 1 {
		t.Fatalf("accepted %d original attempts", winners)
	}
	receipt, err := receipts.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, promotion.ID, attempt)
	if err != nil || receipt.AttemptID != attempt || receipt.RequestHash != hash || receipt.ProjectID != project.ID || receipt.FromEnvironmentID != from.ID || receipt.EnvironmentID != target.ID || receipt.PromotionID != promotion.ID || receipt.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if _, err = s.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rollback_failed", "later failure", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartProjectEnvironmentPromotionRollback(original, acct.ID, promotion.ID, "new-http-key"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptAccepted) {
		t.Fatal("terminal attempt replayed", err)
	}
	changed := state.ContextWithEnvironmentRollbackAttempt(ctx, strings.Repeat("b", 32), hash)
	if _, err = s.StartProjectEnvironmentPromotionRollback(changed, acct.ID, promotion.ID, "different"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptConflict) {
		t.Fatal("replacement attempt admitted", err)
	}
	got, _, err := s.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, target.Slug, promotion.ID)
	if err != nil || got.RollbackStatus != "rollback_failed" || got.RollbackError != "later failure" {
		t.Fatal("replay reset native progress", err)
	}
	later, err := receipts.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, promotion.ID, attempt)
	if err != nil || !reflect.DeepEqual(receipt, later) {
		t.Fatal("historical receipt changed", err)
	}
	// Preserve explicit legacy continuation of unfinished native checkpoints.
	// It uses the original key and leaves historical acceptance immutable.
	if _, err = s.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "wrong-key"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy continuation ignored original key", err)
	}
	if _, err = s.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "original"); err != nil {
		t.Fatal("original-key checkpoint continuation blocked", err)
	}
	continued, err := receipts.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, promotion.ID, attempt)
	if err != nil || !reflect.DeepEqual(receipt, continued) {
		t.Fatal("legacy continuation altered acceptance receipt", err)
	}
	if _, err = receipts.EnvironmentRollbackReceipt(ctx, uuid.NewString(), project.Slug, target.Slug, promotion.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign receipt exposed", err)
	}
	if err = s.DeleteProjectEnvironment(ctx, acct.ID, project.ID, from.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: from.Slug}); err != nil {
		t.Fatal(err)
	}
	if _, err = receipts.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, promotion.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("replacement source inherited receipt", err)
	}
	if _, err = s.StartProjectEnvironmentPromotionRollback(original, acct.ID, promotion.ID, "original"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptAccepted) {
		t.Fatal("scope replacement revived original attempt", err)
	}
}
func TestEnvironmentRollbackReceiptLegacyMem(t *testing.T) {
	environmentReceiptLegacy(t, state.NewMemStore())
}
func TestEnvironmentRollbackReceiptLegacyPG(t *testing.T) {
	s, _ := pgStore(t)
	environmentReceiptLegacy(t, s)
}
func environmentReceiptLegacy(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, project, from, target, p := environmentReceiptFixture(t, s)
	if _, err := s.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, p.ID, "legacy"); err != nil {
		t.Fatal(err)
	}
	hash := api.EnvironmentRollbackRequestHash(project.ID, from.ID, target.ID, p.ID, p.PromotionHash)
	write := state.ContextWithEnvironmentRollbackAttempt(ctx, strings.Repeat("c", 32), hash)
	if _, err := s.StartProjectEnvironmentPromotionRollback(write, acct.ID, p.ID, "legacy"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptConflict) {
		t.Fatal("legacy acceptance adopted", err)
	}
	if _, err := s.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, p.ID, "legacy"); err != nil {
		t.Fatal("legacy retry contract changed", err)
	}
}
func TestEnvironmentRollbackReceiptPGAtomicFailure(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, project, from, target, p := environmentReceiptFixture(t, s)
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_environment_rollback_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt storage fault'; END $$;
 CREATE TRIGGER reject_environment_rollback_receipt BEFORE INSERT ON project_environment_rollback_receipts FOR EACH ROW EXECUTE FUNCTION reject_environment_rollback_receipt();`)
	if err != nil {
		t.Fatal(err)
	}
	hash := api.EnvironmentRollbackRequestHash(project.ID, from.ID, target.ID, p.ID, p.PromotionHash)
	if _, err = s.StartProjectEnvironmentPromotionRollback(state.ContextWithEnvironmentRollbackAttempt(ctx, strings.Repeat("d", 32), hash), acct.ID, p.ID, "original"); err == nil {
		t.Fatal("receipt fault admitted rollback")
	}
	got, _, err := s.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, target.Slug, p.ID)
	if err != nil || got.RollbackStatus != "" || got.RollbackStartedAt != nil || got.RollbackIdempotencyKey != "" {
		t.Fatal("receipt fault changed admission", err)
	}
}
