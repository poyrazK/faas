package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productCommand struct {
	verb, app, file, definition, tenant, plan, revision, period, cursor, feature, input, key string
	operation, scope, authApp, authOrg, quote                                                string
	reviewHash, reason, billingID, document, invoice, billingProvider, billingCustomer       string
	output, policyFile, helpID                                                               string
	emailStatus, deliveryID                                                                  string
	failedAttempt, overdueDays                                                               int
	externalRef, customerName, workspace, authTokenFile, onboardingID                        string
	confirm                                                                                  bool
	amountMillicents                                                                         int64
	after                                                                                    int
	suspended, self                                                                          bool
}

func parseProductCommand(args []string) (productCommand, error) {
	var c productCommand
	if len(args) == 0 {
		return c, errors.New("use gregale product <init|validate|apply|inspect|features|plans|customers|doctor|pilot|email|quote|start|explain|usage|statements|billing|access|plan-policy|team-policy>")
	}
	c.verb = args[0]
	args = args[1:]
	if c.verb == "features" || c.verb == "plans" || c.verb == "customers" || c.verb == "statements" || c.verb == "pilot" || c.verb == "access" || c.verb == "plan-policy" || c.verb == "team-policy" || c.verb == "email" {
		if len(args) == 0 {
			return c, errors.New("a subcommand is required")
		}
		c.verb += " " + args[0]
		args = args[1:]
	}
	if c.verb == "billing" {
		if len(args) < 2 || args[0] != "overdue-policy" {
			return c, errors.New("use gregale product billing overdue-policy <inspect|set|clear>")
		}
		c.verb += " overdue-policy " + args[1]
		args = args[2:]
	}
	if c.verb == "explain" {
		if len(args) == 0 {
			return c, errors.New("use gregale product explain OPERATION_UUID --app SLUG")
		}
		c.operation, args = args[0], args[1:]
		if id, err := uuid.Parse(c.operation); err != nil || id == uuid.Nil {
			return c, errors.New("explain requires an operation UUID")
		}
	}
	fs := newFlagSet("product "+c.verb, flag.ContinueOnError)
	if c.verb != "init" && c.verb != "validate" {
		fs.StringVar(&c.app, "app", "", "application slug")
	}
	switch c.verb {
	case "email recoveries", "email opt-outs":
		fs.StringVar(&c.tenant, "tenant", "", "optional customer platform tenant UUID")
		fs.StringVar(&c.cursor, "cursor", "", "next notification history cursor")
	case "email list":
		fs.StringVar(&c.tenant, "tenant", "", "optional customer platform tenant UUID")
		fs.StringVar(&c.emailStatus, "status", "all", "delivery status filter")
		fs.StringVar(&c.cursor, "cursor", "", "next email history cursor")
	case "email retry":
		fs.StringVar(&c.tenant, "tenant", "", "expected customer platform tenant UUID")
		fs.StringVar(&c.deliveryID, "delivery", "", "provider-rejected delivery UUID")
		fs.IntVar(&c.failedAttempt, "attempt", 0, "failed attempt observed in delivery history; preserve for lost-response retries")
	case "access inspect", "plan-policy inspect", "team-policy inspect":
	case "access review", "plan-policy review", "team-policy review":
		fs.StringVar(&c.policyFile, "policy-file", "", "JSON role-to-permission policy to review")
		fs.StringVar(&c.file, "file", productPolicyReviewDefault(c.verb), "new private review file; preserve for retries")
	case "access apply", "plan-policy apply", "team-policy apply":
		fs.StringVar(&c.file, "file", productPolicyReviewDefault(c.verb), "saved review to apply; preserve for retries")
	case "doctor":
		fs.StringVar(&c.tenant, "tenant", "", "optional owned customer UUID for entitlement and execution checks")
	case "pilot init":
		fs.StringVar(&c.file, "file", "product-pilot.json", "new pilot manifest; no overwrite")
		fs.StringVar(&c.feature, "feature", "export", "product feature to verify")
	case "pilot verify":
		fs.StringVar(&c.file, "file", "product-pilot.json", "explicit three-customer pilot manifest")
		fs.StringVar(&c.output, "output", "", "new private JSON evidence report; no overwrite")
	case "pilot help":
		fs.StringVar(&c.tenant, "tenant", "", "expected customer platform tenant UUID")
		fs.StringVar(&c.helpID, "request", "", "retained Customer Help request UUID")
	case "init":
		fs.StringVar(&c.definition, "definition", "", "existing operation definition UUID")
		fs.StringVar(&c.operation, "operation", "", "Operation name to follow across deployments")
		fs.StringVar(&c.scope, "scope", api.DefaultEnvScope, "Operation environment scope")
		fs.StringVar(&c.file, "file", "product.json", "new catalog file")
		fs.StringVar(&c.authApp, "auth-app", "", "Application Auth app for customer login")
	case "validate", "apply":
		fs.StringVar(&c.file, "file", "product.json", "catalog JSON file")
	case "inspect", "features list", "plans list":
		fs.StringVar(&c.revision, "revision", "", "immutable catalog revision (default current)")
	case "customers list", "customers overview":
		fs.StringVar(&c.cursor, "cursor", "", "next page cursor")
	case "customers activity":
		fs.StringVar(&c.tenant, "tenant", "", "customer platform tenant UUID")
		fs.StringVar(&c.cursor, "cursor", "", "next activity page cursor")
	case "customers workspaces":
		fs.StringVar(&c.authTokenFile, "auth-token-file", "", "private file containing a short-lived Application Auth user access token")
	case "customers onboard":
		productOnboardingFlags(fs, &c)
	case "customers onboarding":
		fs.StringVar(&c.onboardingID, "id", "", "retained onboarding receipt UUID")
	case "customers assign":
		fs.StringVar(&c.tenant, "tenant", "", "customer platform tenant UUID")
		fs.StringVar(&c.plan, "plan", "", "product plan name")
		fs.StringVar(&c.authOrg, "auth-org", "", "Application Auth organization UUID (empty unlinks login)")
		fs.StringVar(&c.revision, "revision", "", "catalog revision (default current)")
		fs.BoolVar(&c.suspended, "suspended", false, "pause new feature starts")
	case "statements list", "statements review", "statements receipts", "statements finalize", "statements waive", "statements credit", "statements handoff", "statements handoff-status", "statements export", "statements export-status":
		productBillingFlags(fs, &c)
	case "billing overdue-policy inspect", "billing overdue-policy clear":
	case "billing overdue-policy set":
		fs.IntVar(&c.overdueDays, "block-overdue-after-days", -1, "days after an invoice's due date before a customer's new starts are refused (0 to 365)")
	case "usage":
		fs.StringVar(&c.tenant, "tenant", "", "customer platform tenant UUID")
		fs.StringVar(&c.period, "period", productcontrol.Period(time.Now()), "UTC usage month YYYY-MM")
		fs.BoolVar(&c.self, "self", false, "use authenticated customer's view")
	case "quote":
		fs.StringVar(&c.feature, "feature", "", "assigned product feature")
	case "start":
		fs.StringVar(&c.quote, "quote", "", "confirmed quote from gregale product quote")
		fs.StringVar(&c.feature, "feature", "", "assigned product feature")
		fs.StringVar(&c.input, "input", "", "input JSON file")
		fs.StringVar(&c.key, "key", "", "stable idempotency key for this work")
	case "explain":
		fs.IntVar(&c.after, "after", 0, "execution generation cursor")
	default:
		return c, fmt.Errorf("unknown product command %q", c.verb)
	}
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, errors.New("unexpected positional arguments")
	}
	if c.verb != "init" && c.verb != "validate" && c.app == "" {
		return c, errors.New("--app is required")
	}
	if c.verb == "init" {
		if (c.definition == "") == (c.operation == "") {
			return c, errors.New("use either --operation NAME or --definition UUID")
		}
		if c.definition != "" {
			if id, err := uuid.Parse(c.definition); err != nil || id == uuid.Nil {
				return c, errors.New("--definition requires an operation definition UUID")
			}
		}
		if c.operation != "" && api.ValidateScope(c.scope) != nil {
			return c, errors.New("--scope requires an explicit valid environment")
		}
	}
	if (c.verb == "access review" || c.verb == "plan-policy review" || c.verb == "team-policy review") && (c.policyFile == "" || c.file == "") {
		return c, errors.New("--policy-file and --file are required for an access review")
	}
	if (c.verb == "access apply" || c.verb == "plan-policy apply" || c.verb == "team-policy apply") && c.file == "" {
		return c, errors.New("--file is required")
	}
	if c.after < 0 {
		return c, errors.New("--after must be a nonnegative execution generation")
	}
	if strings.HasPrefix(c.verb, "email ") {
		if err := validateProductEmailCommand(c); err != nil {
			return c, err
		}
	}
	if c.verb == "doctor" && c.tenant != "" && !validCustomerOperationUUID(c.tenant) {
		return c, errors.New("--tenant requires a customer UUID")
	}
	if c.verb == "pilot help" {
		for _, value := range []string{c.tenant, c.helpID} {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil || id.String() != value {
				return c, errors.New("--tenant and --request require canonical UUIDs")
			}
		}
	}
	if c.verb == "customers overview" && c.cursor != "" && !validCustomerOperationUUID(c.cursor) {
		return c, errors.New("--cursor requires the last customer UUID from the previous overview page")
	}
	if c.verb == "customers activity" {
		id, err := uuid.Parse(c.tenant)
		if err != nil || id == uuid.Nil || id.String() != c.tenant || len(c.cursor) > api.ProductControlOwnerCursorMaxBytes {
			return c, errors.New("--tenant requires a canonical customer UUID; --cursor must be a bounded activity continuation")
		}
	}
	if c.verb == "customers assign" && (c.tenant == "" || c.plan == "") {
		return c, errors.New("--tenant and --plan are required")
	}
	if c.verb == "customers onboard" || c.verb == "customers workspaces" || c.verb == "customers onboarding" {
		if err := validateProductOnboardingCommand(c); err != nil {
			return c, err
		}
	}
	if strings.HasPrefix(c.verb, "statements ") {
		if err := validateProductBillingCommand(c); err != nil {
			return c, err
		}
	}
	if c.verb == "billing overdue-policy set" && (c.overdueDays < 0 || c.overdueDays > api.ProductOverdueBlockMaxDays) {
		return c, fmt.Errorf("--block-overdue-after-days must be 0 to %d (0 holds as soon as an invoice is past due)", api.ProductOverdueBlockMaxDays)
	}
	if c.verb == "usage" {
		if err := productcontrol.ValidatePeriod(c.period); err != nil {
			return c, errors.New("--period must be YYYY-MM")
		}
		if (!c.self && c.tenant == "") || (c.self && c.tenant != "") {
			return c, errors.New("use either --tenant or --self")
		}
	}
	if c.verb == "start" && (c.feature == "" || c.input == "" || c.key == "") {
		return c, errors.New("--feature, --input and a stable --key are required")
	}
	if c.authOrg != "" {
		if id, err := uuid.Parse(c.authOrg); err != nil || id == uuid.Nil || id.String() != c.authOrg {
			return c, errors.New("--auth-org must be a canonical organization UUID")
		}
	}
	if c.verb == "quote" && c.feature == "" {
		return c, errors.New("--feature is required")
	}
	if c.verb == "start" {
		if raw, err := hex.DecodeString(c.quote); err != nil || len(raw) != 32 {
			return c, errors.New("--quote requires a confirmed hash from gregale product quote; preserve it and --key for retries")
		}
	}
	return c, nil
}

