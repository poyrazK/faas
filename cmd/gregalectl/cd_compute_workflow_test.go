package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCDComputeWorkflowSupportsPrepareThenActivate(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	for _, required := range []string{
		"rollout_phase:",
		"default: full",
		"compute_targets:",
		"ROLLOUT_PHASE: ${{ inputs.rollout_phase }}",
		"prepare) JOIN_ARGS+=(--prepare-only)",
		"activate) JOIN_ARGS+=(--activate-prepared)",
		"deploy join-fleet",
		"--max-parallel 2",
		"--prepare-only",
		"if: inputs.rollout_phase != 'activate'",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("phased compute rollout is missing %q", required)
		}
	}
	for _, step := range []string{
		"Verify compute registry lifecycle authorization",
		"Verify compute OCI cache uses fast storage after activation",
		"Verify guest service proxy listener after activation",
		"Verify imaged public-smoke tenant routing after activation",
		"Verify private compute gateway reachability",
	} {
		start := strings.Index(workflow, "- name: "+step)
		if start < 0 {
			t.Errorf("missing post-activation step %q", step)
			continue
		}
		end := strings.Index(workflow[start:], "\n      - name:")
		if end < 0 {
			end = len(workflow) - start
		}
		if !strings.Contains(workflow[start:start+end], "if: inputs.rollout_phase != 'prepare'") {
			t.Errorf("post-activation step %q does not skip preparation", step)
		}
	}
}

func TestCDComputeWorkflowPinsDynamicHostForPostJoinProbes(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	adopt := strings.Index(workflow, "- name: Verify runner prerequisites and adopt the compute host")
	pin := strings.Index(workflow, "- name: Pin adopted compute SSH key for post-join probes")
	probe := strings.Index(workflow, "- name: Verify compute registry lifecycle authorization")
	if adopt < 0 || pin < 0 || probe < 0 || !(adopt < pin && pin < probe) {
		t.Fatalf("post-join SSH pin step ordering is invalid: adopt=%d pin=%d probe=%d", adopt, pin, probe)
	}
	end := strings.Index(workflow[pin:], "\n      - name:")
	if end < 0 {
		t.Fatal("cannot isolate post-join SSH pin step")
	}
	step := workflow[pin : pin+end]
	for _, required := range []string{
		"if: inputs.rollout_phase != 'prepare'",
		`ssh-keyscan -T 10 -p "$SSH_PORT" "$SSH_HOST"`,
		`ssh-keygen -lf "$candidate" -E sha256`,
		`[[ "$candidate_fingerprint" == "$SSH_HOST_KEY_SHA256" ]]`,
		`cat "$verified_keys" >>"$ARTIFACT_DIR/compute-known-hosts"`,
	} {
		if !strings.Contains(step, required) {
			t.Errorf("post-join SSH pinning is missing %q", required)
		}
	}
	if strings.Contains(step, "StrictHostKeyChecking=no") || strings.Contains(step, "accept-new") {
		t.Fatal("post-join SSH pinning must not weaken strict host-key checking")
	}
}

func TestCDComputeWorkflowRequiresExplicitFleetPreflightSkip(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)

	input := regexp.MustCompile(`(?ms)^      skip_fleet_preflight:\n(?:        .*\n)*?        default: false\n(?:        .*\n)*?        type: boolean$`)
	if !input.MatchString(workflow) {
		t.Fatal("cd-compute skip_fleet_preflight input must be boolean and default false")
	}
	if !strings.Contains(workflow, `SKIP_FLEET_PREFLIGHT: ${{ inputs.skip_fleet_preflight }}`) {
		t.Fatal("cd-compute workflow does not pass the explicit input to the adoption step")
	}

	guard := regexp.MustCompile(`(?ms)if \[\[ "\$SKIP_FLEET_PREFLIGHT" == "true" \]\]; then\n(?:            .*\n)*?            JOIN_ARGS\+\=\(--skip-fleet-preflight\)\n          fi`)
	if !guard.MatchString(workflow) {
		t.Fatal("cd-compute workflow must guard --skip-fleet-preflight behind an explicit true input")
	}
	if got := strings.Count(workflow, "JOIN_ARGS+=(--skip-fleet-preflight)"); got != 1 {
		t.Fatalf("cd-compute workflow has %d fleet-preflight skip arguments, want exactly 1", got)
	}
}

