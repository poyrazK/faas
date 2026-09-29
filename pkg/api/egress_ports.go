package api

import (
	"fmt"
	"net/http"
	"slices"
)

// NormalizeEgressPorts validates the extra TCP ports an app declares on top
// of TenantEgressBasePorts (ADR-361) and returns the canonical stored form:
// sorted, de-duplicated, without the base ports. An empty result clears the
// app's extra ports and is accepted on every plan, since it only narrows
// egress.
func NormalizeEgressPorts(plan Plan, raw []int) ([]int, *Problem) {
	base := TenantEgressBasePorts()
	out := make([]int, 0, len(raw))
	for _, port := range raw {
		if port < 1 || port > 65535 {
			return nil, ErrInvalidEgressPort(port, "ports must be between 1 and 65535")
		}
		if reason, forbidden := TenantEgressForbiddenPort(port); forbidden {
			return nil, ErrInvalidEgressPort(port, reason+" ports cannot be opened")
		}
		if slices.Contains(base, uint16(port)) || slices.Contains(out, port) {
			continue
		}
		out = append(out, port)
	}
	if len(out) == 0 {
		return out, nil
	}
	maxPorts := plan.EgressExtraPortsMax()
	if maxPorts == 0 {
		return nil, ErrPlanEgressPortsNotAllowed(plan)
	}
	if len(out) > maxPorts {
		return nil, ErrEgressPortsTooMany(len(out), maxPorts)
	}
	slices.Sort(out)
	return out, nil
}

// ErrPlanEgressPortsNotAllowed (ADR-361) is returned when a plan without an
// extra-port allowance declares one.
func ErrPlanEgressPortsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanEgressPortsNotAllowed,
		"Plan doesn't allow extra egress ports",
		fmt.Sprintf("the %s plan can reach TCP 80 and 443 only; upgrade to Pro or Scale to declare extra ports.", p)).
		WithLimit(0, 1).
		WithDocs(docsBase + "/apps#egress-ports")
}

// ErrEgressPortsTooMany (ADR-361) is returned when the declared extra ports
// exceed the plan's cap.
func ErrEgressPortsTooMany(got, maxPorts int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEgressPortsTooMany,
		"Too many egress ports",
		fmt.Sprintf("egress_ports has %d ports; plan caps it at %d.", got, maxPorts)).
		WithLimit(int64(maxPorts), int64(got)).
		WithDocs(docsBase + "/apps#egress-ports")
}

// ErrInvalidEgressPort (ADR-361) is returned for an out-of-range or
// forbidden port.
func ErrInvalidEgressPort(port int, reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeInvalidEgressPort,
		"Invalid egress port",
		fmt.Sprintf("egress port %d is not allowed: %s.", port, reason)).
		WithDocs(docsBase + "/apps#egress-ports")
}
