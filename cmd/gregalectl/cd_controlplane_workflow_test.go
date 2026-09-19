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

func TestCDControlPlaneObservesCustomerPathDuringActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	observer := strings.Index(workflow, "scripts/ci/observe_rollout_availability.sh")
	publicPath := -1
	if observer >= 0 {
		if offset := strings.Index(workflow[observer:], "https://api.gregale.dev/v1/status"); offset >= 0 {
			publicPath = observer + offset
		}
	}
	activate := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	if observer < 0 || publicPath < 0 || activate < 0 || !(observer <= publicPath && publicPath < activate) {
		t.Fatalf("customer-path observer must wrap activation: observer=%d public=%d activate=%d", observer, publicPath, activate)
	}
	scriptBody, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "observe_rollout_availability.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptBody)
	for _, required := range []string{
		"ROLLOUT_BASELINE_SAMPLE_COUNT",
		"sample baseline",
		"Baseline: **",
		"Rollout attribution is **inconclusive**",
		"HTTP status counts:",
		"customer path lost after a healthy pre-rollout baseline",
		"ROLLOUT_PROBE_PROXY",
		`--proxy "$probe_proxy"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("customer-path observer is missing baseline diagnostic %q", required)
		}
	}
	if strings.Contains(script, `--user-agent "gregale-rollout-observer/`) {
		t.Fatal("customer-path observer must use the same edge identity as the final public gate")
	}
	for _, required := range []string{
		`ssh -N -D "127.0.0.1:${probe_port}"`,
		`probe_proxy="socks5h://127.0.0.1:${probe_port}"`,
		`ROLLOUT_PROBE_PROXY="$probe_proxy" scripts/ci/observe_rollout_availability.sh`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("control-plane rollout must observe from the GCP vantage point; missing %q", required)
		}
	}
}

func TestCDControlPlaneVerifiesSBOMBeforeActivationAndAcceptsAfterHealth(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	verify := strings.Index(workflow, "release kgv verify --git-sha ${RELEASE_ID}")
	activate := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	health := strings.Index(workflow, "Converge Prometheus config and rules")
	acceptStep := strings.Index(workflow, "Accept activated release SBOM baseline")
	accept := strings.Index(workflow, `release kgv rotate --git-sha '${RELEASE_ID}'`)
	if verify < 0 || activate < 0 || health < 0 || acceptStep < 0 || accept < 0 {
		t.Fatalf("control-plane workflow is missing KGV lifecycle: verify=%d activate=%d health=%d acceptStep=%d accept=%d", verify, activate, health, acceptStep, accept)
	}
	if !(verify < activate && activate < health && health < acceptStep && acceptStep < accept) {
		t.Fatalf("KGV lifecycle must verify before activation and accept only after health: verify=%d activate=%d health=%d acceptStep=%d accept=%d", verify, activate, health, acceptStep, accept)
	}
}

func TestCDControlPlaneReusesVerifiedImmutableRelease(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)

	verify := strings.Index(workflow, `test -x '${release_dir}/bin/deployctl' && '${release_dir}/bin/deployctl' bundle-check-installed '${release_dir}'`)
	reuse := strings.Index(workflow, `reusing verified immutable release ${RELEASE_ID}`)
	activeGuard := strings.Index(workflow, `if [[ "$current_release" == "$release_dir" ]]`)
	remove := strings.Index(workflow, `rm -rf -- "$release_dir"`)
	upload := strings.Index(workflow, `"root@${{ env.CP_HOST }}:${release_dir}/.transport.tar.gz"`)
	reconstruct := strings.Index(workflow, `--directory "$release_dir/bin" --no-same-owner`)
	activate := strings.Index(workflow, `${release_dir}/bin/deployctl deploy ${RELEASE_ID}`)
	if verify < 0 || reuse < 0 || activeGuard < 0 || remove < 0 || upload < 0 || reconstruct < 0 || activate < 0 {
		t.Fatalf("control-plane workflow is missing idempotent release handling: verify=%d reuse=%d guard=%d remove=%d upload=%d reconstruct=%d activate=%d", verify, reuse, activeGuard, remove, upload, reconstruct, activate)
	}
	if !(verify < reuse && reuse < activeGuard && activeGuard < remove && remove < upload && upload < reconstruct && reconstruct < activate) {
		t.Fatalf("control-plane release handling is out of order: verify=%d reuse=%d guard=%d remove=%d upload=%d reconstruct=%d activate=%d", verify, reuse, activeGuard, remove, upload, reconstruct, activate)
	}
	for _, want := range []string{
		`rm -rf -- "${TRANSPORT_ROOT:?}/bin"`,
		`"$BUNDLE_ROOT/bin/deployctl" "$TRANSPORT_ROOT/controller-bin/deployctl"`,
		`"$BUNDLE_ROOT/bin/migrate" "$TRANSPORT_ROOT/controller-bin/migrate"`,
		`"$release_dir/release.tar.gz"`,
		`rm -f -- "$release_dir/bin/release-manifest.json"`,
		`rm -rf -- "${release_dir:?}/controller-bin"`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("control-plane compact transport is missing %q", want)
		}
	}
	if strings.Contains(workflow, `"${BUNDLE_ROOT}/." "root@${{ env.CP_HOST }}:${release_dir}/"`) {
		t.Fatal("control-plane workflow still uploads the unpacked canonical binaries beside release.tar.gz")
	}
}

