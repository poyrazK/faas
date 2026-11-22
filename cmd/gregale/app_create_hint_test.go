package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r28: `gregale app create NAME` answered "unexpected positional
// argument". Apps are created by deploying; say how.
func TestAppCreatePointsAtDeploy(t *testing.T) {
	_, err := main.RewriteCliTree([]string{"app", "create", "shop"})
	if err == nil || !strings.Contains(err.Error(), "deploy --name shop") || !strings.Contains(err.Error(), "--create-only") {
		t.Fatalf("app create advice: %v", err)
	}
	got, err := main.RewriteCliTree([]string{"app", "create"})
	if err != nil || strings.Join(got, " ") != "app create" {
		t.Fatalf("bare app create must still read an app named create: %v %v", got, err)
	}
}
