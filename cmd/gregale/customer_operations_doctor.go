package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationDoctorCommand struct {
	app, deployment, tenant, name string
	timeout                       time.Duration
}
type customerOperationDoctorClient interface {
	GetOperationDoctor(context.Context, string, string, string, string) (api.OperationDoctorResponse, error)
}

func parseCustomerOperationDoctor(args []string) (customerOperationDoctorCommand, error) {
	var c customerOperationDoctorCommand
	fs := newFlagSet("customer-operations doctor", flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "owned application")
	fs.StringVar(&c.deployment, "deployment", "", "exact deployment ID")
	fs.StringVar(&c.tenant, "tenant", "", "owned platform tenant ID")
	fs.StringVar(&c.name, "name", "", "optional operation name")
	fs.DurationVar(&c.timeout, "timeout", 0, "local diagnostic deadline")
	if err := parseInterspersed(fs, args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 || c.app == "" || !validCustomerOperationUUID(c.deployment) || !validCustomerOperationUUID(c.tenant) || c.timeout < 0 || len(c.name) > api.OperationNameMaxBytes {
		return c, fmt.Errorf("doctor requires --app, --deployment and --tenant; selectors and timeout must be valid")
	}
	return c, nil
}

func cmdCustomerOperationDoctor(args []string) int {
	c, err := parseCustomerOperationDoctor(args)
	if err != nil {
		return printErr("Invalid Operations diagnostic command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	code, err := runCustomerOperationDoctor(ctx, client, c, osStdout, jsonOutput)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		_ = printErr("Local diagnostic request timed out", err)
		return 124
	}
	if err != nil {
		return printErr("Operations diagnostics failed", err)
	}
	return code
}

func runCustomerOperationDoctor(ctx context.Context, client customerOperationDoctorClient, c customerOperationDoctorCommand, out io.Writer, asJSON bool) (int, error) {
	r, err := client.GetOperationDoctor(ctx, c.app, c.deployment, c.tenant, c.name)
	if err != nil {
		return 3, err
	}
	if err := validateCustomerOperationDoctor(r, c); err != nil {
		return 3, &exitErr{code: 3, msg: err.Error()}
	}
	if asJSON {
		err = json.NewEncoder(out).Encode(r)
	} else {
		_, err = fmt.Fprintf(out, "Submission: %s (observed %s; responding API node only)\n", r.SubmissionState, r.ObservedAt.UTC().Format(time.RFC3339Nano))
		for _, check := range r.Checks {
			if err != nil {
				break
			}
			name := check.Check
			if check.Name != "" {
				name += "/" + check.Name
			}
			_, err = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n  %s\n", check.Impact, check.Status, name, check.Code, check.Message)
			if err == nil && check.Remediation != "" {
				_, err = fmt.Fprintf(out, "  %s\n", check.Remediation)
			}
		}
	}
	if err != nil {
		return 3, err
	}
	switch r.SubmissionState {
	case "eligible":
		return 0, nil
	case "blocked":
		return 1, nil
	default:
		return 3, nil
	}
}

func validateCustomerOperationDoctor(r api.OperationDoctorResponse, c customerOperationDoctorCommand) error {
	deployment, depErr := uuid.Parse(r.DeploymentID)
	wantedDeployment, _ := uuid.Parse(c.deployment)
	tenant, tenantErr := uuid.Parse(r.PlatformTenantID)
	wantedTenant, _ := uuid.Parse(c.tenant)
	if depErr != nil || tenantErr != nil || !validCustomerOperationUUID(r.AppID) || api.ValidateScope(r.Scope) != nil || deployment != wantedDeployment || tenant != wantedTenant || r.ObservedAt.IsZero() || r.ObservationScope != "responding_api_node" || len(r.Checks) == 0 || len(r.Checks) > api.OperationDoctorChecksMax || r.SubmissionState != r.ObservedSubmissionState() || !slices.Contains([]string{"eligible", "blocked", "unknown"}, r.SubmissionState) {
		return fmt.Errorf("invalid or mismatched Operations diagnostic report")
	}
	seen := map[string]bool{}
	definitionObserved := false
	for _, check := range r.Checks {
		key := check.Check + "/" + check.DefinitionID
		if seen[key] {
			return fmt.Errorf("duplicate Operations diagnostic check")
		}
		seen[key] = true
		if check.Check == "definition_contract" || check.Check == "definitions" {
			definitionObserved = true
		}
		if check.Check == "" || check.Code == "" || check.Message == "" || !slices.Contains([]string{"submission", "delivery", "qualification"}, check.Impact) || !slices.Contains([]string{"observed", "configured", "blocked", "warning", "unknown", "not_requested"}, check.Status) || (check.Status == "blocked" && check.Impact != "submission") || (check.Status == "warning" && check.Impact == "submission") || (c.name != "" && check.Name != "" && check.Name != c.name) {
			return fmt.Errorf("invalid Operations diagnostic check")
		}
	}
	for _, required := range []string{"tenant", "plan", "deployment_pin", "pending_capacity", "preview", "workload_trust", "result_storage"} {
		if !seen[required+"/"] {
			return fmt.Errorf("incomplete Operations diagnostic report")
		}
		for _, check := range r.Checks {
			if check.Check == required && (check.Impact != "submission" || check.Status == "not_requested") {
				return fmt.Errorf("invalid submission prerequisite observation")
			}
		}
	}
	if !definitionObserved {
		return fmt.Errorf("missing definition observation")
	}
	if _, ok := api.LimitsFor(r.Plan); !ok {
		return fmt.Errorf("unknown plan observation")
	}
	return nil
}
