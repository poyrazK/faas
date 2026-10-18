package main

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateProductDoctorReport(r api.ProductDoctorReport, tenant string) error {
	id, err := uuid.Parse(r.AppID)
	if err != nil || id == uuid.Nil || r.TenantID != tenant || r.ObservationScope != "responding_api_node" || r.ObservedAt.IsZero() || r.QualificationState != "unverified" || r.ConfigurationState != r.ObservedConfigurationState() || len(r.Checks) > api.ProductControlDoctorMaxChecks {
		return errors.New("product diagnostic response has an invalid scope, bound or readiness claim")
	}
	for _, c := range r.Checks {
		switch c.Status {
		case "observed", "configured", "not_requested", "blocked", "unverified", "warning":
		default:
			return errors.New("product diagnostic response has an unknown check status")
		}
	}
	return nil
}

func productReportExit(value any) int {
	status := ""
	switch v := value.(type) {
	case api.ProductDoctorReport:
		status = v.ConfigurationState
	case api.ProductPilotReport:
		if v.HelpState == "blocked" || v.JourneyState == "blocked" {
			return 1
		}
		if v.LedgerState != "blocked" && v.JourneyState == "incomplete" {
			return 3
		}
		status = v.LedgerState
	default:
		return 0
	}
	switch status {
	case "observed":
		return 0
	case "blocked":
		return 1
	default:
		return 3
	}
}

func writeProductDoctor(out io.Writer, r api.ProductDoctorReport) error {
	if _, err := fmt.Fprintf(out, "Configuration: %s · qualification: %s\nScope: %s; no admission or capacity reserved\n", r.ConfigurationState, r.QualificationState, r.ObservationScope); err != nil {
		return err
	}
	for _, c := range r.Checks {
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n  %s\n", c.Status, c.Check, c.Feature, c.Code, c.Message); err != nil {
			return err
		}
		if c.Remediation != "" {
			if _, err := fmt.Fprintf(out, "  Next: %s\n", c.Remediation); err != nil {
				return err
			}
		}
	}
	return nil
}
