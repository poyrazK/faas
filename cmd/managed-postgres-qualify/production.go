// adr: 20261028103115 — read-only check of the production provisioning gate.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

// runProductionCheck reports whether the production gate would open, and the
// configured default backend an operator pins. It never contacts a provider.
func runProductionCheck(getenv func(string) string, now time.Time, output, errorOutput io.Writer) int {
	if !managedpostgres.ProductionEnvironmentActive(getenv) {
		_, _ = fmt.Fprintln(errorOutput, "the production check requires FAAS_ENVIRONMENT=production")
		return 2
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres configuration is unavailable")
		return 2
	}
	result := struct {
		Readiness          managedpostgres.QualificationReadiness `json:"readiness"`
		Approval           managedpostgres.ProductionApproval     `json:"approval"`
		BackendID          string                                 `json:"configured_backend_id,omitempty"`
		BackendFingerprint string                                 `json:"configured_backend_fingerprint,omitempty"`
	}{Approval: managedpostgres.LoadProductionApproval(getenv)}
	result.Readiness = registry.ProductionReadiness(result.Approval, now.UTC())
	if regions := registry.Regions(); len(regions) == 1 {
		if backend, err := registry.Default(regions[0]); err == nil {
			result.BackendID, result.BackendFingerprint = backend.ID, backend.Fingerprint
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write the production check")
		return 1
	}
	if !result.Readiness.Ready {
		_, _ = fmt.Fprintln(errorOutput, "production provisioning would stay closed: "+strings.Join(result.Readiness.Reasons, ", "))
		return 1
	}
	return 0
}
