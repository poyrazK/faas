package reposcan

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func composeDependencyConditions(value any) (map[string]string, error) {
	var entries map[string]any
	switch value := value.(type) {
	case nil, []any, []string:
		return nil, nil
	case map[string]any:
		entries = value
	default:
		return nil, fmt.Errorf("depends_on must be an array or service map")
	}
	conditions := make(map[string]string, len(entries))
	for name, raw := range entries {
		definition, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("depends_on service declaration must be an object")
		}
		condition := api.ComposeDependencyStarted
		if rawCondition, exists := definition["condition"]; exists {
			condition, ok = rawCondition.(string)
			if !ok {
				return nil, fmt.Errorf("depends_on condition must be a string")
			}
		}
		if required, exists := definition["required"]; exists {
			flag, valid := required.(bool)
			if !valid || (!flag && condition == api.ComposeDependencyHealthy) {
				return nil, fmt.Errorf("healthy dependency gates require required: true")
			}
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if _, duplicate := conditions[key]; duplicate {
			return nil, fmt.Errorf("depends_on has duplicate normalized service names")
		}
		conditions[key] = condition
	}
	if err := api.ValidateComposeDependencyConditions(conditions, dependencyNames(value)); err != nil {
		return nil, err
	}
	return conditions, nil
}
