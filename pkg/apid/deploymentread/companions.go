package deploymentread

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Values are parsed only as opaque JSON and are never included in projection.
type storedCompanion struct {
	Name           string                     `json:"name"`
	Image          string                     `json:"image"`
	Preset         string                     `json:"preset"`
	Type           string                     `json:"type"`
	RAMMB          int                        `json:"ram_mb"`
	CPUMillicores  int                        `json:"cpu_millicores"`
	ScratchMB      int                        `json:"scratch_mb"`
	DiskIOProfile  string                     `json:"disk_io_profile"`
	Port           int                        `json:"port"`
	PrimaryIngress bool                       `json:"primary_ingress"`
	Essential      *bool                      `json:"essential"`
	DependsOn      []api.WorkloadDependency   `json:"depends_on"`
	Env            map[string]json.RawMessage `json:"env"`
	EnvSecrets     map[string]json.RawMessage `json:"env_secrets"`
}

var declarationName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var environmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func projectCompanions(raw json.RawMessage) (string, string, []api.CompanionDeclaration) {
	out := make([]api.CompanionDeclaration, 0)
	unavailable := func() (string, string, []api.CompanionDeclaration) {
		return "unavailable", "declaration_metadata_unavailable", make([]api.CompanionDeclaration, 0)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "available", "", out
	}
	var stored []storedCompanion
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored) > api.SidecarCapMax {
		return unavailable()
	}
	seen := make(map[string]bool)
	for _, row := range stored {
		if !declarationName.MatchString(row.Name) || row.Name == "main" || seen[row.Name] || (row.Type != "init" && row.Type != "sidecar") || len(row.DependsOn) > api.WorkloadDependencyCapMax {
			return unavailable()
		}
		seen[row.Name] = true
		if row.Image != "" && !api.ValidCompanionImageReference(row.Image) {
			return unavailable()
		}
		if row.Image == "" && row.Preset == "" {
			return unavailable()
		}
		if row.Preset != "" {
			preset, ok := api.NormalizeCompanionPreset(row.Preset)
			if !ok {
				return unavailable()
			}
			row.Preset = string(preset)
		}
		if row.RAMMB < 0 || row.CPUMillicores < 0 || row.ScratchMB < 0 || row.Port < 0 || row.Port > 65535 {
			return unavailable()
		}
		if row.DiskIOProfile != "" && row.DiskIOProfile != "low" && row.DiskIOProfile != "standard" && row.DiskIOProfile != "high" {
			return unavailable()
		}
		for _, dependency := range row.DependsOn {
			if !declarationName.MatchString(dependency.Name) {
				return unavailable()
			}
			switch dependency.Condition {
			case "", "started", "healthy", "completed_successfully":
			default:
				return unavailable()
			}
		}
		keys := make(map[string]bool)
		for _, env := range []map[string]json.RawMessage{row.Env, row.EnvSecrets} {
			for key := range env {
				if !environmentName.MatchString(key) {
					return unavailable()
				}
				keys[key] = true
			}
		}
		envKeys := make([]string, 0, len(keys))
		for key := range keys {
			envKeys = append(envKeys, key)
		}
		sort.Strings(envKeys)
		dependencies := append(make([]api.WorkloadDependency, 0, len(row.DependsOn)), row.DependsOn...)
		out = append(out, api.CompanionDeclaration{Name: row.Name, Image: row.Image, Preset: row.Preset, Type: row.Type, RAMMB: row.RAMMB, CPUMillicores: row.CPUMillicores, ScratchMB: row.ScratchMB, DiskIOProfile: row.DiskIOProfile, Port: row.Port, PrimaryIngress: row.PrimaryIngress, Essential: row.Essential, DependsOn: dependencies, EnvironmentKeys: envKeys})
	}
	return "available", "", out
}
