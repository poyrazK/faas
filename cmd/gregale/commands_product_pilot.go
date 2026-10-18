package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productPilotClient interface {
	GetProductDoctor(context.Context, string, string) (api.ProductDoctorReport, error)
	ExplainProductOperation(context.Context, string, string, int) (api.ProductOperationExplanation, error)
	ReviewProductBilling(context.Context, string, string, string) (api.ProductBillingReview, error)
	GetProductCatalog(context.Context, string, string) (api.ProductCatalog, error)
	GetProductOwnerHelp(context.Context, string, string) (api.ProductHelpRequest, error)
}

func initProductPilot(c productCommand) (any, error) {
	starter, pro := int64(25000), int64(10000)
	m := api.ProductPilotManifest{Version: 1, App: c.app, Feature: c.feature, Cases: []api.ProductPilotCase{{Name: "alice-success", Baseline: true, Plan: "starter", ExpectedState: api.OperationSucceeded, ExpectedChargeMillicents: &starter}, {Name: "bob-success", Baseline: true, Plan: "pro", ExpectedState: api.OperationSucceeded, ExpectedChargeMillicents: &pro}, {Name: "carol-success", Baseline: true, Plan: "starter", ExpectedState: api.OperationSucceeded, ExpectedChargeMillicents: &starter}}}
	if err := writeProductEvidence(c.file, m); err != nil {
		return nil, err
	}
	return map[string]string{"file": c.file, "status": "created", "next": "Fill app_id from product doctor --json, and the three real tenant, catalog revision and Operation IDs. Review expected plans, outcomes and integer charges. Version 2 adds required baseline downloads, three run-help journeys and inbox observations; see the pilot guide. Version 1 keeps optional help_cases and journeys."}, nil
}

func writeProductEvidence(path string, value any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	e := json.NewEncoder(f)
	e.SetIndent("", "  ")
	writeErr := e.Encode(value)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func readProductPilot(path string) (api.ProductPilotManifest, string, error) {
	var m api.ProductPilotManifest
	f, err := openCustomerFile(path)
	if err != nil {
		return m, "", err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.ProductControlPilotManifestMaxBytes+1))
	if err != nil {
		return m, "", err
	}
	if len(raw) > api.ProductControlPilotManifestMaxBytes {
		return m, "", errors.New("pilot manifest exceeds its size bound")
	}
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return m, "", err
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, "", err
	}
	if err = productcontrol.ValidatePilot(m); err != nil {
		return m, "", err
	}
	hash := sha256.Sum256(canonical)
	return m, hex.EncodeToString(hash[:]), nil
}

