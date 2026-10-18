package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productPilotFixture struct {
	doctor     api.ProductDoctorReport
	catalog    api.ProductCatalog
	operations map[string]api.ProductOperationExplanation
	reviews    map[string]api.ProductBillingReview
	help       map[string]api.ProductHelpRequest
	reads      int
}

func (f *productPilotFixture) GetProductDoctor(context.Context, string, string) (api.ProductDoctorReport, error) {
	f.reads++
	return f.doctor, nil
}
func (f *productPilotFixture) ExplainProductOperation(_ context.Context, _ string, id string, _ int) (api.ProductOperationExplanation, error) {
	f.reads++
	return f.operations[id], nil
}
func (f *productPilotFixture) ReviewProductBilling(_ context.Context, _ string, tenant, _ string) (api.ProductBillingReview, error) {
	f.reads++
	return f.reviews[tenant], nil
}
func (f *productPilotFixture) GetProductCatalog(context.Context, string, string) (api.ProductCatalog, error) {
	f.reads++
	return f.catalog, nil
}

func (f *productPilotFixture) GetProductOwnerHelp(_ context.Context, _ string, id string) (api.ProductHelpRequest, error) {
	f.reads++
	return f.help[id], nil
}

func newProductPilotFixture(t *testing.T) (*productPilotFixture, api.ProductPilotManifest, productCommand) {
	t.Helper()
	f := &productPilotFixture{operations: map[string]api.ProductOperationExplanation{}, reviews: map[string]api.ProductBillingReview{}}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}}}}
	var err error
	f.catalog, err = productcontrol.Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	app := uuid.NewString()
	now := time.Now().UTC()
	f.doctor = api.ProductDoctorReport{AppID: app, ObservedAt: now, ObservationScope: "responding_api_node", ConfigurationState: "unverified", QualificationState: "unverified", Checks: []api.ProductDoctorCheck{{Check: "customer_context", Status: "unverified", Impact: "submission"}}}
	m := api.ProductPilotManifest{Version: 1, App: "exports", AppID: app, Feature: "export"}
	for i, name := range []string{"alice", "bob", "carol"} {
		plan := "starter"
		if i == 1 {
			plan = "pro"
		}
		grant := spec.Plans[plan].Features["export"]
		tenant, id := uuid.NewString(), uuid.NewString()
		amount := grant.UnitPriceMillicents
		m.Cases = append(m.Cases, api.ProductPilotCase{Name: name + "-success", Baseline: true, TenantID: tenant, Plan: plan, Revision: f.catalog.Revision, OperationID: id, ExpectedState: api.OperationSucceeded, ExpectedChargeMillicents: &amount})
		evidence := api.ProductReceiptEvidence{OperationID: id, TenantID: tenant, ResolvedState: api.OperationSucceeded, SucceededAt: &now, ResolvedAt: &now, Receipt: api.ProductOperationReceipt{Revision: f.catalog.Revision, Feature: "export", Plan: plan, Currency: "EUR", Period: productcontrol.Period(now), UnitPriceMillicents: amount, AdmittedAt: now, Admission: &api.ProductAdmissionEvidence{ConfirmedQuote: strings.Repeat("a", 64), DefinitionID: uuid.NewString(), DefinitionRevision: strings.Repeat("b", 64), DeploymentID: uuid.NewString(), Name: "export", Scope: api.DefaultEnvScope, MonthlyStarts: grant.MonthlyStarts}}}
		f.operations[id] = api.ProductOperationExplanation{AppID: app, Evidence: evidence, EntitlementDecision: "allowed_at_admission", Charge: api.ProductExplainedCharge{State: "settled", AmountMillicents: amount, Provisional: true}}
		f.reviews[tenant] = api.ProductBillingReview{TenantID: tenant, Period: evidence.Receipt.Period, Currency: "EUR", Starts: 1, AmountMillicents: amount, Lines: []api.ProductBillingLine{{Feature: "export", Revision: f.catalog.Revision, Plan: plan, UnitPriceMillicents: amount, Starts: 1, Succeeded: 1, AmountMillicents: amount}}}
	}
	return f, m, productCommand{verb: "pilot verify", app: m.App, file: filepath.Join(t.TempDir(), "pilot.json")}
}

func saveProductPilot(t *testing.T, path string, m api.ProductPilotManifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProductPilotReadOnlyEvidence(t *testing.T) {
	f, m, c := newProductPilotFixture(t)
	output := []byte("id,name\n1,private-cell-value\n")
	sum := sha256.Sum256(output)
	m.Cases[0].OutputFile = "alice.csv"
	m.Cases[0].OutputSHA256 = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(filepath.Dir(c.file), "alice.csv"), output, 0600); err != nil {
		t.Fatal(err)
	}
	saveProductPilot(t, c.file, m)
	c.output = filepath.Join(filepath.Dir(c.file), "report.json")
	report, err := verifyProductPilot(t.Context(), f, c)
	if err != nil || report.LedgerState != "observed" || report.QualificationState != "unverified" || len(report.Operations) != 3 || len(report.Outputs) != 1 || productReportExit(report) != 0 {
		t.Fatalf("evidence: %+v %v", report, err)
	}
	raw, err := os.ReadFile(c.output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-cell-value") {
		t.Fatal("report contains CSV contents")
	}
	info, err := os.Stat(c.output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report file is not private")
	}
	if _, err := verifyProductPilot(t.Context(), f, c); err == nil {
		t.Fatal("overwrote evidence report")
	}
	c.output = ""
	again, err := verifyProductPilot(t.Context(), f, c)
	if err != nil || again.ManifestSHA256 != report.ManifestSHA256 || again.LedgerState != report.LedgerState {
		t.Fatalf("repeat changed evidence identity: %+v %v", again, err)
	}
	for _, check := range report.Checks {
		if check.Impact == "qualification" && check.Status != "unverified" {
			t.Fatal("ledger reads qualified a live scenario")
		}
	}
}

