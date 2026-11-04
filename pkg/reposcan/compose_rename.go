package reposcan

import (
	"fmt"
	"strings"
)

// composeRenamedFrom validates a service's x-gregale-renamed-from, the name
// it had before (ADR-20261029090000). The name must not be the service's own,
// a service still in the file, or another service's previous name. renamedBy
// records each previous name against the service that claims it.
func composeRenamedFrom(services map[string]composeCandidate, name, raw string, hasWorkload bool, renamedBy map[string]string) (string, error) {
	previous := strings.TrimSpace(raw)
	if previous == "" {
		return "", nil
	}
	if !hasWorkload {
		return "", fmt.Errorf("x-gregale-renamed-from requires a deployable workload")
	}
	if previous == name {
		return "", fmt.Errorf("x-gregale-renamed-from names the service itself")
	}
	if _, still := services[previous]; still {
		return "", fmt.Errorf("x-gregale-renamed-from names %q, which is still a service in this file", previous)
	}
	if other, claimed := renamedBy[previous]; claimed {
		return "", fmt.Errorf("x-gregale-renamed-from names %q, which %s also renames", previous, other)
	}
	renamedBy[previous] = name
	return previous, nil
}
