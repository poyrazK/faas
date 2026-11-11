package api

import (
	"fmt"
	"regexp"
)

// FunctionCallablesEnv carries an app's function_callables to its guest as
// a JSON object (ADR-20261111150000); the SDK functions clients read it.
const FunctionCallablesEnv = "GREGALE_FUNCTIONS"

var functionCallableName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidateFunctionCallables checks a function_callables map: at most
// ServiceBindingTargetsMax entries, each a function name or app slug mapped
// to a valid app slug other than the function's own.
func ValidateFunctionCallables(callables map[string]string, self string) error {
	if len(callables) > ServiceBindingTargetsMax {
		return fmt.Errorf("function_callables has %d entries; the limit is %d", len(callables), ServiceBindingTargetsMax)
	}
	for name, slug := range callables {
		if !functionCallableName.MatchString(name) {
			return fmt.Errorf("function_callables name %q must be lowercase letters, digits, - and _", name)
		}
		if !ValidAppSlug(slug) {
			return fmt.Errorf("function_callables %q: %q is not an app slug", name, slug)
		}
		if slug == self {
			return fmt.Errorf("function_callables %q names the function's own app", name)
		}
	}
	return nil
}