func cmdProduct(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale product <init|validate|apply|inspect|features|plans|customers|doctor|pilot|email|quote|start|explain|usage|statements|billing|access|plan-policy|team-policy>", "product-control")
		return 1
	}
	c, err := parseProductCommand(args)
	if err != nil {
		return printErr("Invalid product command", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var value any
	switch c.verb {
	case "pilot init":
		value, err = initProductPilot(c)
	case "init":
		value, err = initProductCatalog(c)
	case "validate":
		value, err = readProductCatalog(c.file)
	default:
		client, authErr := authedClient()
		if authErr != nil {
			return printErr("Not logged in", authErr)
		}
		value, err = runProductCommand(ctx, client, c)
	}
	if err != nil {
		return printErr("Product Control failed", err)
	}
	if err := writeProductOutput(osStdout, value, jsonOutput); err != nil {
		return printErr("Write output", err)
	}
	return productReportExit(value)
}

func readProductCatalog(path string) (api.ProductCatalog, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.ProductControlMaxCatalogBytes+1))
	if err != nil {
		return api.ProductCatalog{}, err
	}
	if len(body) > api.ProductControlMaxCatalogBytes {
		return api.ProductCatalog{}, errors.New("catalog exceeds maximum size")
	}
	canonical, err := operations.CanonicalJSON(body)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	var spec api.ProductCatalogSpec
	decoder := json.NewDecoder(strings.NewReader(string(canonical)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return api.ProductCatalog{}, err
	}
	return productcontrol.Compile(spec)
}

