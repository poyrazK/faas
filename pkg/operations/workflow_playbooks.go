package operations

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

// Repeated milestone steps may share a rule, but cannot redefine it inside one workflow contract.
func validateWorkflowPlaybookContracts(steps []api.OperationWorkflowSpec) error {
	groups := map[string]map[string]string{}
	for _, step := range steps {
		version := step.Version
		if version == 0 {
			version = 1
		}
		key := fmt.Sprintf("%s/%d", step.Workflow, version)
		if groups[key] == nil {
			groups[key] = map[string]string{}
		}
		for _, rule := range step.Playbooks {
			encoded, err := json.Marshal(rule)
			if err != nil {
				return err
			}
			if old, exists := groups[key][rule.ID]; exists && old != string(encoded) {
				return fmt.Errorf("workflow playbook %q conflicts within its contract", rule.ID)
			}
			groups[key][rule.ID] = string(encoded)
			if len(groups[key]) > api.OperationWorkflowPlaybooksMax {
				return fmt.Errorf("workflow contract playbooks exceed their limit")
			}
		}
	}
	return nil
}