func TestProductPilotMismatchAndScopeGuards(t *testing.T) {
	for _, name := range []string{"wrong tenant", "wrong app", "wrong price", "missing confirmation", "billing outcome", "native claim"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := newProductPilotFixture(t)
			first := m.Cases[0]
			view := f.operations[first.OperationID]
			switch name {
			case "wrong tenant":
				view.Evidence.TenantID = uuid.NewString()
			case "wrong app":
				view.AppID = uuid.NewString()
			case "wrong price":
				view.Evidence.Receipt.UnitPriceMillicents++
			case "missing confirmation":
				view.Evidence.Receipt.Admission.ConfirmedQuote = ""
			case "billing outcome":
				review := f.reviews[first.TenantID]
				review.Lines[0].Succeeded = 0
				f.reviews[first.TenantID] = review
			case "native claim":
				f.doctor.QualificationState = "qualified"
			}
			f.operations[first.OperationID] = view
			saveProductPilot(t, c.file, m)
			report, err := verifyProductPilot(t.Context(), f, c)
			if name == "wrong tenant" || name == "wrong app" || name == "native claim" {
				if err == nil {
					t.Fatal("invalid response trusted")
				}
			} else if err != nil || report.LedgerState != "blocked" || productReportExit(report) != 1 {
				t.Fatalf("mismatch not reported: %+v %v", report, err)
			}
		})
	}
	for _, name := range []string{"duplicate customers", "duplicate operations", "unconfirmed amount", "unsafe output path"} {
		t.Run(name, func(t *testing.T) {
			f, m, c := newProductPilotFixture(t)
			switch name {
			case "duplicate customers":
				m.Cases[1].TenantID = m.Cases[0].TenantID
			case "duplicate operations":
				m.Cases[1].OperationID = m.Cases[0].OperationID
			case "unconfirmed amount":
				m.Cases[0].ExpectedChargeMillicents = nil
			case "unsafe output path":
				m.Cases[0].OutputFile = "../secret.csv"
				m.Cases[0].OutputSHA256 = strings.Repeat("a", 64)
			}
			saveProductPilot(t, c.file, m)
			if _, err := verifyProductPilot(t.Context(), f, c); err == nil || f.reads != 0 {
				t.Fatal("invalid manifest reached the API")
			}
		})
	}
}

func TestProductPilotBillingMustCoverAllReferencedRuns(t *testing.T) {
	f, m, c := newProductPilotFixture(t)
	extra := m.Cases[0]
	extra.Name = "alice-second-success"
	extra.Baseline = false
	extra.OperationID = uuid.NewString()
	m.Cases = append(m.Cases, extra)
	view := f.operations[m.Cases[0].OperationID]
	view.Evidence.OperationID = extra.OperationID
	f.operations[extra.OperationID] = view
	saveProductPilot(t, c.file, m)
	r, err := verifyProductPilot(t.Context(), f, c)
	if err != nil || r.LedgerState != "blocked" {
		t.Fatalf("one aggregate success covered two receipts: %+v %v", r, err)
	}
	review := f.reviews[extra.TenantID]
	review.Starts = 2
	review.AmountMillicents *= 2
	review.Lines[0].Starts = 2
	review.Lines[0].Succeeded = 2
	review.Lines[0].AmountMillicents *= 2
	f.reviews[extra.TenantID] = review
	r, err = verifyProductPilot(t.Context(), f, c)
	if err != nil || r.LedgerState != "observed" {
		t.Fatalf("complete coverage rejected: %+v %v", r, err)
	}
}

func TestProductPilotOutputCannotEscapeManifestDirectory(t *testing.T) {
	f, m, c := newProductPilotFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(c.file), "linked.csv")); err != nil {
		t.Fatal(err)
	}
	m.Cases[0].OutputFile = "linked.csv"
	m.Cases[0].OutputSHA256 = strings.Repeat("a", 64)
	saveProductPilot(t, c.file, m)
	if _, err := verifyProductPilot(t.Context(), f, c); err == nil {
		t.Fatal("output symlink escaped manifest directory")
	}
}

func TestProductDoctorAndPilotCommands(t *testing.T) {
	for _, args := range [][]string{{"doctor", "--app", "exports"}, {"doctor", "--app", "exports", "--tenant", uuid.NewString()}, {"pilot", "init", "--app", "exports"}, {"pilot", "verify", "--app", "exports", "--file", "pilot.json"}} {
		if _, err := parseProductCommand(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
	}
	if _, err := parseProductCommand([]string{"doctor", "--app", "exports", "--tenant", "other"}); err == nil {
		t.Fatal("invalid tenant accepted")
	}
	c := productCommand{app: "exports", feature: "export", file: filepath.Join(t.TempDir(), "pilot.json")}
	if _, err := initProductPilot(c); err != nil {
		t.Fatal(err)
	}
	if _, err := initProductPilot(c); err == nil {
		t.Fatal("pilot init overwrote a file")
	}
	f, _, _ := newProductPilotFixture(t)
	if _, err := verifyProductPilot(t.Context(), f, c); err == nil || f.reads != 0 {
		t.Fatal("unfilled template reached API")
	}
	f.doctor.ConfigurationState = "observed"
	if err := validateProductDoctorReport(f.doctor, ""); err == nil {
		t.Fatal("unverified prerequisite reported observed")
	}
}