func initProductCatalog(c productCommand) (any, error) {
	feature := api.ProductFeature{Label: "Export"}
	if c.operation != "" {
		feature.Operation = &api.ProductOperationBinding{Name: c.operation, Scope: c.scope}
	} else {
		id, err := uuid.Parse(c.definition)
		if err != nil || id == uuid.Nil {
			return nil, errors.New("a valid Operation definition or name is required")
		}
		feature.DefinitionID = id.String()
	}
	spec := api.ProductCatalogSpec{AuthApp: c.authApp, Currency: "EUR", Features: map[string]api.ProductFeature{"export": feature}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 25000}}}}}
	if _, err := productcontrol.Compile(spec); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(c.file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(spec)
	closeErr := file.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return map[string]string{"file": c.file, "status": "created"}, nil
}

func runProductCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	switch c.verb {
	case "email list", "email retry", "email recoveries", "email opt-outs":
		return runProductEmailCommand(ctx, client, c)
	case "doctor":
		report, err := client.GetProductDoctor(ctx, c.app, c.tenant)
		if err != nil {
			return nil, err
		}
		if err := validateProductDoctorReport(report, c.tenant); err != nil {
			return nil, err
		}
		return report, nil
	case "team-policy inspect", "team-policy review", "team-policy apply":
		return runProductTeamPolicyCommand(ctx, client, c)
	case "plan-policy inspect", "plan-policy review", "plan-policy apply":
		return runProductPlanPolicyCommand(ctx, client, c)
	case "access inspect", "access review", "access apply":
		return runProductAccessCommand(ctx, client, c)
	case "pilot verify":
		return verifyProductPilot(ctx, client, c)
	case "pilot help":
		return inspectProductPilotHelp(ctx, client, c)
	case "apply":
		catalog, err := readProductCatalog(c.file)
		if err != nil {
			return nil, err
		}
		return client.PutProductCatalog(ctx, c.app, catalog.Spec)
	case "inspect", "features list", "plans list":
		catalog, err := client.GetProductCatalog(ctx, c.app, c.revision)
		if err != nil {
			return nil, err
		}
		if c.verb == "features list" {
			return catalog.Spec.Features, nil
		}
		if c.verb == "plans list" {
			return catalog.Spec.Plans, nil
		}
		return catalog, nil
	case "customers list":
		return client.ListProductCustomers(ctx, c.app, c.cursor)
	case "customers overview":
		page, err := client.GetProductCustomerOverview(ctx, c.app, c.cursor)
		if err == nil {
			err = validateProductCustomerOverview(page, c)
		}
		return page, err
	case "customers activity":
		page, err := client.GetProductCustomerActivity(ctx, c.app, c.tenant, c.cursor)
		if err == nil {
			err = validateProductCustomerActivity(page, c)
		}
		return page, err
	case "customers assign":
		return client.SetProductCustomer(ctx, c.app, api.ProductCustomer{TenantID: c.tenant, Plan: c.plan, Revision: c.revision, Suspended: c.suspended, AuthOrganizationID: c.authOrg})
	case "customers onboard", "customers workspaces", "customers onboarding":
		return runProductOnboardingCommand(ctx, client, c)
	case "statements list", "statements review", "statements receipts", "statements finalize", "statements waive", "statements credit", "statements handoff", "statements handoff-status", "statements export", "statements export-status":
		return runProductBillingCommand(ctx, client, c)
	case "billing overdue-policy inspect", "billing overdue-policy set", "billing overdue-policy clear":
		return runProductOverdueCommand(ctx, client, c)
	case "usage":
		var usage api.ProductUsage
		var err error
		if c.self {
			var portal api.ProductPortal
			portal, err = client.GetProductPortal(ctx, c.app, c.period)
			if portal.Usage != nil {
				usage = *portal.Usage
			}
		} else {
			usage, err = client.GetProductUsage(ctx, c.app, c.tenant, c.period)
		}
		if err != nil {
			return nil, err
		}
		return usage, nil
	case "quote":
		view, err := client.GetProductPortal(ctx, c.app, "")
		if err != nil {
			return nil, err
		}
		grant, ok := view.Catalog.Spec.Plans[view.Customer.Plan].Features[c.feature]
		if !ok || view.Customer.Suspended {
			return nil, errors.New("feature is unavailable in the assigned plan")
		}
		remaining := grant.MonthlyStarts
		for _, line := range view.Usage.Lines {
			if line.Feature == c.feature {
				remaining -= line.Starts
			}
		}
		if remaining < 0 {
			remaining = 0
		}
		return map[string]any{"quote": view.Quotes[c.feature], "feature": c.feature, "plan": view.Customer.Plan, "currency": view.Catalog.Spec.Currency, "unit_price_millicents": grant.UnitPriceMillicents, "monthly_starts": grant.MonthlyStarts, "remaining_starts": remaining, "period": view.Usage.Period, "revision": view.Catalog.Revision}, nil
	case "start":
		return startProductFeature(ctx, client, c)
	case "explain":
		return client.ExplainProductOperation(ctx, c.app, c.operation, c.after)
	}
	return nil, errors.New("unsupported product command")
}