func verifyProductPilot(ctx context.Context, client productPilotClient, c productCommand) (api.ProductPilotReport, error) {
	m, hash, err := readProductPilot(c.file)
	if err != nil {
		return api.ProductPilotReport{}, err
	}
	if m.App != c.app {
		return api.ProductPilotReport{}, errors.New("manifest app does not match --app")
	}
	r := api.ProductPilotReport{Version: m.Version, App: m.App, AppID: m.AppID, Feature: m.Feature, ManifestSHA256: hash, ObservedAt: time.Now().UTC(), LedgerState: "observed", QualificationState: "unverified", Checks: []api.ProductDoctorCheck{}, Operations: []api.ProductOperationExplanation{}, Reviews: []api.ProductBillingReview{}, HelpState: "not_requested", Help: []api.ProductPilotHelpResult{}, JourneyState: "not_requested", Journeys: []api.ProductPilotJourneyResult{}}
	r.Doctor, err = client.GetProductDoctor(ctx, c.app, "")
	if err != nil {
		return r, err
	}
	if err := validateProductDoctorReport(r.Doctor, ""); err != nil {
		return r, err
	}
	wanted, _ := uuid.Parse(m.AppID)
	observed, parseErr := uuid.Parse(r.Doctor.AppID)
	if parseErr != nil || wanted != observed || r.Doctor.ObservationScope != "responding_api_node" {
		return r, errors.New("doctor response does not match the manifest app identity and observation scope")
	}
	for _, item := range m.Cases {
		if err := verifyProductPilotCase(ctx, client, c.app, m.Feature, item, &r); err != nil {
			return r, err
		}
		if item.OutputFile != "" {
			if err := verifyProductPilotOutput(filepath.Dir(c.file), item, &r); err != nil {
				return r, err
			}
		}
	}
	verifyProductPilotCoverage(&r)
	if err := verifyProductPilotHelp(ctx, client, m, &r); err != nil {
		return r, err
	}
	if err := verifyProductPilotJourneys(filepath.Dir(c.file), m, &r); err != nil {
		return r, err
	}
	for _, name := range []string{"customer_login_and_isolation", "private_download_and_output_hashes", "lost_response_and_tab_recovery", "redeployment_and_plan_change", "native_interruption_and_reconciliation", "customer_help_visibility_and_recovery", "customer_inbox_visibility_and_read_recovery", "permission_changes_and_revocation", "flag_rollback"} {
		r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: name, Status: "unverified", Impact: "qualification", Code: name + "_unverified", Message: "Retained ledger evidence does not prove that this live scenario was performed.", Remediation: "Run the documented three-customer native pilot and retain the source-qualified execution/browser evidence and actual output hashes."})
	}
	if c.output != "" {
		if err := writeProductEvidence(c.output, r); err != nil {
			return r, err
		}
	}
	return r, nil
}

func verifyProductPilotOutput(directory string, item api.ProductPilotCase, r *api.ProductPilotReport) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(item.OutputFile)
	if err != nil {
		return fmt.Errorf("read local output for %s: %w", item.Name, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > api.ProductControlPilotOutputMaxBytes {
		return errors.New("pilot output must be a bounded regular CSV file")
	}
	if r.Version == 2 && item.Baseline && info.Size() == 0 {
		return errors.New("version 2 baseline output must contain the downloaded CSV bytes")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, api.ProductControlPilotOutputMaxBytes+1))
	if err != nil {
		return err
	}
	if size > api.ProductControlPilotOutputMaxBytes || (r.Version == 2 && item.Baseline && size == 0) {
		return errors.New("pilot output exceeds its bound")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	status, code := "observed", "local_output_hash_match"
	if digest != item.OutputSHA256 {
		status, code = "blocked", "local_output_hash_mismatch"
		r.LedgerState = "blocked"
	}
	r.Outputs = append(r.Outputs, api.ProductPilotOutput{Case: item.Name, File: item.OutputFile, SHA256: digest, Bytes: size, Source: "local_file_under_manifest_directory"})
	r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: item.Name + "/local_output", Status: status, Impact: "output_evidence", Code: code, Message: "Local CSV bytes were hashed and compared; this does not prove customer download, exporter correctness or native execution."})
	return nil
}