func TestCDControlPlaneConvergesAPIDLifecycleStorageBeforeActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	cleanup := strings.Index(workflow, "Converge APID lifecycle storage credential")
	activate := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	verify := strings.Index(workflow, "Verify canonical storage credential source")
	if cleanup < 0 || activate < 0 || verify < 0 {
		t.Fatalf("control-plane storage convergence is incomplete: cleanup=%d activate=%d verify=%d", cleanup, activate, verify)
	}
	if !(cleanup < activate && activate < verify) {
		t.Fatalf("legacy storage overrides must be removed before activation and verified after: cleanup=%d activate=%d verify=%d", cleanup, activate, verify)
	}
	for _, required := range []string{
		"APID_STORAGE_LIFECYCLE_ENV: ${{ secrets.COMPUTE_IMAGED_STORAGE_ENV }}",
		"/etc/faas/apid-storage.env",
		"/etc/systemd/system/faas-apid.service.d/99-storage-lifecycle.conf",
		"/etc/faas/oci-e2e.env",
		"/etc/systemd/system/faas-apid.service.d/99-codex-oci-source.conf",
		"/etc/systemd/system/faas-apid.service.d/99-oci-e2e.conf",
		"/etc/systemd/system/faas-schedd.service.d/99-oci-e2e.conf",
		"process_password",
		"/proc/$pid/environ",
		"assert_process_credential faas-apid /etc/faas/apid-storage.env",
		"assert_process_credential faas-schedd /etc/faas/storage.env",
		"gregalectl artifact lifecycle-check",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("control-plane workflow is missing storage convergence contract %q", required)
		}
	}
}