func startProductFeature(ctx context.Context, client *Client, c productCommand) (any, error) {
	file, err := openCustomerFile(c.input)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	input, err := io.ReadAll(io.LimitReader(file, api.OperationSubmissionMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(input) > api.OperationSubmissionMaxBytes {
		return nil, errors.New("input exceeds the operation size limit")
	}
	canonical, err := operations.CanonicalJSON(input)
	if err != nil {
		return nil, err
	}
	return client.StartProductFeature(ctx, api.ProductStartRequest{App: c.app, Feature: c.feature, Input: canonical, ExpectedQuote: c.quote}, c.key)
}

func writeProductOutput(out io.Writer, value any, asJSON bool) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if asJSON {
		return encoder.Encode(value)
	}
	if handled, err := writeProductEmailOutput(out, value); handled {
		return err
	}
	if handled, err := writeProductTeamPolicyOutput(out, value); handled {
		return err
	}
	if handled, err := writeProductPlanPolicyOutput(out, value); handled {
		return err
	}
	if handled, err := writeProductAccessOutput(out, value); handled {
		return err
	}
	if handled, err := writeProductOnboardingOutput(out, value); handled {
		return err
	}
	if report, ok := value.(api.ProductDoctorReport); ok {
		return writeProductDoctor(out, report)
	}
	if page, ok := value.(api.ProductCustomerOverviewPage); ok {
		return writeProductCustomerOverview(out, page)
	}
	if page, ok := value.(api.ProductCustomerActivityPage); ok {
		return writeProductCustomerActivity(out, page)
	}
	if report, ok := value.(api.ProductPilotReport); ok {
		_, err := fmt.Fprintf(out, "Ledger: %s · native qualification: %s\nManifest SHA-256: %s\n", report.LedgerState, report.QualificationState, report.ManifestSHA256)
		if err != nil {
			return err
		}
		return encoder.Encode(report)
	}
	if explanation, ok := value.(api.ProductOperationExplanation); ok {
		return writeProductExplanation(out, explanation)
	}
	if usage, ok := value.(api.ProductUsage); ok {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "%s · %s · provisional\nFEATURE\tSTARTS\tSUCCEEDED\tMILLICENTS\n", usage.Period, usage.Currency)
		for _, line := range usage.Lines {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\n", line.Feature, line.Starts, line.Succeeded, line.AmountMillicents)
		}
		return w.Flush()
	}
	if features, ok := value.(map[string]api.ProductFeature); ok {
		names := make([]string, 0, len(features))
		for name := range features {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			binding := features[name].DefinitionID
			if operation := features[name].Operation; operation != nil {
				binding = operation.Name + "@" + operation.Scope + " (live)"
			}
			if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", name, features[name].Label, binding); err != nil {
				return err
			}
		}
		return nil
	}
	return encoder.Encode(value)
}

