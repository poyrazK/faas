package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCDControlPlanePromotesActiveReleaseCLI(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	deploy := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	promote := strings.Index(workflow, "ln -sfn /opt/faas/current/bin/gregalectl /usr/local/bin/.gregalectl-${RELEASE_ID}")
	atomicMove := strings.Index(workflow, "mv -Tf /usr/local/bin/.gregalectl-${RELEASE_ID} /usr/local/bin/gregalectl")
	if deploy < 0 || promote < 0 || atomicMove < 0 {
		t.Fatalf("control-plane workflow is missing deploy/promote steps: deploy=%d promote=%d move=%d", deploy, promote, atomicMove)
	}
	if !(deploy < promote && promote < atomicMove) {
		t.Fatalf("control-plane CLI promotion must follow successful activation: deploy=%d promote=%d move=%d", deploy, promote, atomicMove)
	}
	if got := strings.Count(workflow, "/usr/local/bin/gregalectl"); got != 1 {
		t.Fatalf("control-plane workflow has %d canonical CLI destinations, want exactly 1", got)
	}
}
