package productcontrol

import (
	"fmt"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

func PilotJourneyStage(stage string) bool {
	switch stage {
	case "sign_in", "paid_feature", "usage", "plan_change", "help_request", "owner_response", "inbox_update":
		return true
	}
	return false
}

func validatePilotJourneys(m api.ProductPilotManifest, names, tenants map[string]bool) error {
	if len(m.Journeys) > api.ProductControlPilotMaxJourneys {
		return fmt.Errorf("pilot journeys exceed the three-customer bound")
	}
	if m.Version == 2 && len(m.Journeys) != api.ProductControlPilotMaxJourneys {
		return fmt.Errorf("version 2 requires all three customer journeys")
	}
	seen := map[string]bool{}
	for _, j := range m.Journeys {
		if !slug.MatchString(j.Name) || names[j.Name] || !tenants[j.TenantID] || seen[j.TenantID] || !pilotHasOperation(m, j.TenantID, j.OperationID) || !pilotHasHelp(m, j.TenantID, j.HelpRequestID) || len(j.Steps) > api.ProductControlPilotMaxJourneySteps {
			return fmt.Errorf("journey %q requires a unique baseline customer and selected run/help references", j.Name)
		}
		if m.Version == 2 && !pilotBaselineJourney(m, j) {
			return fmt.Errorf("version 2 journeys require the customer's baseline export and help about that same run")
		}
		if err := validatePilotJourneySteps(j); err != nil {
			return err
		}
		names[j.Name], seen[j.TenantID] = true, true
	}
	return nil
}

func pilotBaselineJourney(m api.ProductPilotManifest, j api.ProductPilotJourney) bool {
	baseline, help := false, false
	for _, c := range m.Cases {
		if c.Baseline && c.TenantID == j.TenantID && c.OperationID == j.OperationID {
			baseline = true
		}
	}
	for _, c := range m.HelpCases {
		if c.TenantID == j.TenantID && c.RequestID == j.HelpRequestID && c.Kind == "run" && c.OperationID == j.OperationID {
			help = true
		}
	}
	return baseline && help
}

func pilotHasHelp(m api.ProductPilotManifest, tenant, id string) bool {
	for _, c := range m.HelpCases {
		if c.TenantID == tenant && c.RequestID == id {
			return true
		}
	}
	return false
}

func validatePilotJourneySteps(j api.ProductPilotJourney) error {
	if j.StartedAt.IsZero() != (len(j.Steps) == 0) {
		return fmt.Errorf("journey %q needs a start time and observations together", j.Name)
	}
	previous := j.StartedAt
	for _, s := range j.Steps {
		if !PilotJourneyStage(s.Stage) || (s.Outcome != "observed" && s.Outcome != "blocked") || s.ObservedAt.IsZero() || s.ObservedAt.Before(previous) || !filepath.IsLocal(s.EvidenceFile) || len(s.EvidenceFile) > api.ProductControlPilotOutputPathMaxBytes || !pilotDigest(s.EvidenceSHA256) {
			return fmt.Errorf("journey %q requires ordered observations, known stages/outcomes and bounded local evidence references", j.Name)
		}
		previous = s.ObservedAt
	}
	return nil
}
