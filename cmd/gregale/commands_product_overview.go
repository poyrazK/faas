package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func validateProductCustomerOverview(page api.ProductCustomerOverviewPage, c productCommand) error {
	if !validCustomerOperationUUID(page.AppID) || page.ObservedAt.IsZero() || page.ObservationScope != "responding_api_node" || page.QualificationState != "unverified" || productcontrol.ValidatePeriod(page.Period) != nil || page.Customers == nil || len(page.Customers) > api.ProductControlOverviewPageSize {
		return errors.New("customer overview has an invalid scope, bound or qualification claim")
	}
	previous := c.cursor
	for _, row := range page.Customers {
		id := row.Customer.TenantID
		if !validCustomerOperationUUID(id) || id <= previous || row.Customer.Plan == "" || row.Readiness.AppID != page.AppID || row.Readiness.Revision != row.Customer.Revision {
			return errors.New("customer overview does not match its assigned customer scope or order")
		}
		if err := validateProductDoctorReport(row.Readiness, id); err != nil {
			return err
		}
		if err := validateProductCustomerActivation(row.Activation); err != nil {
			return err
		}
		if row.Work.Unresolved < 0 || row.Work.NeedsReview < 0 || row.Work.NeedsReview > row.Work.Unresolved || (row.Work.NeedsReview == 0) != (row.Work.ReviewPeriod == "") || (row.Work.ReviewPeriod != "" && productcontrol.ValidatePeriod(row.Work.ReviewPeriod) != nil) {
			return errors.New("customer overview has invalid work counts or review month")
		}
		if err := validateProductOverviewActions(row.NextActions, c.app, id); err != nil {
			return err
		}
		previous = id
	}
	if page.NextCursor != "" && (len(page.Customers) == 0 || !validCustomerOperationUUID(page.NextCursor) || page.NextCursor != previous) {
		return errors.New("customer overview cursor does not match the last customer")
	}
	return nil
}

func validateProductCustomerActivation(a api.ProductCustomerActivation) error {
	if a.State != productcontrol.CustomerActivationState(a) {
		return errors.New("customer activation does not match its retained evidence")
	}
	if a.OnboardingID == "" {
		if a.SetupAt != nil || a.SetupCurrent || a.AccessConfigured || a.AccessVerifiedAt != nil || a.FirstSuccess != nil {
			return errors.New("customer activation evidence has no guided setup identity")
		}
		return nil
	}
	if !validCustomerOperationUUID(a.OnboardingID) || a.SetupAt == nil || a.SetupAt.IsZero() || (a.AccessVerifiedAt != nil && (a.AccessVerifiedAt.IsZero() || !a.AccessConfigured)) {
		return errors.New("customer activation has an invalid setup or workspace visit")
	}
	if a.FirstSuccess != nil && (!validCustomerOperationUUID(a.FirstSuccess.OperationID) || a.FirstSuccess.Feature == "" || a.FirstSuccess.VerifiedAt.IsZero()) {
		return errors.New("customer activation has invalid success evidence")
	}
	return nil
}

func validateProductOverviewActions(actions []api.ProductCustomerNextAction, app, tenant string) error {
	if len(actions) == 0 || len(actions) > api.ProductControlOverviewMaxActions {
		return errors.New("customer overview has an invalid next-action bound")
	}
	base := "/dashboard/apps/" + app + "/product-control"
	for _, action := range actions {
		u, err := url.Parse(action.URL)
		if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || (u.Path != base && u.Path != base+"/doctor" && u.Path != base+"/onboarding") || u.Query().Get("tenant") != tenant || action.Code == "" || action.Label == "" {
			return errors.New("customer overview action does not match the requested product/customer")
		}
	}
	return nil
}

func writeProductCustomerOverview(out io.Writer, page api.ProductCustomerOverviewPage) error {
	if _, err := fmt.Fprintf(out, "Assigned customers · checked %s · UTC allowance month %s\nScope: %s · native qualification: %s\nWork counts cover all retained months; activation is historical evidence after guided setup.\n", page.ObservedAt.UTC().Format(time.RFC3339), page.Period, page.ObservationScope, page.QualificationState); err != nil {
		return err
	}
	for _, row := range page.Customers {
		if err := writeProductCustomerOverviewRow(out, row); err != nil {
			return err
		}
	}
	if len(page.Customers) == 0 {
		_, err := fmt.Fprintln(out, "No assigned customers on this page.")
		return err
	}
	if page.NextCursor != "" {
		_, err := fmt.Fprintf(out, "\nMore customers: --cursor %s\n", page.NextCursor)
		return err
	}
	return nil
}

func writeProductCustomerOverviewRow(out io.Writer, row api.ProductCustomerOverview) error {
	lines := []string{
		"\n" + row.Name + " · " + row.Customer.TenantID,
		"Plan " + row.Customer.Plan + " · configuration " + row.Readiness.ConfigurationState + " · tenant " + row.TenantStatus,
		productcontrol.CustomerActivationLabel(row.Activation.State),
		fmt.Sprintf("Pending outcomes %d · need review %d (all retained months)", row.Work.Unresolved, row.Work.NeedsReview),
	}
	if row.Customer.Suspended {
		lines = append(lines, "Product access suspended")
	}
	if row.Activation.AccessVerifiedAt != nil {
		lines = append(lines, "First verified workspace visit "+row.Activation.AccessVerifiedAt.UTC().Format(time.RFC3339))
	}
	if first := row.Activation.FirstSuccess; first != nil {
		lines = append(lines, "First success after guided setup "+first.Feature+" · "+first.OperationID+" · "+first.VerifiedAt.UTC().Format(time.RFC3339))
	}
	for _, check := range row.Readiness.Checks {
		if check.Impact != "qualification" && (check.Status == "blocked" || check.Status == "unverified" || check.Status == "warning") {
			lines = append(lines, "  "+check.Status+" · "+check.Feature+" "+check.Check+" · "+check.Code+": "+check.Message)
		}
	}
	for _, action := range row.NextActions {
		lines = append(lines, "Next: "+action.Label+" · "+action.URL)
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}
