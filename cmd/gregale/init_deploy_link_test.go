package main_test

import (
	"bytes"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-14: `init --deploy --name e2ecc-web` wrote no link, so a later
// plain `gregale deploy` in that directory derived "web" from the directory
// name and created a second app. The deployed slug is now the directory's
// standalone context.
func TestInitDeployLinksTheDirectoryToTheDeployedApp(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	main.LinkInitDeploy(dir, "e2ecc-web", &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	got, err := main.LinkedAppSlug(dir)
	if err != nil || got != "e2ecc-web" {
		t.Fatalf("linked app = %q, %v; want e2ecc-web", got, err)
	}
}
