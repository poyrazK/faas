// spec: §4.2
package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestDockerfilePreviewRepeatedLeadingDot(t *testing.T) {
	archive := writeDockerfilePreviewArchive(t, map[string]string{
		"././repo/apps/api/Dockerfile": "FROM scratch\n",
	})
	if err := main.ValidateExplicitDockerfileArchive(archive, "apps/api"); err != nil {
		t.Fatalf("valid Dockerfile archive rejected: %v", err)
	}
}