func TestCDComputeWorkflowPassesReleaseTagToBundleValidation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- name: Download and validate signed fleet enrollment bundle")
	if start < 0 {
		t.Fatal("cannot find signed fleet enrollment validation step")
	}
	end := strings.Index(workflow[start:], "\n      - name:")
	if end < 0 {
		t.Fatal("cannot isolate signed fleet enrollment validation step")
	}
	step := workflow[start : start+end]
	if !strings.Contains(step, `RELEASE_TAG: ${{ inputs.release_tag }}`) {
		t.Fatal("signed fleet enrollment validation does not receive the release tag")
	}
	if !strings.Contains(step, `/releases/tags/${RELEASE_TAG}`) {
		t.Fatal("signed fleet enrollment validation does not use the exported release tag")
	}
	if !strings.Contains(step, `/releases/${release_id}`) {
		t.Fatal("signed fleet enrollment validation must fetch assets from release ID metadata")
	}
}

func TestFleetEnrollmentWorkflowsUseCachedSourceAndPinnedCosignBinary(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"),
		filepath.Join("..", "..", ".github", "workflows", "fleet-enrollment.yml"),
	}
	const (
		cosignURL    = "https://github.com/sigstore/cosign/releases/download/v2.4.1/cosign-linux-amd64"
		cosignSHA256 = "8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b"
	)
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		workflow := string(body)
		for _, required := range []string{cosignURL, cosignSHA256, "sha256sum -c -"} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s is missing %q", filepath.Base(path), required)
			}
		}
		if strings.Contains(workflow, "go install github.com/sigstore/cosign") {
			t.Errorf("%s still compiles cosign during every run", filepath.Base(path))
		}
	}

	computeBody, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, dependencyPath := range []string{
		"cache-dependency-path: claim-source/go.sum",
		"cache-dependency-path: bundle-source/go.sum",
	} {
		if !strings.Contains(string(computeBody), dependencyPath) {
			t.Errorf("cd-compute Go cache is missing %q", dependencyPath)
		}
	}
}

func TestFleetEnrollmentGCSAuthIsFreshAndKeylessPerJob(t *testing.T) {
	computePath := filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml")
	computeBody, err := os.ReadFile(computePath)
	if err != nil {
		t.Fatal(err)
	}
	compute := string(computeBody)
	for _, required := range []string{
		"id-token: write",
		"providers/gregale-fleet-reader",
		"gregale-fleet-reader@",
		"Authenticate fleet bundle reader with GitHub OIDC",
		"token_format: access_token",
		"https://www.googleapis.com/auth/devstorage.read_only",
		`GCP_FLEET_BUNDLE_ACCESS_TOKEN: ${{ steps.fleet_bundle_auth.outputs.access_token }}`,
	} {
		if !strings.Contains(compute, required) {
			t.Errorf("cd-compute keyless bundle auth is missing %q", required)
		}
	}
	if got := strings.Count(compute, "Authenticate fleet bundle reader with GitHub OIDC"); got != 2 {
		t.Fatalf("cd-compute GCS auth steps = %d, want hosted preflight plus post-queue deploy", got)
	}
	platformBody, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(platformBody), "id-token: write") {
		t.Fatal("cd-platform caller must grant its reusable compute jobs OIDC token permission")
	}

	publisherPath := filepath.Join("..", "..", ".github", "workflows", "fleet-enrollment.yml")
	publisherBody, err := os.ReadFile(publisherPath)
	if err != nil {
		t.Fatal(err)
	}
	publisher := string(publisherBody)
	for _, required := range []string{
		"providers/gregale-fleet-publisher",
		"gregale-fleet-publisher@",
		"Authenticate fleet bundle publisher with GitHub OIDC",
		"https://www.googleapis.com/auth/devstorage.read_write",
	} {
		if !strings.Contains(publisher, required) {
			t.Errorf("fleet-enrollment keyless publisher auth is missing %q", required)
		}
	}

	identityPath := filepath.Join("..", "..", "scripts", "ops", "gcp_fleet_enrollment_identity.sh")
	identityBody, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	identity := string(identityBody)
	for _, required := range []string{
		"fleet-enrollment.yml@refs/heads/main",
		"cd-compute.yml@refs/heads/main",
		"roles/storage.objectCreator",
		"roles/storage.objectViewer",
		"roles/iam.workloadIdentityUser",
	} {
		if !strings.Contains(identity, required) {
			t.Errorf("fleet enrollment identity convergence is missing %q", required)
		}
	}
	if strings.Contains(identity, "roles/storage.objectAdmin") {
		t.Fatal("fleet enrollment identities must not receive destructive objectAdmin access")
	}
}

