package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
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

func TestCDControlPlanePromotesVersionedStatusPage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)

	bundle := strings.Index(workflow, `"${BUNDLE_ROOT}/statuspage/index.html"`)
	seal := strings.Index(workflow, `bundle-create "${BUNDLE_ROOT}"`)
	deploy := strings.Index(workflow, `deployctl deploy ${RELEASE_ID}`)
	stage := strings.Index(workflow, `install -o root -g faas -m 0644 ${release_dir}/statuspage/index.html`)
	promote := strings.Index(workflow, `mv -Tf /etc/faas/statuspage/.index.html-${RELEASE_ID} /etc/faas/statuspage/index.html`)
	if bundle < 0 || seal < 0 || deploy < 0 || stage < 0 || promote < 0 {
		t.Fatalf("control-plane workflow is missing status-page release handling: bundle=%d seal=%d deploy=%d stage=%d promote=%d", bundle, seal, deploy, stage, promote)
	}
	if !(bundle < seal && seal < deploy && deploy < stage && stage < promote) {
		t.Fatalf("status page must be sealed before deployment and promoted after activation: bundle=%d seal=%d deploy=%d stage=%d promote=%d", bundle, seal, deploy, stage, promote)
	}
}

func TestCDControlPlaneBundlesEveryCanonicalControlPlaneDaemon(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "for unit in")
	if start < 0 {
		t.Fatalf("control-plane workflow is missing its bundled unit loop")
	}
	end := strings.Index(workflow[start:], "; do")
	if end < 0 {
		t.Fatalf("control-plane workflow has an unterminated bundled unit loop")
	}
	unitList := workflow[start : start+end]
	for _, daemon := range daemonunitspec.DaemonsForRole(daemonunitspec.RoleControlPlane) {
		unit := "faas-" + daemon + ".service"
		if !strings.Contains(unitList, unit) {
			t.Errorf("control-plane workflow does not bundle canonical unit %s", unit)
		}
	}
}

func TestCDControlPlaneConvergesOutbounddAndPublicBetaBilling(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	for _, required := range []string{
		"host-config/outboundd.toml",
		"FAAS_OUTBOUNDD_ROLE=control-plane",
		"FAAS_BILLING_MODE=disabled",
		"/etc/faas/secrets/outboundd/outboundd.env",
		"faas_map  faas-outboundd  faas",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("control-plane workflow is missing convergence contract %q", required)
		}
	}
	prerequisites := strings.Index(workflow, "outboundd was added after the original")
	deploy := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	if prerequisites < 0 || deploy < 0 || prerequisites > deploy {
		t.Fatalf("outboundd prerequisites must converge before deployctl activation: prerequisites=%d deploy=%d", prerequisites, deploy)
	}
}
