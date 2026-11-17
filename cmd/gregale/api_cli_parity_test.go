// adr: 20261112223000

package main_test

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// parityExemptionsFile freezes the customer settings the CLI cannot reach
// (ADR-20261112223000 §3). Each line is `<Request>.<json_field> <reason>`.
const parityExemptionsFile = "../../docs/cli-api-parity-exemptions.txt"

// parityExemptionCeiling is the number of frozen gaps. Closing a gap removes
// its line and lowers this number; it may never rise, so a new API-only field
// fails CI instead of joining the list.
const parityExemptionCeiling = 8

func jsonFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = t.Field(i).Name
		}
		out = append(out, name)
	}
	return out
}

func TestDeployRequestFieldsHaveReviewedCLISurfaces(t *testing.T) {
	deploy, ok := main.LookupCliCommand("deploy")
	if !ok {
		t.Fatal("deploy command missing from CLI manifest")
	}
	for _, c := range []struct {
		name     string
		typ      reflect.Type
		surfaces map[string]main.UpdateAppCLISurface
	}{
		{"CreateDeploymentRequest", reflect.TypeOf(api.CreateDeploymentRequest{}), main.DeployRequestCLISurfaces()},
		{"CreateDeploymentOverrides", reflect.TypeOf(api.CreateDeploymentOverrides{}), main.DeployOverridesCLISurfaces()},
	} {
		seen := map[string]bool{}
		for _, field := range jsonFields(c.typ) {
			seen[field] = true
			surface, ok := c.surfaces[field]
			switch {
			case !ok:
				t.Errorf("%s.%s has no reviewed CLI surface; add a path or a known gap (and an exemption line)", c.name, field)
			case (surface.Path == "") == (surface.KnownGap == ""):
				t.Errorf("%s.%s needs exactly one of a CLI path or a known gap", c.name, field)
			}
			for _, flag := range surface.AppFlags {
				if !cliCommandHasFlag(deploy, flag) {
					t.Errorf("%s.%s maps to --%s, which gregale deploy does not declare", c.name, field, flag)
				}
			}
		}
		for field := range c.surfaces {
			if !seen[field] {
				t.Errorf("%s has no JSON field %q", c.name, field)
			}
		}
	}
}

// TestCLIAPIParityExemptionsAreFrozen: every known gap is in the exemption
// file and every line there is still a gap, and the file never grows.
func TestCLIAPIParityExemptionsAreFrozen(t *testing.T) {
	gaps := map[string]bool{}
	for prefix, surfaces := range map[string]map[string]main.UpdateAppCLISurface{
		"UpdateAppRequest":          *main.UpdateAppRequestCLISurfaces,
		"CreateDeploymentRequest":   main.DeployRequestCLISurfaces(),
		"CreateDeploymentOverrides": main.DeployOverridesCLISurfaces(),
	} {
		for field, surface := range surfaces {
			if surface.KnownGap != "" {
				gaps[prefix+"."+field] = true
			}
		}
	}
	body, err := os.ReadFile(parityExemptionsFile)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, _ := strings.Cut(line, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemption %s gives no reason", key)
		}
		listed[key] = true
	}
	var missing, stale []string
	for gap := range gaps {
		if !listed[gap] {
			missing = append(missing, gap)
		}
	}
	for key := range listed {
		if !gaps[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, gap := range missing {
		t.Errorf("%s is API-only and not in docs/cli-api-parity-exemptions.txt: give it a CLI flag or gregale.yaml key", gap)
	}
	for _, key := range stale {
		t.Errorf("docs/cli-api-parity-exemptions.txt lists %s, which the CLI now reaches (or no longer exists): delete the line and lower parityExemptionCeiling", key)
	}
	if len(listed) > parityExemptionCeiling {
		t.Errorf("%d exemptions exceed the ceiling of %d; the list may only shrink", len(listed), parityExemptionCeiling)
	}
	if len(listed) < parityExemptionCeiling {
		t.Errorf("%d exemptions, ceiling %d: lower parityExemptionCeiling to %d so the list stays frozen", len(listed), parityExemptionCeiling, len(listed))
	}
}
