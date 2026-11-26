package main_test

import (
	"strings"
	"testing"
)

// Prod hunt r54 (rc.381): `release traffic rollback` in the app's checkout
// printed usage mid-split; `rollback` there already took the linked app.
func TestTrafficRollbackUsesTheLinkedApp(t *testing.T) {
	paths := linkedAppServer(t, nil)
	_, _, stderr := runCLI(t, "release", "traffic", "rollback", "--reason", "test")
	if strings.Contains(stderr, "usage:") {
		t.Fatalf("traffic rollback printed usage in a linked checkout:\n%s", stderr)
	}
	for _, p := range *paths {
		if strings.Contains(p, "/apps/web/") {
			return
		}
	}
	t.Fatalf("traffic rollback sent no request for the linked app: %q", *paths)
}
