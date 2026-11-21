package referrals_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/referrals"
	"strings"
	"sync"
	"testing"
)

func qualificationInput() referrals.QualificationInput {
	return referrals.QualificationInput{LeadVersion: 1, UseCase: "Customer API migration", Workload: "Node services, 100 requests per second", DecisionMaker: "Engineering director", BuyingTimeline: "30_days", Priority: "high", ProductFit: "strong", Blocker: "Needs a technical walkthrough"}
}
func TestSalesQualificationVersionsOwnershipAndCoverage(t *testing.T) {
	_, s, owner, other := pipelineFixture(t)
	ctx := t.Context()
	mine := referrals.Scope{OwnerID: owner}
	manager := referrals.Scope{OwnerID: owner, All: true}
	theirs := referrals.Scope{OwnerID: other}
	lead, err := s.SaveProspect(ctx, mine, owner, "", prospect(owner, "qualified@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.QualificationProfile(ctx, mine, lead)
	if err != nil || p.Qualification.Version != 0 || p.Qualification.BuyingTimeline != "unknown" || p.Qualification.UpdatedAt != nil {
		t.Fatal(p, err)
	}
	in := qualificationInput()
	if err = s.SaveQualification(ctx, theirs, lead, in); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("another rep qualified lead", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.SaveQualification(ctx, mine, lead, in) }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, referrals.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	rows, counts, _, err := s.QualificationOverview(ctx, manager, referrals.QualificationFilter{Limit: 25})
	if err != nil || len(rows) != 0 || counts.Open != 1 || counts.Complete != 1 {
		t.Fatal(rows, counts, err)
	}
	p, err = s.QualificationProfile(ctx, mine, lead)
	if err != nil || p.Qualification.Version != 1 || p.Qualification.UpdatedBy != owner {
		t.Fatal(p, err)
	}
	update := p.Qualification.QualificationInput
	update.DecisionMaker = ""
	if err = s.SaveQualification(ctx, mine, lead, update); err != nil {
		t.Fatal(err)
	}
	rows, counts, _, err = s.QualificationOverview(ctx, manager, referrals.QualificationFilter{Limit: 25})
	if err != nil || len(rows) != 1 || rows[0].Captured != 3 || len(rows[0].Missing) != 1 || rows[0].Missing[0] != "decision_maker" || counts.MissingDecisionMaker != 1 {
		t.Fatal(rows, counts, err)
	}
	p.Lead.OwnerID = other
	if _, err = s.SaveProspect(ctx, manager, owner, lead, p.Lead.ProspectInput); err != nil {
		t.Fatal(err)
	}
	if _, err = s.QualificationProfile(ctx, mine, lead); !errors.Is(err, referrals.ErrNotFound) {
		t.Fatal("former owner can read qualification", err)
	}
	if err = s.SaveQualification(ctx, theirs, lead, update); !errors.Is(err, referrals.ErrConflict) {
		t.Fatal("stale parent version accepted", err)
	}
	p, err = s.QualificationProfile(ctx, theirs, lead)
	if err != nil || p.Qualification.UpdatedBy != owner || p.Qualification.UseCase != in.UseCase {
		t.Fatal(p, err)
	}
	update = p.Qualification.QualificationInput
	update.DecisionMaker = "CTO"
	if err = s.SaveQualification(ctx, theirs, lead, update); err != nil {
		t.Fatal(err)
	}
}
func TestSalesClosingReasonsAtomicCorrectionsReopeningAndLegacy(t *testing.T) {
	pool, s, owner, other := pipelineFixture(t)
	ctx := t.Context()
	scope := referrals.Scope{OwnerID: owner, All: true}
	in := prospect(owner, "outcome@example.com")
	in.Stage = "lost"
	if _, err := s.SaveProspect(ctx, scope, owner, "", in); !errors.Is(err, referrals.ErrOutcomeRequired) {
		t.Fatal("new closed lead needs reason", err)
	}
	in.Stage = "qualified"
	lead, err := s.SaveProspect(ctx, scope, owner, "", in)
	if err != nil {
		t.Fatal(err)
	}
	in.Version = 1
	in.Stage = "lost"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); !errors.Is(err, referrals.ErrOutcomeRequired) {
		t.Fatal("closing transition needs reason", err)
	}
	in.CloseReason = "budget"
	in.CloseNote = strings.Repeat("x", 1001)
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err == nil {
		t.Fatal("oversize reason did not fail")
	}
	p, err := s.QualificationProfile(ctx, scope, lead)
	if err != nil || p.Lead.Stage != "qualified" || p.Lead.Version != 1 || p.Outcome != nil {
		t.Fatal("reason failure partially committed stage", p, err)
	}
	in.CloseNote = "Budget is not approved this quarter"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err != nil {
		t.Fatal(err)
	}
	p, err = s.QualificationProfile(ctx, scope, lead)
	if err != nil || p.Outcome == nil || p.Outcome.Reason != "budget" || p.Outcome.OwnerID != owner {
		t.Fatal(p, err)
	}
	// Metadata edits keep the reason; a correction adds a new immutable record.
	in.Version = 2
	in.CloseReason = ""
	in.CloseNote = ""
	in.Company = "New company name"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err != nil {
		t.Fatal(err)
	}
	in.Version = 3
	in.CloseReason = "timing"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err != nil {
		t.Fatal(err)
	}
	_, c, reasons, err := s.QualificationOverview(ctx, scope, referrals.QualificationFilter{Limit: 25})
	if err != nil || len(reasons) != 1 || reasons[0].Reason != "timing" || reasons[0].Count != 1 || c.UnclassifiedLost != 0 {
		t.Fatal(c, reasons, err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM sales_outcomes WHERE lead_id=$1", lead).Scan(&count); err != nil || count != 2 {
		t.Fatal("outcome correction erased history", count, err)
	}
	in.Version = 4
	in.Stage = "demo"
	in.CloseReason = ""
	in.CloseNote = ""
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err != nil {
		t.Fatal(err)
	}
	_, _, reasons, err = s.QualificationOverview(ctx, scope, referrals.QualificationFilter{Limit: 25})
	if err != nil || len(reasons) != 0 {
		t.Fatal("reopened lead counted as closed", reasons, err)
	}
	in.Version = 5
	in.Stage = "lost"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); !errors.Is(err, referrals.ErrOutcomeRequired) {
		t.Fatal("old closing reason reused", err)
	}
	in.Stage = "won"
	in.CloseReason = "other"
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); !errors.Is(err, referrals.ErrInvalidOutcome) {
		t.Fatal("other lacked explanation", err)
	}
	in.CloseNote = "Successful platform pilot"
	in.OwnerID = other
	if _, err = s.SaveProspect(ctx, scope, owner, lead, in); err != nil {
		t.Fatal(err)
	}
	p, err = s.QualificationProfile(ctx, scope, lead)
	if err != nil || p.Outcome.Stage != "won" || p.Outcome.LeadVersion != 6 || p.Outcome.OwnerID != other || p.Outcome.ActorID != owner {
		t.Fatal(p, err)
	}
	// Historical closed rows remain unclassified until explicitly annotated.
	if _, err = pool.Exec(ctx, "INSERT INTO sales_leads(id,owner_id,name,email,stage) VALUES(gen_random_uuid(),$1,'Legacy deal','legacy@example.com','lost')", owner); err != nil {
		t.Fatal(err)
	}
	_, c, reasons, err = s.QualificationOverview(ctx, scope, referrals.QualificationFilter{Owner: owner, Limit: 25})
	if err != nil || c.UnclassifiedLost != 1 || len(reasons) != 0 {
		t.Fatal(c, reasons, err)
	}
	var credit int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM referral_attributions").Scan(&credit); err != nil || credit != 0 {
		t.Fatal("sales outcome created referral credit", credit, err)
	}
}
