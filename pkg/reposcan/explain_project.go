package reposcan

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/onebox-faas/faas/pkg/projectconfig"
)

// SettingLayer is one source that can set a project setting, in precedence
// order (ADR-20261107180000, `gregale config explain`).
type SettingLayer struct {
	// Source names the file or rule, e.g. "compose.yaml: api" or
	// "gregale.yaml `project:`".
	Source string `json:"source"`
	// Field is the key inside Source, e.g. "x-gregale-service-policy".
	Field string `json:"field,omitempty"`
	// Value is what this layer declares; empty when Set is false.
	Value string `json:"value,omitempty"`
	Set   bool   `json:"set"`
	// Wins marks the layer the effective value comes from. depends_on lists
	// merge, so every layer that sets one wins.
	Wins bool   `json:"wins"`
	Note string `json:"note,omitempty"`
}

// SettingExplanation is a project setting's effective value and the layers
// that decide it.
type SettingExplanation struct {
	Key      string         `json:"key"`
	Value    string         `json:"value"`
	Layers   []SettingLayer `json:"layers"`
	Warnings []string       `json:"warnings,omitempty"`
}

// ProjectSettingKeys are the keys ExplainProjectSetting accepts; <name> is a
// scanned service.
var ProjectSettingKeys = []string{
	"service_policy",
	"services.<name>.service_policy",
	"services.<name>.visibility",
	"services.<name>.depends_on",
}

// defaultServicePolicy is what reconcile gives a new app that declares no
// policy (ADR-20261022111119); an existing app keeps its stored policy.
const (
	defaultServicePolicy     = string(ServiceBindingPolicyProject)
	defaultServicePolicyNote = "a new app gets project; an existing app keeps its current policy"
)

// ExplainProjectSetting explains one project setting of the repository in
// fsys: its effective value, from the same scan a deploy runs, and every
// layer that can set it. Compose x-gregale-* keys win over the project block,
// which is read from the root gregale.yaml or the legacy
// .gregale/project.yaml.
func ExplainProjectSetting(fsys fs.FS, key string) (SettingExplanation, error) {
	service, field, err := parseProjectSettingKey(key)
	if err != nil {
		return SettingExplanation{}, err
	}
	cfg, source, warnings, err := loadProjectConfig(fsys)
	if err != nil {
		return SettingExplanation{}, err
	}
	blockSource := source
	if blockSource == "" {
		blockSource = "gregale.yaml `project:`"
	}
	if service == "" {
		policy := strings.TrimSpace(cfg.ServicePolicy)
		out := SettingExplanation{Key: key, Value: defaultServicePolicy, Warnings: warnings}
		out.Layers = []SettingLayer{
			{Source: blockSource, Field: "service_policy", Value: policy, Set: policy != "", Wins: policy != "", Note: "default for services that set none"},
			{Source: "default", Value: defaultServicePolicy, Set: true, Wins: policy == "", Note: defaultServicePolicyNote},
		}
		if policy != "" {
			out.Value = strings.ToLower(policy)
		}
		return out, nil
	}
	effective, err := Scan(fsys)
	if err != nil {
		return SettingExplanation{}, err
	}
	detected, err := ScanWithOptions(fsys, ScanOptions{withoutProjectConfig: true})
	if err != nil {
		return SettingExplanation{}, err
	}
	eff, ok := findWorkload(effective.Workloads, service)
	if !ok {
		return SettingExplanation{}, fmt.Errorf("service %q is not in the project; the scan found %s", service, workloadNames(effective.Workloads))
	}
	base, _ := findWorkload(detected.Workloads, service)
	block := serviceBlock(cfg, service)
	out := SettingExplanation{Key: key, Warnings: warnings}
	switch field {
	case "service_policy":
		composePolicy := string(base.ServiceBindingPolicy)
		servicePolicy := strings.TrimSpace(block.ServicePolicy)
		projectPolicy := strings.TrimSpace(cfg.ServicePolicy)
		out.Value = string(eff.ServiceBindingPolicy)
		if out.Value == "" {
			out.Value = defaultServicePolicy
		}
		winner := 3
		switch {
		case composePolicy != "":
			winner = 0
		case servicePolicy != "":
			winner = 1
		case projectPolicy != "":
			winner = 2
		}
		out.Layers = []SettingLayer{
			{Source: base.Source, Field: "x-gregale-service-policy", Value: composePolicy, Set: composePolicy != "", Wins: winner == 0},
			{Source: blockSource, Field: "services." + service + ".service_policy", Value: servicePolicy, Set: servicePolicy != "", Wins: winner == 1},
			{Source: blockSource, Field: "service_policy", Value: projectPolicy, Set: projectPolicy != "", Wins: winner == 2},
			{Source: "default", Value: defaultServicePolicy, Set: true, Wins: winner == 3, Note: defaultServicePolicyNote},
		}
	case "visibility":
		out.Value = visibility(eff.Unpublished)
		detectedNote := "detected services are public by default"
		if base.Unpublished {
			detectedNote = "Compose service with no published ports"
		}
		declared := strings.ToLower(strings.TrimSpace(block.Visibility))
		out.Layers = []SettingLayer{
			{Source: base.Source, Value: visibility(base.Unpublished), Set: true, Wins: declared == "", Note: detectedNote},
			{Source: blockSource, Field: "services." + service + ".visibility", Value: declared, Set: declared != "", Wins: declared != ""},
		}
	case "depends_on":
		out.Value = strings.Join(eff.DependsOn, ", ")
		detectedList := strings.Join(base.DependsOn, ", ")
		declaredList := strings.Join(block.DependsOn, ", ")
		out.Layers = []SettingLayer{
			{Source: base.Source, Field: "depends_on", Value: detectedList, Set: detectedList != "", Wins: detectedList != ""},
			{Source: blockSource, Field: "services." + service + ".depends_on", Value: declaredList, Set: declaredList != "", Wins: declaredList != "", Note: "merged with the detected list"},
		}
	}
	return out, nil
}

// parseProjectSettingKey splits a key into its service ("" for the
// project-wide service_policy) and field.
func parseProjectSettingKey(key string) (service, field string, err error) {
	key = strings.TrimSpace(key)
	if key == "service_policy" {
		return "", key, nil
	}
	rest, ok := strings.CutPrefix(key, "services.")
	if ok {
		dot := strings.LastIndex(rest, ".")
		if dot > 0 {
			service, field = rest[:dot], rest[dot+1:]
			switch field {
			case "service_policy", "visibility", "depends_on":
				return service, field, nil
			}
		}
	}
	return "", "", fmt.Errorf("unknown project setting %q (want %s)", key, strings.Join(ProjectSettingKeys, ", "))
}

func findWorkload(workloads []Workload, name string) (Workload, bool) {
	for _, w := range workloads {
		if strings.EqualFold(w.Name, name) {
			return w, true
		}
	}
	return Workload{}, false
}

func serviceBlock(cfg projectconfig.Config, name string) projectconfig.Service {
	for key, service := range cfg.Services {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return service
		}
	}
	return projectconfig.Service{}
}

func visibility(unpublished bool) string {
	if unpublished {
		return "internal"
	}
	return "public"
}