func writeProductExplanation(out io.Writer, view api.ProductOperationExplanation) error {
	r := view.Evidence.Receipt
	lines := []string{
		"Operation " + view.Evidence.OperationID,
		"Customer " + view.Evidence.TenantID + " · feature " + r.Feature,
		"Entitlement " + view.EntitlementDecision + " · plan " + r.Plan + " · catalog " + r.Revision,
		"Admitted " + r.AdmittedAt.UTC().Format(time.RFC3339),
	}
	if evidence := r.Admission; evidence != nil {
		lines = append(lines, fmt.Sprintf("Allowance %d / %d starts used before this admission", evidence.StartsBefore, evidence.MonthlyStarts),
			"Binding "+evidence.Name+"@"+evidence.Scope+" · definition "+evidence.DefinitionID+" · deployment "+evidence.DeploymentID)
	} else {
		lines = append(lines, "Historical binding and allowance evidence unavailable for this older receipt")
	}
	if view.Outcome != nil {
		lines = append(lines, fmt.Sprintf("Outcome %s · generation %d · result available %t · delivery %s", view.Outcome.State, view.Outcome.Generation, view.Outcome.ResultAvailable, view.Outcome.CompletionDelivery))
	} else {
		lines = append(lines, "Operation detail unavailable; commercial receipt retained")
	}
	lines = append(lines, fmt.Sprintf("Price %s %d millicents per success · charge %s · amount %d millicents · %s (provisional)", r.Currency, r.UnitPriceMillicents, view.Charge.State, view.Charge.AmountMillicents, r.Period))
	for _, execution := range view.Executions {
		lines = append(lines, fmt.Sprintf("Execution generation %d · %s · attempts %d · invocation %s · job %s · workflow %s", execution.Generation, execution.State, execution.Attempts, execution.InvocationID, execution.JobRunID, execution.WorkflowRunID))
	}
	if view.NextGeneration != 0 {
		lines = append(lines, fmt.Sprintf("More executions: --after %d", view.NextGeneration))
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}
