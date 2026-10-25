// adr: 20261025090000
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentPromotionReceiptMem(t *testing.T) { promotionReceiptSuite(t, state.NewMemStore()) }
func TestDeploymentPromotionReceiptPG(t *testing.T)  { s, _ := pgStore(t); promotionReceiptSuite(t, s) }
func promotionReceiptSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	_, app, base, candidate := bindingPromotionFixture(t, s)
	receipts := s.(state.DeploymentPromotionReceiptStore)
	attempt := strings.Repeat("a", 32)
	request := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &base.ID}
	write := state.ContextWithDeploymentPromotion(ctx, attempt, request)
	stale := "00000000-0000-0000-0000-000000000000"
	if _, err := s.UpdateDeploymentTraffic(write, candidate.ID, 100, stale); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("mismatched context=%v", err)
	}
	wrong := request
	wrong.ExpectedServingDeploymentID = &stale
	if _, err := s.UpdateDeploymentTraffic(state.ContextWithDeploymentPromotion(ctx, attempt, wrong), candidate.ID, 100, stale); !errors.Is(err, state.ErrTrafficServingChanged) {
		t.Fatalf("stale guard=%v", err)
	}
	if _, err := receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused guard retained receipt: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Go(func() { _, err := s.UpdateDeploymentTraffic(write, candidate.ID, 100, base.ID); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrDeploymentPromotionAttemptAccepted) {
			t.Errorf("concurrent promotion=%v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("accepted %d traffic writes", winners)
	}
	got, err := receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt)
	hash, _ := api.DeploymentPromotionRequestHash(candidate.ID, request)
	if err != nil || got.AttemptID != attempt || got.RequestHash != hash || got.DeploymentID != candidate.ID || got.ExpectedServingDeploymentID != base.ID || got.Scope != "default" || got.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v %v", got, err)
	}
	// Later traffic changes cannot erase acceptance or permit an old attempt to reapply it.
	if _, err = s.UpdateDeploymentTraffic(ctx, base.ID, 100, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateDeploymentTraffic(write, candidate.ID, 100, base.ID); !errors.Is(err, state.ErrDeploymentPromotionAttemptAccepted) {
		t.Fatalf("replayed old traffic intent: %v", err)
	}
	if _, err = s.UpdateDeploymentTraffic(state.ContextWithDeploymentPromotion(ctx, attempt, api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &candidate.ID}), base.ID, 100, candidate.ID); !errors.Is(err, state.ErrDeploymentPromotionAttemptConflict) {
		t.Fatalf("reused attempt: %v", err)
	}
	later, err := receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt)
	if err != nil || !reflect.DeepEqual(got, later) {
		t.Fatal("historical receipt changed")
	}
	d, _ := s.DeploymentByID(ctx, base.ID)
	if d.TrafficPercent != 100 {
		t.Fatal("receipt read/retry changed current traffic")
	}
	if _, err = receipts.DeploymentPromotionReceipt(ctx, candidate.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-app receipt exposed")
	}
}

func TestDeploymentPromotionReceiptPGFailureRollsBack(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, app, base, candidate := bindingPromotionFixture(t, s)
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_promotion_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt storage fault'; END $$;
 CREATE TRIGGER reject_promotion_receipt BEFORE INSERT ON deployment_promotion_receipts FOR EACH ROW EXECUTE FUNCTION reject_promotion_receipt();`)
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("b", 32)
	request := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &base.ID}
	if _, err = s.UpdateDeploymentTraffic(state.ContextWithDeploymentPromotion(ctx, attempt, request), candidate.ID, 100, base.ID); err == nil {
		t.Fatal("failed receipt allowed traffic commit")
	}
	for id, percent := range map[string]int{base.ID: 100, candidate.ID: 0} {
		d, err := s.DeploymentByID(ctx, id)
		if err != nil || d.TrafficPercent != percent {
			t.Fatalf("rollback %s=%+v %v", id, d, err)
		}
	}
	if _, err = s.DeploymentPromotionReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed write retained a receipt")
	}
}

func TestDeploymentPromotionNamedReceiptMem(t *testing.T) {
	promotionNamedReceiptSuite(t, state.NewMemStore())
}
func TestDeploymentPromotionNamedReceiptPG(t *testing.T) {
	s, _ := pgStore(t)
	promotionNamedReceiptSuite(t, s)
}
func promotionNamedReceiptSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "named-promotion@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "promotion-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "named-promotion", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.(state.ReleaseReviewStore).ReadReleaseReview(ctx, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:staged", Kind: state.DeploymentKindImage, Scope: "staging", TrafficPercent: 0, TrafficPercentExplicit: true, ExpectedReview: &review, ReviewedSettings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	empty := ""
	attempt := strings.Repeat("d", 32)
	req := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &empty, ExpectedReview: candidate.StagedReview}
	traffic := s.(state.ReviewedTrafficStore)
	receipts := s.(state.DeploymentPromotionReceiptStore)
	stale := *candidate.StagedReview
	stale.ConfigurationHash = strings.Repeat("f", 64)
	bad := req
	bad.ExpectedReview = &stale
	if _, err = traffic.UpdateDeploymentTrafficReviewed(state.ContextWithDeploymentPromotion(ctx, attempt, bad), candidate.ID, 100, "", stale); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("changed review=%v", err)
	}
	if _, err = receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("stale named promotion retained receipt")
	}
	if _, err = traffic.UpdateDeploymentTrafficReviewed(state.ContextWithDeploymentPromotion(ctx, attempt, req), candidate.ID, 100, "", *candidate.StagedReview); err != nil {
		t.Fatal(err)
	}
	original, err := receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt)
	if err != nil || original.EnvironmentID != env.ID || original.ProjectID != project.ID || original.Scope != "staging" || original.ExpectedServingDeploymentID != "" {
		t.Fatalf("named receipt=%+v %v", original, err)
	}
	if _, err = s.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	later, err := receipts.DeploymentPromotionReceipt(ctx, app.ID, attempt)
	if err != nil || !reflect.DeepEqual(original, later) {
		t.Fatal("mutable protection change erased historical acceptance")
	}
}
