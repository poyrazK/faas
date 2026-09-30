// adr: 375
package state

import (
	"errors"
	"fmt"
	"strings"
)

func globalTrafficPolicyError(err error) error {
	if err == nil {
		return nil
	}
	var aggregate *TrafficPolicyAggregateError
	var analysis *TrafficPolicyAnalysisError
	if errors.As(err, &aggregate) {
		copy := *aggregate
		copy.Scope = "global_route_" + strings.TrimPrefix(copy.Scope, "host_")
		err = &copy
	} else if errors.As(err, &analysis) {
		copy := *analysis
		copy.Scope = "global_route_" + copy.Scope
		err = &copy
	}
	return fmt.Errorf("state: global route traffic policy: %w", err)
}
