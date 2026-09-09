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

func TestCDControlPlaneReusesVerifiedImmutableRelease(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)

	verify := strings.Index(workflow, `test -x '${release_dir}/bin/deployctl' && '${release_dir}/bin/deployctl' bundle-check '${release_dir}'`)
	reuse := strings.Index(workflow, `reusing verified immutable release ${RELEASE_ID}`)
	activeGuard := strings.Index(workflow, `if [[ "$current_release" == "$release_dir" ]]`)
	remove := strings.Index(workflow, `rm -rf -- "$release_dir"`)
	upload := strings.Index(workflow, `"${BUNDLE_ROOT}/." "root@${{ env.CP_HOST }}:${release_dir}/"`)
	activate := strings.Index(workflow, `${release_dir}/bin/deployctl deploy ${RELEASE_ID}`)
	if verify < 0 || reuse < 0 || activeGuard < 0 || remove < 0 || upload < 0 || activate < 0 {
		t.Fatalf("control-plane workflow is missing idempotent release handling: verify=%d reuse=%d guard=%d remove=%d upload=%d activate=%d", verify, reuse, activeGuard, remove, upload, activate)
	}
	if !(verify < reuse && reuse < activeGuard && activeGuard < remove && remove < upload && upload < activate) {
		t.Fatalf("control-plane release handling is out of order: verify=%d reuse=%d guard=%d remove=%d upload=%d activate=%d", verify, reuse, activeGuard, remove, upload, activate)
	}
}
