package main

import (
	"strings"
	"testing"
)

func TestLoadNodeKeysSQLFiltersUntrustedLifecycleRows(t *testing.T) {
	t.Parallel()
	for _, clause := range []string{
		"join compute_nodes",
		"where n.active",
		"k.revoked_at is null",
		"k.key_state = 'current'",
		"k.key_state = 'overlap' and k.valid_until > now()",
	} {
		if !strings.Contains(loadNodeKeysSQL, clause) {
			t.Errorf("node key loader query missing %q", clause)
		}
	}
}