func TestCDControlPlaneRepairsSharedCachePermissionsBeforeActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	root := strings.Index(workflow, "install -d -o faas-apid -g faas -m 2770 /var/lib/faas/cache")
	repair := strings.Index(workflow, "find /var/lib/faas/cache -type d")
	group := strings.Index(workflow, "-exec chgrp faas {} + -exec chmod 2770 {} +")
	activate := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	if root < 0 || repair < 0 || group < 0 || activate < 0 {
		t.Fatalf("control-plane cache convergence is incomplete: root=%d repair=%d group=%d activate=%d", root, repair, group, activate)
	}
	if !(root < repair && repair <= group && group < activate) {
		t.Fatalf("cache shards must be repaired before activation: root=%d repair=%d group=%d activate=%d", root, repair, group, activate)
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
	if !strings.Contains(unitList, "faas-apid.socket") {
		t.Error("control-plane workflow does not bundle the durable APID socket")
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

func TestCDControlPlaneAcceptsIdleWakeWindow(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	if !strings.Contains(workflow, "wake is None or valid(wake)") {
		t.Fatal("control-plane rollout gate must accept wake_p95_ms=null during an idle window")
	}
}

func TestCDControlPlaneVerifiesPostgresBackupContractAfterActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	bundle := strings.Index(workflow, "host-config/faas-pg-backup-contract-preflight.sh")
	deploy := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	verify := strings.Index(workflow, "Verify PostgreSQL backup namespace contract")
	run := strings.Index(workflow, "/opt/faas/current/host-config/faas-pg-backup-contract-preflight.sh")
	if bundle < 0 || deploy < 0 || verify < 0 || run < 0 {
		t.Fatalf("control-plane workflow is missing PostgreSQL backup contract verification: bundle=%d deploy=%d verify=%d run=%d", bundle, deploy, verify, run)
	}
	if !(bundle < deploy && deploy < verify && verify < run) {
		t.Fatalf("PostgreSQL backup contract must be bundled and checked after activation: bundle=%d deploy=%d verify=%d run=%d", bundle, deploy, verify, run)
	}
}

func TestCDControlPlaneConvergesKeylessBackupIdentityBeforeActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	bundle := strings.Index(workflow, `"${BUNDLE_ROOT}/host-config/faas-rclone-backup-identity.py"`)
	install := strings.Index(workflow, `/usr/local/lib/faas/faas-rclone-backup-identity.py`)
	archive := strings.Index(workflow, `ALTER SYSTEM SET archive_command`)
	deploy := strings.Index(workflow, `deployctl deploy ${RELEASE_ID}`)
	if bundle < 0 || install < 0 || archive < 0 || deploy < 0 {
		t.Fatalf("control-plane workflow is missing backup identity convergence: bundle=%d install=%d archive=%d deploy=%d", bundle, install, archive, deploy)
	}
	if !(bundle < install && install < archive && archive < deploy) {
		t.Fatalf("backup identity must be bundled and converged before activation: bundle=%d install=%d archive=%d deploy=%d", bundle, install, archive, deploy)
	}
	for _, required := range []string{
		"faas-pg-basebackup-push.service faas-pg-wal-prune.service",
		"FAAS_RCLONE_BIN=/usr/local/lib/faas/faas-rclone-backup-identity.py",
		"offhostbox:faas-pg-wal/%f",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("control-plane workflow is missing backup identity contract %q", required)
		}
	}
}

func TestCDControlPlaneConvergesPostgresConnectionMetricsBeforeActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	bundle := strings.Index(workflow, `"${BUNDLE_ROOT}/host-config/faas-postgres-connection-metrics"`)
	install := strings.Index(workflow, `/usr/local/bin/faas-postgres-connection-metrics`)
	enable := strings.Index(workflow, `systemctl enable --now faas-postgres-connection-metrics.timer`)
	verify := strings.Index(workflow, `faas_postgres_ordinary_connection_capacity [1-9][0-9]*`)
	deploy := strings.Index(workflow, `deployctl deploy ${RELEASE_ID}`)
	if bundle < 0 || install < 0 || enable < 0 || verify < 0 || deploy < 0 {
		t.Fatalf("control-plane workflow is missing PostgreSQL connection metrics convergence: bundle=%d install=%d enable=%d verify=%d deploy=%d", bundle, install, enable, verify, deploy)
	}
	if !(bundle < install && install < enable && enable < verify && verify < deploy) {
		t.Fatalf("PostgreSQL connection metrics must converge before activation: bundle=%d install=%d enable=%d verify=%d deploy=%d", bundle, install, enable, verify, deploy)
	}
	for _, required := range []string{
		"faas-postgres-connection-metrics.service",
		"faas-postgres-connection-metrics.timer",
		"/var/lib/node_exporter/textfile_collector/faas_postgres_connections.prom",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("control-plane workflow is missing PostgreSQL connection metrics contract %q", required)
		}
	}
}

func TestCDControlPlanePromotesDPAArtifactWithRelease(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	bundle := strings.Index(workflow, `install -m 0644 docs/DPA.md "${BUNDLE_ROOT}/host-config/dpa.md"`)
	deploy := strings.Index(workflow, "deployctl deploy ${RELEASE_ID}")
	install := strings.Index(workflow, "${release_dir}/host-config/dpa.md /etc/faas/.dpa.md-${RELEASE_ID}")
	if bundle < 0 || deploy < 0 || install < 0 {
		t.Fatalf("control-plane workflow is missing versioned DPA handling: bundle=%d deploy=%d install=%d", bundle, deploy, install)
	}
	if !(bundle < deploy && deploy < install) {
		t.Fatalf("DPA must be bundled before activation and installed after it: bundle=%d deploy=%d install=%d", bundle, deploy, install)
	}
}
