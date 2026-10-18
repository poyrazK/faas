// spec: §4.2
package main

import "testing"

func TestDockerfilePreviewRepeatedLeadingDot(t *testing.T) {
	archive := writeDockerfilePreviewArchive(t, map[string]string{
		"././repo/apps/api/Dockerfile": "FROM scratch\n",
	})
	if err := validateExplicitDockerfileArchive(archive, "apps/api"); err != nil {
		t.Fatalf("valid Dockerfile archive rejected: %v", err)
	}
}