func TestFleetEnrollmentRunsHaveDigestStableNames(t *testing.T) {
	computeBody, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(computeBody), "run-name: Compute ${{ inputs.node || 'fleet' }} ${{ inputs.fleet_bundle_sha256 || inputs.release_tag }}") {
		t.Fatal("cd-compute run name must identify the exact node and signed bundle digest")
	}
	publisherBody, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "fleet-enrollment.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publisherBody), "run-name: Sign fleet bundle ${{ inputs.bundle_sha256 }}") {
		t.Fatal("fleet-enrollment run name must identify the exact signed bundle digest")
	}
}

func TestCDComputeWorkflowDownloadsCanonicalAssetsFromOneLookupInParallel(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- name: Download and verify the canonical release")
	end := strings.Index(workflow, "- name: Assemble the standard join artifact directory")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("cannot isolate the canonical release download step")
	}
	download := workflow[start:end]
	for _, want := range []string{
		`-o "$release_json" "$release_api"`,
		`jq -er --arg asset "$asset"`,
		`download_asset "$asset" &`,
		`download_pids+=("$!")`,
		`if ! wait "$pid"; then`,
		"one or more canonical release assets failed to download",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("parallel canonical release download is missing %q", want)
		}
	}
	if got := strings.Count(download, "/releases/tags/${RELEASE_TAG}"); got != 1 {
		t.Fatalf("canonical release metadata lookups = %d, want 1", got)
	}
	if !strings.Contains(download, `/releases/${release_id}`) {
		t.Fatal("canonical release download must fetch assets from release ID metadata")
	}
}

func TestCDControlplaneWorkflowDownloadsCanonicalAssetsByReleaseID(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatalf("read cd-controlplane workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- name: Download and verify canonical release")
	end := strings.Index(workflow, "- name: Set up SSH")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("cannot isolate control-plane canonical release download step")
	}
	download := workflow[start:end]
	for _, want := range []string{
		`/releases/tags/${RELEASE_TAG}`,
		`/releases/${release_id}`,
		`.assets[] | select(.name == $name) | .browser_download_url`,
		`sha256sum -c SHA256SUMS`,
		`cosign verify-blob`,
	} {
		if !strings.Contains(download, want) {
			t.Errorf("control-plane canonical release download is missing %q", want)
		}
	}
	if strings.Contains(download, `gh release download`) {
		t.Fatal("control-plane canonical release download must not use by-tag asset listing")
	}
}

func TestCDComputeWorkflowUsesInfrastructureHealthHost(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- name: Verify private compute gateway reachability")
	if start < 0 {
		t.Fatal("cannot find private compute gateway reachability step")
	}
	end := strings.Index(workflow[start:], "\n      - name:")
	if end < 0 {
		t.Fatal("cannot isolate private compute gateway reachability step")
	}
	step := workflow[start : start+end]

	if !strings.Contains(step, `--header 'Host: gatewayd-internal.faas'`) {
		t.Fatal("cd-compute private reachability probe must use the gateway infrastructure health host")
	}
	if strings.Contains(step, `--header 'Host: health-probe.invalid'`) {
		t.Fatal("cd-compute private reachability probe must not route health checks through the unknown-app path")
	}
	if !strings.Contains(step, `grep -q '^# HELP gateway_compute_node_changed_subscriber_alive '`) {
		t.Fatal("cd-compute metrics probe must require a family registered by gatewayd-internal")
	}
	if strings.Contains(step, "gatewayd_ops_total") {
		t.Fatal("cd-compute metrics probe must not require the removed gatewayd_ops_total family")
	}
	for _, required := range []string{
		`$1 == "private_dns:" { selected = 1; next }`,
		`gateway_host="${NODE}.${private_dns_zone}"`,
		"selected dynamic node $NODE has no private DNS zone",
	} {
		if !strings.Contains(step, required) {
			t.Errorf("dynamic compute private reachability is missing %q", required)
		}
	}
}

