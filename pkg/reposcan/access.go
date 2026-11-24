package reposcan

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// NormalizeServiceAccess checks a declared service access value
// (ADR-20261124090000): "" (not declared), "public" or "key".
func NormalizeServiceAccess(value string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "", api.ServiceAccessPublic, api.ServiceAccessKey:
		return v, nil
	}
	return "", fmt.Errorf("must be %s or %s, got %q", api.ServiceAccessPublic, api.ServiceAccessKey, value)
}
