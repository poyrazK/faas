package productcontrol

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func ValidatePilot(m api.ProductPilotManifest) error {
	app, err := uuid.Parse(m.AppID)
	if (m.Version != 1 && m.Version != 2) || !slug.MatchString(m.App) || err != nil || app == uuid.Nil || !slug.MatchString(m.Feature) || len(m.Cases) < 3 || len(m.Cases) > api.ProductControlPilotMaxCases {
		return fmt.Errorf("pilot requires version 1 or 2, app, app_id, feature and 3–%d explicit cases", api.ProductControlPilotMaxCases)
	}
	names, ids, baselineTenants, baselinePlans := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	baselines := 0
	for _, c := range m.Cases {
		if c.OutputFile != "" || c.OutputSHA256 != "" {
			hash, err := hex.DecodeString(c.OutputSHA256)
			if !filepath.IsLocal(c.OutputFile) || len(c.OutputFile) > api.ProductControlPilotOutputPathMaxBytes || !strings.HasSuffix(c.OutputFile, ".csv") || err != nil || len(hash) != 32 || hex.EncodeToString(hash) != c.OutputSHA256 {
				return fmt.Errorf("case %q requires a local relative .csv path and lower-case SHA-256 together", c.Name)
			}
		}
		tenant, tenantErr := uuid.Parse(c.TenantID)
		op, opErr := uuid.Parse(c.OperationID)
		revision, revErr := hex.DecodeString(c.Revision)
		if !slug.MatchString(c.Name) || names[c.Name] || tenantErr != nil || tenant == uuid.Nil || tenant.String() != c.TenantID || opErr != nil || op == uuid.Nil || op.String() != c.OperationID || ids[c.OperationID] || !slug.MatchString(c.Plan) || revErr != nil || len(revision) != 32 || hex.EncodeToString(revision) != c.Revision || c.ExpectedChargeMillicents == nil || *c.ExpectedChargeMillicents < 0 || *c.ExpectedChargeMillicents > api.ProductControlMaxUnitPriceMillicents {
			return fmt.Errorf("case %q requires unique names/operation IDs, canonical tenant/operation UUIDs, an immutable revision, plan and explicit bounded charge", c.Name)
		}
		switch c.ExpectedState {
		case api.OperationSucceeded, api.OperationFailed, api.OperationCancelled, api.OperationRequiresReconciliation:
		default:
			return fmt.Errorf("case %q requires a verifiable terminal outcome or retained reconciliation state", c.Name)
		}
		if c.ExpectedState != api.OperationSucceeded && *c.ExpectedChargeMillicents != 0 {
			return fmt.Errorf("case %q cannot expect a nonzero charge without success", c.Name)
		}
		if c.Baseline {
			if m.Version == 2 && c.OutputFile == "" {
				return fmt.Errorf("version 2 baseline cases require the downloaded CSV path and reviewed hash")
			}
			baselines++
			if baselineTenants[c.TenantID] || c.ExpectedState != api.OperationSucceeded {
				return fmt.Errorf("baseline cases require distinct customers and successful exports")
			}
			baselineTenants[c.TenantID] = true
			baselinePlans[c.Plan] = true
		}
		names[c.Name], ids[c.OperationID] = true, true
	}
	if baselines != 3 || len(baselineTenants) != 3 || len(baselinePlans) != 2 {
		return fmt.Errorf("pilot requires exactly three baseline customers on two plans")
	}
	for _, c := range m.Cases {
		if !baselineTenants[c.TenantID] {
			return fmt.Errorf("additional case %q must belong to a baseline customer", c.Name)
		}
	}
	if err := validatePilotHelp(m, names, baselineTenants); err != nil {
		return err
	}
	return validatePilotJourneys(m, names, baselineTenants)
}