func TestCDComputeWorkflowVerifiesFastCacheAfterActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	cacheGate := strings.Index(workflow, "Verify compute OCI cache uses fast storage after activation")
	gatewayGate := strings.Index(workflow, "Verify private compute gateway reachability")
	if join < 0 || cacheGate < 0 || gatewayGate < 0 || !(join < cacheGate && cacheGate < gatewayGate) {
		t.Fatalf("fast-cache post gate order is invalid: join=%d cache=%d gateway=%d", join, cacheGate, gatewayGate)
	}
	for _, want := range []string{
		"mountpoint -q /var/lib/faas/cache",
		"stat -c %d /var/lib/faas/cache",
		"stat -c %d /srv/fc",
		"findmnt -n -o FSTYPE --mountpoint /var/lib/faas/cache",
		"xfs_info /srv/fc",
		"reflink=1",
		"test -g /var/lib/faas/cache",
		"-mindepth 1",
		"! -group faas",
		"! -perm -g+w",
		"cache permission contract violation",
	} {
		if !strings.Contains(workflow[cacheGate:gatewayGate], want) {
			t.Errorf("fast-cache post gate is missing %q", want)
		}
	}
	if strings.Contains(workflow[cacheGate:gatewayGate], "! -perm -2000") {
		t.Fatal("fast-cache post gate must not require setgid on restricted-daemon child directories")
	}
}

func TestCDComputeWorkflowVerifiesGuestServiceProxyAfterActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	proxyGate := strings.Index(workflow, "Verify guest service proxy listener after activation")
	if join < 0 || proxyGate < 0 || join >= proxyGate {
		t.Fatalf("guest service proxy gate must follow activation: join=%d gate=%d", join, proxyGate)
	}
	for _, want := range []string{"service_proxy_listen", `10\.100\.0\.1:10080`, "10.100.0.1:53", "ss -ltnH", "ss -lunH"} {
		if !strings.Contains(workflow[proxyGate:], want) {
			t.Errorf("guest service proxy gate is missing %q", want)
		}
	}
}

func TestCDComputeWorkflowVerifiesHostingSmokeTenantRouting(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	smokeGate := strings.Index(workflow, "Verify imaged public-smoke tenant routing after activation")
	gatewayGate := strings.Index(workflow, "Verify private compute gateway reachability")
	if join < 0 || smokeGate < 0 || gatewayGate < 0 || !(join < smokeGate && smokeGate < gatewayGate) {
		t.Fatalf("public-smoke config gate order is invalid: join=%d smoke=%d gateway=%d", join, smokeGate, gatewayGate)
	}
	for _, want := range []string{
		"systemctl show faas-imaged.service --property=Environment --value",
		"FAAS_API_HOSTING_SMOKE_REQUIRED=1",
		"FAAS_API_HOSTING_SMOKE_URL=https://",
		"FAAS_APPS_DOMAIN=",
	} {
		if !strings.Contains(workflow[smokeGate:gatewayGate], want) {
			t.Errorf("public-smoke config gate is missing %q", want)
		}
	}

	template, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "compute_only_service", "templates", "zz-faas-api-hosting-smoke.conf.j2"))
	if err != nil {
		t.Fatalf("read hosting-smoke drop-in: %v", err)
	}
	if !strings.Contains(string(template), "Environment=FAAS_APPS_DOMAIN={{ gatewayd_apps_domain | default('gregale.dev') }}") {
		t.Fatal("compute hosting-smoke drop-in does not render the tenant apps domain")
	}
}