func verifyProductPilotCase(ctx context.Context, client productPilotClient, app, feature string, item api.ProductPilotCase, r *api.ProductPilotReport) error {
	view, err := client.ExplainProductOperation(ctx, app, item.OperationID, 0)
	if err != nil {
		return fmt.Errorf("read case %s: %w", item.Name, err)
	}
	e := view.Evidence
	receipt := e.Receipt
	// Recheck scope before emitting any returned evidence into the report.
	wanted, _ := uuid.Parse(r.AppID)
	actual, parseErr := uuid.Parse(view.AppID)
	if parseErr != nil || actual != wanted || e.OperationID != item.OperationID || e.TenantID != item.TenantID {
		return fmt.Errorf("case %s returned evidence for another app/customer/operation", item.Name)
	}
	matched := view.EntitlementDecision == "allowed_at_admission" && receipt.Feature == feature && receipt.Plan == item.Plan && receipt.Revision == item.Revision && view.Charge.AmountMillicents == *item.ExpectedChargeMillicents
	if item.ExpectedState == api.OperationRequiresReconciliation {
		matched = matched && e.ResolvedState == "" && view.Outcome != nil && view.Outcome.State == api.OperationRequiresReconciliation
	} else {
		matched = matched && e.ResolvedState == item.ExpectedState && e.ResolvedAt != nil
	}
	catalog, err := client.GetProductCatalog(ctx, app, item.Revision)
	if err != nil {
		return fmt.Errorf("read case %s catalog: %w", item.Name, err)
	}
	grant, granted := catalog.Spec.Plans[item.Plan].Features[feature]
	compiled, compileErr := productcontrol.Compile(catalog.Spec)
	matched = matched && compileErr == nil && compiled.Revision == item.Revision && catalog.Revision == item.Revision && granted && receipt.Currency == catalog.Spec.Currency && receipt.UnitPriceMillicents == grant.UnitPriceMillicents
	admission := receipt.Admission
	if admission == nil {
		matched = false
	} else {
		quote, err := hex.DecodeString(admission.ConfirmedQuote)
		matched = matched && err == nil && len(quote) == 32 && admission.MonthlyStarts == grant.MonthlyStarts && admission.StartsBefore >= 0 && admission.StartsBefore < admission.MonthlyStarts && validCustomerOperationUUID(admission.DefinitionID) && validCustomerOperationUUID(admission.DeploymentID) && len(admission.DefinitionRevision) == 64
		binding := catalog.Spec.Features[feature]
		if binding.Operation != nil {
			matched = matched && admission.Name == binding.Operation.Name && admission.Scope == binding.Operation.Scope
		} else {
			matched = matched && admission.DefinitionID == binding.DefinitionID
		}
	}
	if item.ExpectedState == api.OperationSucceeded && e.WaivedAt == nil {
		matched = matched && e.SucceededAt != nil && view.Charge.State == "settled" && view.Charge.AmountMillicents == receipt.UnitPriceMillicents
	}
	if e.WaivedAt != nil {
		matched = matched && view.Charge.State == "waived" && view.Charge.AmountMillicents == 0
	}
	if item.ExpectedState == api.OperationFailed || item.ExpectedState == api.OperationCancelled {
		matched = matched && view.Charge.State == "not_billable" && view.Charge.AmountMillicents == 0
	}
	review, err := client.ReviewProductBilling(ctx, app, item.TenantID, receipt.Period)
	if err != nil {
		return fmt.Errorf("read case %s billing review: %w", item.Name, err)
	}
	if review.TenantID != item.TenantID || review.Period != receipt.Period || review.Currency != receipt.Currency {
		return fmt.Errorf("case %s billing response scope mismatch", item.Name)
	}
	found := false
	for _, line := range review.Lines {
		if line.Feature == feature && line.Plan == item.Plan && line.Revision == item.Revision && line.UnitPriceMillicents == receipt.UnitPriceMillicents {
			found = line.Starts > 0
			if e.WaivedAt != nil {
				found = found && line.Waived > 0
			} else {
				switch item.ExpectedState {
				case api.OperationSucceeded:
					found = found && line.Succeeded > 0
				case api.OperationFailed:
					found = found && line.Failed > 0
				case api.OperationCancelled:
					found = found && line.Cancelled > 0
				case api.OperationRequiresReconciliation:
					found = found && line.Unresolved > 0
				}
			}
		}
	}
	matched = matched && found
	status, code := "observed", "receipt_and_billing_match"
	if !matched {
		status, code = "blocked", "receipt_or_billing_mismatch"
		r.LedgerState = "blocked"
	}
	r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: item.Name, Feature: feature, Status: status, Impact: "ledger", Code: code, Message: "Compared scoped retained receipt, immutable plan price, verified outcome, expected charge and admission-month billing evidence."})
	r.Operations = append(r.Operations, view)
	r.Reviews = append(r.Reviews, review)
	return nil
}
